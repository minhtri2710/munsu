package taskauthority

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// plantInterruptedJournal writes an interrupted home.Commit journal record for
// the given scope and change-set and returns the root. home.Open recovery
// replays the items mechanically. expectedRevision is the pre-commit scope
// revision; newRevision is the post-commit revision the interrupted commit
// would have produced.
func plantInterruptedJournal(t *testing.T, root, scope, txnID string, expectedRevision, newRevision uint64, items []home.ChangeItem) {
	t.Helper()
	journalRec := struct {
		TxnID            string            `json:"txn_id"`
		Scope            string            `json:"scope"`
		FenceToken       uint64            `json:"fence_token"`
		ExpectedRevision uint64            `json:"expected_revision"`
		NewRevision      uint64            `json:"new_revision"`
		Items            []home.ChangeItem `json:"items"`
		Committed        bool              `json:"committed"`
	}{
		TxnID: txnID, Scope: scope, FenceToken: 1,
		ExpectedRevision: expectedRevision, NewRevision: newRevision, Committed: false,
		Items: items,
	}
	data, err := json.Marshal(journalRec)
	if err != nil {
		t.Fatal(err)
	}
	journalDir := filepath.Join(root, home.JournalDirName)
	if err := os.WriteFile(filepath.Join(journalDir, scope+"."+txnID+".json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

// reopenCanonical reopens the home and returns a fresh Canonical over the
// recovered state.
func reopenCanonical(t *testing.T, root string) *Canonical {
	t.Helper()
	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	return c2
}

// TestCanonicalReserveTransferInterruptedCommitRecovers proves an interrupted
// ReserveTransfer commit is recovered mechanically: the reservation is present
// exactly once, the revision advances exactly once, and the fence is active.
func TestCanonicalReserveTransferInterruptedCommitRecovers(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")

	next := Aggregate{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        "t1",
		Generation:    1,
		Revision:      2,
		Current:       true,
		Definition:    TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"},
		Phase:         PhaseQueued,
		Transfer: &TransferState{
			ReservationID:   "res-t1",
			DestinationHome: "dest-home",
			FenceToken:      "fence-res-t1",
			ReservedAt:      1000,
		},
	}
	docData, _ := json.Marshal(taskDoc{HomeRevision: 2, Aggregate: next})
	rec := receipt{OperationID: "op-interrupted-reserve", Digest: "intent", TaskID: "t1", Generation: 1, Revision: 2, Phase: string(PhaseQueued)}
	recData, _ := json.Marshal(rec)
	plantInterruptedJournal(t, root, taskScope("t1"), "op-interrupted-reserve", 1, 2, []home.ChangeItem{
		{Root: home.RootState, Key: taskCurrentKey("t1"), Data: docData},
		{Root: home.RootState, Key: receiptKey("op-interrupted-reserve"), Data: recData},
	})

	c2 := reopenCanonical(t, root)
	agg, err := c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("read recovered task: %v", err)
	}
	if agg.Revision != 2 || agg.Transfer == nil || agg.Transfer.ReservationID != "res-t1" {
		t.Fatalf("recovered aggregate = %+v", agg)
	}
	// The fence is active on the recovered reservation.
	start := CanonicalStartRequest{HomeID: c2.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 2), Reason: "start"}
	if _, err := c2.Start(mustOperation(t, "op-post-recover-start", start), start); err == nil {
		t.Fatalf("Start on recovered reserved task = nil error, want fence rejection")
	}
}

// TestCanonicalReceiveTransferInterruptedCommitRecovers proves an interrupted
// ReceiveTransfer commit is recovered exactly once: the destination receives
// exactly one generation, no duplicate, and the received generation is not yet
// current.
func TestCanonicalReceiveTransferInterruptedCommitRecovers(t *testing.T) {
	cDest, _, root := newTestCanonical(t)

	agg := Aggregate{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        "t1",
		Generation:    1,
		Revision:      FirstRevision,
		Current:       false,
		Definition:    TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"},
		Phase:         PhaseQueued,
		Transfer: &TransferState{
			ReservationID:    "res-t1",
			SourceHome:       "source-home",
			SourceGeneration: 3,
		},
	}
	docData, _ := json.Marshal(taskDoc{HomeRevision: 1, Aggregate: agg})
	// Build the real receive operation so the planted receipt carries the true
	// intent digest; the post-recovery replay uses the same operation identity.
	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	op := mustOperation(t, "op-interrupted-receive", req)
	rec := receipt{OperationID: op.ID.Value(), Digest: op.Digest, TaskID: "t1", Generation: 1, Revision: 1, Phase: string(PhaseQueued)}
	recData, _ := json.Marshal(rec)
	plantInterruptedJournal(t, root, taskScope("t1"), "op-interrupted-receive", 0, 1, []home.ChangeItem{
		{Root: home.RootState, Key: taskGenKey("t1", 1), Data: docData},
		{Root: home.RootState, Key: receiptKey("op-interrupted-receive"), Data: recData},
	})

	c2 := reopenCanonical(t, root)
	// The received generation is present but not current.
	if _, err := c2.Get(mustTaskID(t, "t1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on received-not-current = %v, want ErrNotFound", err)
	}
	// Replaying the receive operation (same op id + digest) returns the
	// original committed outcome without creating a duplicate.
	out, err := c2.ReceiveTransfer(op, req)
	if err != nil {
		t.Fatalf("receive after recovery: %v", err)
	}
	if !out.Replayed {
		t.Fatalf("receive after recovery not replayed: %+v", out)
	}
}

// TestCanonicalActivateTransferInterruptedCommitRecovers proves an interrupted
// ActivateTransfer commit is recovered exactly once: the destination owns the
// activated generation, no duplicate, one truthful current owner.
func TestCanonicalActivateTransferInterruptedCommitRecovers(t *testing.T) {
	cDest, _, root := newTestCanonical(t)

	// First, receive the generation normally (committed).
	req := receiveTransferRequest(t, cDest, "t1", "res-t1", "source-home", 3)
	if _, err := cDest.ReceiveTransfer(mustOperation(t, "op-receive-1", req), req); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	// Simulate an interrupted ActivateTransfer: plant the activation change-set
	// (current.json + receipt) at scope revision 1 -> 2.
	next := Aggregate{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        "t1",
		Generation:    1,
		Revision:      2,
		Current:       true,
		Definition:    TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"},
		Phase:         PhaseQueued,
		Transfer: &TransferState{
			ReservationID:    "res-t1",
			SourceHome:       "source-home",
			SourceGeneration: 3,
		},
	}
	docData, _ := json.Marshal(taskDoc{HomeRevision: 2, Aggregate: next})
	rec := receipt{OperationID: "op-interrupted-activate", Digest: "intent", TaskID: "t1", Generation: 1, Revision: 2, Phase: string(PhaseQueued)}
	recData, _ := json.Marshal(rec)
	plantInterruptedJournal(t, root, taskScope("t1"), "op-interrupted-activate", 1, 2, []home.ChangeItem{
		{Root: home.RootState, Key: taskCurrentKey("t1"), Data: docData},
		{Root: home.RootState, Key: receiptKey("op-interrupted-activate"), Data: recData},
	})

	c2 := reopenCanonical(t, root)
	agg, err := c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("read recovered task: %v", err)
	}
	if !agg.Current || agg.Generation != 1 || agg.Revision != 2 {
		t.Fatalf("recovered aggregate = %+v, want current gen 1 rev 2", agg)
	}
}

// TestCanonicalCommitTransferInterruptedCommitRecovers proves an interrupted
// CommitTransfer commit is recovered exactly once: the source is superseded
// (non-current) with evidence recorded, no duplicate.
func TestCanonicalCommitTransferInterruptedCommitRecovers(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustReserveTransfer(t, c, "t1", preconditionOf(1, 1), "dest-home")

	evidence := TransferActivationInfo{
		ReservationID:         "res-t1",
		TaskID:                "t1",
		SourceHome:            c.HomeID().Value(),
		SourceGeneration:      1,
		DestinationHome:       "dest-home",
		DestinationGeneration: 1,
		ActivationOperationID: "op-activate-t1",
		ActivationDigest:      digestOf("activate:t1:res-t1"),
	}
	next := Aggregate{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        "t1",
		Generation:    1,
		Revision:      3,
		Current:       false,
		Definition:    TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"},
		Phase:         PhaseQueued,
		Transfer: &TransferState{
			ReservationID:   "res-t1",
			DestinationHome: "dest-home",
			FenceToken:      "fence-res-t1",
			ReservedAt:      1000,
			Transferred:     true,
			Activation:      &evidence,
		},
	}
	docData, _ := json.Marshal(taskDoc{HomeRevision: 3, Aggregate: next})
	rec := receipt{OperationID: "op-interrupted-commit", Digest: "intent", TaskID: "t1", Generation: 1, Revision: 3, Phase: string(PhaseQueued)}
	recData, _ := json.Marshal(rec)
	plantInterruptedJournal(t, root, taskScope("t1"), "op-interrupted-commit", 2, 3, []home.ChangeItem{
		{Root: home.RootState, Key: taskCurrentKey("t1"), Data: docData},
		{Root: home.RootState, Key: receiptKey("op-interrupted-commit"), Data: recData},
	})

	c2 := reopenCanonical(t, root)
	// The superseded source is not current truth after recovery: normal Get
	// fails closed, historical evidence remains available by generation.
	if _, err := c2.Get(mustTaskID(t, "t1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after recovered commit = %v, want ErrNotFound", err)
	}
	agg, err := c2.GetGeneration(mustTaskID(t, "t1"), Generation(1))
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	if agg.Current || agg.Transfer == nil || !agg.Transfer.Transferred {
		t.Fatalf("recovered aggregate not superseded: %+v", agg)
	}
	if agg.Transfer.Activation == nil || agg.Transfer.Activation.DestinationHome != "dest-home" {
		t.Fatalf("recovered activation evidence missing: %+v", agg.Transfer)
	}
}

// TestCanonicalRetireInterruptedCommitRecovers proves an interrupted Retire
// commit is recovered exactly once: the retired phase and preserved evidence
// commit once, no duplicate, and the active bindings are cleared.
func TestCanonicalRetireInterruptedCommitRecovers(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")
	wt := worktreeBinding()
	next := Aggregate{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        "t1",
		Generation:    1,
		Revision:      2,
		Current:       true,
		Definition:    TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"},
		Phase:         PhaseRetired,
		Retirement: &RetirementEvidence{
			OperationID: "op-interrupted-retire",
			Generation:  1,
			RetiredAt:   1000,
			Worktree:    &wt,
		},
	}
	docData, _ := json.Marshal(taskDoc{HomeRevision: 2, Aggregate: next})
	rec := receipt{OperationID: "op-interrupted-retire", Digest: "intent", TaskID: "t1", Generation: 1, Revision: 2, Phase: string(PhaseRetired)}
	recData, _ := json.Marshal(rec)
	plantInterruptedJournal(t, root, taskScope("t1"), "op-interrupted-retire", 1, 2, []home.ChangeItem{
		{Root: home.RootState, Key: taskCurrentKey("t1"), Data: docData},
		{Root: home.RootState, Key: receiptKey("op-interrupted-retire"), Data: recData},
	})

	c2 := reopenCanonical(t, root)
	agg, err := c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("read recovered task: %v", err)
	}
	if agg.Phase != PhaseRetired || agg.Worktree != nil {
		t.Fatalf("recovered retired aggregate = %+v", agg)
	}
	if agg.Retirement == nil || agg.Retirement.Worktree == nil || agg.Retirement.Worktree.LeaseID != "lease-wt" {
		t.Fatalf("recovered retirement evidence = %+v", agg.Retirement)
	}
}

// TestCanonicalDeliveryAuthorizationInterruptedCommitRecovers proves an
// interrupted delivery authorization commit is recovered mechanically exactly
// once: the advanced task document, the delivery ledger, and the operation
// receipt all replay from the write-ahead journal, and the recovered
// authorization is current and re-readable.
func TestCanonicalDeliveryAuthorizationInterruptedCommitRecovers(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")

	// The interrupted issuance: expectedRevision 3 -> 4 (post-issuance).
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	holds, err := c.listHolds()
	if err != nil {
		t.Fatal(err)
	}
	req := authorizeRequest(c, "t1", preconditionOf(1, 3))
	op := mustOperation(t, "op-interrupted-auth", req)

	next := agg.clone()
	next.Revision++
	auth := DeliveryAuthorization{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        next.TaskID,
		Generation:    next.Generation,
		Revision:      next.Revision,
		Phase:         next.Phase,
		Owner:         next.Definition.Owner,
		Kind:          req.Kind,
		Identity:      req.Identity,
		BindingDigest: deliveryBindingDigest(*next.Endpoint, *next.Worktree),
		HoldsDigest:   deliveryHoldsDigest(holds, next),
		Preconditions: uniqueDeliveryPreconditions(req.Preconditions),
		OperationID:   op.ID.Value(),
		Digest:        op.Digest,
		IssuedAt:      1000,
	}
	if err := validateDeliveryAuthorization(auth); err != nil {
		t.Fatal(err)
	}
	index := DeliveryIndex{SchemaVersion: TaskAuthoritySchema, TaskID: "t1", AuthorizationOpID: op.ID.Value()}
	if err := validateDeliveryIndex(index); err != nil {
		t.Fatal(err)
	}

	authData, _ := json.Marshal(auth)
	indexData, _ := json.Marshal(index)
	docData, _ := json.Marshal(taskDoc{HomeRevision: 4, Aggregate: next})
	receiptData, _ := json.Marshal(receipt{OperationID: op.ID.Value(), Digest: op.Digest, TaskID: "t1", Generation: 1, Revision: 4, Phase: string(PhaseWorking)})
	plantInterruptedJournal(t, root, taskScope("t1"), op.ID.Value(), 3, 4, []home.ChangeItem{
		{Root: home.RootState, Key: taskCurrentKey("t1"), Data: docData},
		{Root: home.RootState, Key: deliveryAuthorizationKey("t1", op.ID.Value()), Data: authData},
		{Root: home.RootState, Key: deliveryCurrentKey("t1"), Data: indexData},
		{Root: home.RootState, Key: receiptKey(op.ID.Value()), Data: receiptData},
	})

	// DeliveryCurrency is the first read after the interrupted journal is
	// planted, so the query itself must recover the task, index, and evidence.
	cur, err := c.DeliveryCurrency(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("read recovered currency: %v", err)
	}
	if !cur.Valid || cur.Revision != 4 || cur.Authorization == nil || cur.Authorization.OperationID != op.ID.Value() {
		t.Fatalf("recovered currency = %+v", cur)
	}

	recovered, err := c.DeliveryAuthorization(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("read recovered authorization: %v", err)
	}
	if recovered.OperationID != op.ID.Value() || recovered.Revision != 4 || recovered.Identity != deliveryIdentity() {
		t.Fatalf("recovered authorization = %+v", recovered)
	}

	// The recovered operation replays exactly once.
	replayed, err := c.AuthorizeDelivery(op, req)
	if err != nil {
		t.Fatalf("replay of recovered authorization: %v", err)
	}
	if !replayed.Replayed || replayed.Authorization.Revision != 4 {
		t.Fatalf("recovered replay = %+v", replayed)
	}
}

// TestCanonicalDeliveryOutcomeInterruptedCommitRecovers proves an interrupted
// outcome commit is recovered mechanically exactly once: the terminal outcome
// is present, the ledger stays bounded, and the operation replays.
func TestCanonicalDeliveryOutcomeInterruptedCommitRecovers(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")
	auth := mustAuthorize(t, c, "t1", 3, "op-auth-crash-outcome")

	outReq := CanonicalDeliveryOutcomeRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4),
		AuthorizationOperationID: auth.OperationID,
		Status:                   DeliveryOutcomeCompleted,
		Detail:                   "merge confirmed",
		MergedSHA:                "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	}
	op := mustOperation(t, "op-interrupted-outcome", outReq)

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	next := agg.clone()
	next.Revision++
	outcome := DeliveryOutcome{
		SchemaVersion:            TaskAuthoritySchema,
		TaskID:                   next.TaskID,
		Generation:               next.Generation,
		AuthorizationOperationID: auth.OperationID,
		OperationID:              op.ID.Value(),
		Digest:                   op.Digest,
		Status:                   outReq.Status,
		Detail:                   outReq.Detail,
		MergedSHA:                outReq.MergedSHA,
		CommittedAt:              1000,
	}
	if err := validateDeliveryOutcome(outcome); err != nil {
		t.Fatal(err)
	}
	index := DeliveryIndex{SchemaVersion: TaskAuthoritySchema, TaskID: "t1", AuthorizationOpID: auth.OperationID, OutcomeOpID: op.ID.Value(), Terminal: true}
	if err := validateDeliveryIndex(index); err != nil {
		t.Fatal(err)
	}

	outcomeData, _ := json.Marshal(outcome)
	indexData, _ := json.Marshal(index)
	docData, _ := json.Marshal(taskDoc{HomeRevision: 5, Aggregate: next})
	receiptData, _ := json.Marshal(receipt{OperationID: op.ID.Value(), Digest: op.Digest, TaskID: "t1", Generation: 1, Revision: 5, Phase: string(PhaseWorking)})
	plantInterruptedJournal(t, root, taskScope("t1"), op.ID.Value(), 4, 5, []home.ChangeItem{
		{Root: home.RootState, Key: taskCurrentKey("t1"), Data: docData},
		{Root: home.RootState, Key: deliveryOutcomeKey("t1", op.ID.Value()), Data: outcomeData},
		{Root: home.RootState, Key: deliveryCurrentKey("t1"), Data: indexData},
		{Root: home.RootState, Key: receiptKey(op.ID.Value()), Data: receiptData},
	})

	recovered, err := c.DeliveryOutcome(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("read recovered outcome: %v", err)
	}
	if recovered.OperationID != op.ID.Value() || recovered.Status != DeliveryOutcomeCompleted {
		t.Fatalf("recovered outcome = %+v", recovered)
	}
	replayed, err := c.CommitDeliveryOutcome(op, outReq)
	if err != nil {
		t.Fatalf("replay of recovered outcome: %v", err)
	}
	if !replayed.Replayed || replayed.Outcome.OperationID != op.ID.Value() {
		t.Fatalf("recovered outcome replay = %+v", replayed)
	}
	// A distinct incompatible outcome still conflicts after recovery.
	distinct := outReq
	distinct.Precondition = preconditionOf(1, 5)
	distinct.Status = DeliveryOutcomeRetryable
	if _, err := c.CommitDeliveryOutcome(mustOperation(t, "op-after-recovered", distinct), distinct); !errors.Is(err, ErrConflict) {
		t.Fatalf("distinct outcome after recovered terminal = %v, want ErrConflict", err)
	}
}
