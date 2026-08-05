package taskauthority

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// TestCanonicalLifecycleOperationReceiptsSurviveReopen proves the durable
// operation receipts of the lifecycle operations (start/block/complete/
// reopen) survive a home reopen and replay the original committed outcome
// through a fresh Canonical. This is the canonical durability obligation for
// the previously-existing lifecycle operations, not only the binding
// operations.
func TestCanonicalLifecycleOperationReceiptsSurviveReopen(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// start (queued -> working)
	start := startWithRev(c, "t1", 1)
	startOp := mustOperation(t, "op-life-start", start)
	if _, err := c.Start(startOp, start); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// complete (working -> done)
	complete := CanonicalCompleteRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2),
		To:           PhaseDone,
		Reason:       "done",
	}
	completeOp := mustOperation(t, "op-life-complete", complete)
	if _, err := c.Complete(completeOp, complete); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// reopen (done -> queued gen 2)
	reopen := CanonicalReopenRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 3),
		Reason:       "reopen",
	}
	reopenOp := mustOperation(t, "op-life-reopen", reopen)
	if _, err := c.Reopen(reopenOp, reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	// Reopen the home and replay each lifecycle operation's receipt.
	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}

	startOut, err := c2.Start(startOp, start)
	if err != nil {
		t.Fatalf("Start replay after reopen: %v", err)
	}
	if !startOut.Replayed || startOut.Phase != PhaseWorking {
		t.Fatalf("Start replay = %+v, want Replayed working", startOut)
	}

	completeOut, err := c2.Complete(completeOp, complete)
	if err != nil {
		t.Fatalf("Complete replay after reopen: %v", err)
	}
	if !completeOut.Replayed || completeOut.Phase != PhaseDone {
		t.Fatalf("Complete replay = %+v, want Replayed done", completeOut)
	}

	reopenOut, err := c2.Reopen(reopenOp, reopen)
	if err != nil {
		t.Fatalf("Reopen replay after reopen: %v", err)
	}
	if !reopenOut.Replayed || !reopenOut.Reopened || reopenOut.Generation != 2 {
		t.Fatalf("Reopen replay = %+v, want Replayed Reopened gen 2", reopenOut)
	}
}

// TestCanonicalLifecycleInterruptedCommitRecovers proves an interrupted
// home.Commit of a lifecycle operation (start) is recovered mechanically on
// the next home.Open: the phase transition commits exactly once, the revision
// advances exactly once, and no duplicate or contradictory Task state is left
// behind.
func TestCanonicalLifecycleInterruptedCommitRecovers(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Simulate an interrupted Start: plant a write-ahead journal record that
	// would commit the queued -> working transition and receipt at scope
	// revision 1, exactly as a real interrupted home.Commit would.
	next := Aggregate{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        "t1",
		Generation:    1,
		Revision:      2,
		Current:       true,
		Definition:    TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"},
		Phase:         PhaseWorking,
		PhaseDetail:   "start",
	}
	docData, err := json.Marshal(taskDoc{HomeRevision: 2, Aggregate: next})
	if err != nil {
		t.Fatal(err)
	}
	rec := receipt{OperationID: "op-interrupted-start", Digest: "intent", TaskID: "t1", Generation: 1, Revision: 2, Phase: string(PhaseWorking)}
	recData, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	scope := taskScope("t1")
	txnID := "op-interrupted-start"
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
		ExpectedRevision: 1, NewRevision: 2, Committed: false,
		Items: []home.ChangeItem{
			{Root: home.RootState, Key: taskCurrentKey("t1"), Data: docData},
			{Root: home.RootState, Key: receiptKey("op-interrupted-start"), Data: recData},
		},
	}
	data, err := json.Marshal(journalRec)
	if err != nil {
		t.Fatal(err)
	}
	journalDir := filepath.Join(root, home.JournalDirName)
	if err := os.WriteFile(filepath.Join(journalDir, scope+"."+txnID+".json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open after interruption: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("read recovered task: %v", err)
	}
	if agg.Phase != PhaseWorking || agg.Revision != 2 {
		t.Fatalf("recovered aggregate = phase %s rev %d, want working/2", agg.Phase, agg.Revision)
	}

	// The recovered scope revision is 2: a fresh mutation must use it.
	block := CanonicalBlockRequest{
		HomeID:       c2.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2),
		Detail:       "waiting",
		Reason:       "block",
	}
	if _, err := c2.Block(mustOperation(t, "op-block-after-recovery", block), block); err != nil {
		t.Fatalf("block after recovery: %v", err)
	}
}

// TestCanonicalLifecycleInterruptedCompleteFailsClosed proves a journal record
// that replays to a malformed lifecycle document fails closed on read rather
// than serving contradictory Task state.
func TestCanonicalLifecycleInterruptedCompleteFailsClosed(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Plant an interrupted journal record whose item writes malformed JSON to
	// the current document. Recovery replays it verbatim; the canonical read
	// must then fail closed instead of serving the corrupt document.
	scope := taskScope("t1")
	txnID := "op-corrupt-complete"
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
		ExpectedRevision: 1, NewRevision: 2, Committed: false,
		Items: []home.ChangeItem{{Root: home.RootState, Key: taskCurrentKey("t1"), Data: []byte("{not json")}},
	}
	data, err := json.Marshal(journalRec)
	if err != nil {
		t.Fatal(err)
	}
	journalDir := filepath.Join(root, home.JournalDirName)
	if err := os.WriteFile(filepath.Join(journalDir, scope+"."+txnID+".json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Get(mustTaskID(t, "t1")); err == nil {
		t.Fatalf("Get on malformed recovered state = nil error, want failure")
	}
	if _, err := c2.List(); err == nil {
		t.Fatalf("List on malformed recovered state = nil error, want failure")
	}
}

// TestCanonicalBlockOperationReusedConflict proves reuse of a lifecycle
// Operation ID with a different intent conflicts on the canonical path.
func TestCanonicalBlockOperationReusedConflict(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	start := startWithRev(c, "t1", 1)
	if _, err := c.Start(mustOperation(t, "op-start-for-block", start), start); err != nil {
		t.Fatalf("Start: %v", err)
	}

	block := CanonicalBlockRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2),
		Detail:       "waiting",
		Reason:       "block",
	}
	op := mustOperation(t, "op-shared-block", block)
	if _, err := c.Block(op, block); err != nil {
		t.Fatalf("Block: %v", err)
	}

	diff := block
	diff.Detail = "other"
	reused, err := domain.NewOperation(op.ID, diff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Block(reused, diff); !errorsIsOperationConflict(err) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

func errorsIsOperationConflict(err error) bool {
	return errors.Is(err, ErrOperationConflict)
}

// TestCanonicalBeginSpawnInterruptedCommitRecovers proves an interrupted
// BeginSpawn commit is recovered mechanically on the next home.Open: the
// launch intent commits exactly once, the revision advances exactly once, and
// the recovered launch fence is active (the worktree binding must match the
// recovered intent's reservation). Replaying the interrupted operation returns
// the original committed outcome without re-committing.
func TestCanonicalBeginSpawnInterruptedCommitRecovers(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Build the real BeginSpawn operation so the planted receipt carries the
	// true intent digest; the post-recovery replay uses the same identity.
	req := launchRequest(c, "t1", preconditionOf(1, 1))
	op := mustOperation(t, "op-interrupted-begin", req)

	next := Aggregate{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        "t1",
		Generation:    1,
		Revision:      2,
		Current:       true,
		Definition:    TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"},
		Phase:         PhaseQueued,
		Launch: &LaunchIntent{
			OperationID:           op.ID.Value(),
			SnapshotDigest:        req.SnapshotDigest,
			Backend:               req.Backend,
			Harness:               req.Harness,
			Model:                 req.Model,
			Effort:                req.Effort,
			Mode:                  req.Mode,
			Kind:                  req.Kind,
			Project:               req.Project,
			ParentTaskID:          req.ParentTaskID,
			LaunchID:              req.LaunchID,
			WindowLabel:           req.WindowLabel,
			WorktreeReservationID: req.WorktreeReservationID,
			WorktreeFenceToken:    req.WorktreeFenceToken,
			EndpointReservationID: req.EndpointReservationID,
			EndpointFenceToken:    req.EndpointFenceToken,
			PlannedAt:             1000,
		},
	}
	docData, err := json.Marshal(taskDoc{HomeRevision: 2, Aggregate: next})
	if err != nil {
		t.Fatal(err)
	}
	rec := receipt{OperationID: op.ID.Value(), Digest: op.Digest, TaskID: "t1", Generation: 1, Revision: 2, Phase: string(PhaseQueued)}
	recData, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	plantInterruptedJournal(t, root, taskScope("t1"), "op-interrupted-begin", 1, 2, []home.ChangeItem{
		{Root: home.RootState, Key: taskCurrentKey("t1"), Data: docData},
		{Root: home.RootState, Key: receiptKey("op-interrupted-begin"), Data: recData},
	})

	c2 := reopenCanonical(t, root)
	agg, err := c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("read recovered task: %v", err)
	}
	if agg.Revision != 2 || agg.Launch == nil || agg.Launch.LaunchID != "launch-t1" {
		t.Fatalf("recovered aggregate = revision %d launch %+v, want rev 2 with committed intent", agg.Revision, agg.Launch)
	}

	// The recovered launch fence is active: a worktree binding that does not
	// match the recovered reservation fails closed, and one that matches
	// succeeds.
	bwBad := CanonicalBindWorktreeRequest{
		HomeID:       c2.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2),
		Binding:      worktreeBinding(),
		Reason:       "bind worktree",
	}
	if _, err := c2.BindWorktree(mustOperation(t, "op-recovered-bad-wt", bwBad), bwBad); !errors.Is(err, ErrConflict) {
		t.Fatalf("worktree binding outside recovered fence = %v, want ErrConflict", err)
	}
	bwGood := CanonicalBindWorktreeRequest{
		HomeID:       c2.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2),
		Binding:      launchWorktreeBinding(req),
		Reason:       "bind worktree",
	}
	if _, err := c2.BindWorktree(mustOperation(t, "op-recovered-good-wt", bwGood), bwGood); err != nil {
		t.Fatalf("worktree binding under recovered fence: %v", err)
	}

	// Replaying the interrupted BeginSpawn returns the original outcome.
	out, err := c2.BeginSpawn(op, req)
	if err != nil {
		t.Fatalf("BeginSpawn replay after recovery: %v", err)
	}
	if !out.Replayed || out.Revision != 2 {
		t.Fatalf("BeginSpawn replay = %+v, want Replayed rev 2", out)
	}
}

// TestCanonicalLaunchOperationReceiptsSurviveReopen proves the durable
// operation receipts of the launch operations (begin spawn / attach endpoint /
// record launch / bind endpoint) survive a home reopen and replay the original
// committed outcome through a fresh Canonical.
func TestCanonicalLaunchOperationReceiptsSurviveReopen(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := launchRequest(c, "t1", preconditionOf(1, 1))
	beginOp := mustOperation(t, "op-durable-begin", req)
	if _, err := c.BeginSpawn(beginOp, req); err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}
	rev := uint64(2)

	bw := CanonicalBindWorktreeRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev),
		Binding:      launchWorktreeBinding(req),
		Reason:       "bind worktree",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-durable-wt", bw), bw); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	rev++

	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	attachOp := mustOperation(t, "op-durable-attach", attach)
	if _, err := c.AttachEndpoint(attachOp, attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	rev++

	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev), req)
	recordOp := mustOperation(t, "op-durable-record", record)
	if _, err := c.RecordLaunch(recordOp, record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	rev++

	be := CanonicalBindEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev),
		Binding:      launchEndpointBinding(req, "handle-1"),
		Reason:       "spawn",
	}
	beOp := mustOperation(t, "op-durable-bind", be)
	if _, err := c.BindEndpoint(beOp, be); err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}

	// Reopen the home and replay each launch operation's receipt.
	c2 := reopenCanonical(t, root)

	beginOut, err := c2.BeginSpawn(beginOp, req)
	if err != nil {
		t.Fatalf("BeginSpawn replay after reopen: %v", err)
	}
	if !beginOut.Replayed || beginOut.Phase != PhaseQueued || beginOut.Revision != 2 {
		t.Fatalf("BeginSpawn replay = %+v, want Replayed queued rev 2", beginOut)
	}

	attachOut, err := c2.AttachEndpoint(attachOp, attach)
	if err != nil {
		t.Fatalf("AttachEndpoint replay after reopen: %v", err)
	}
	if !attachOut.Replayed || attachOut.Phase != PhaseQueued || attachOut.Revision != 4 {
		t.Fatalf("AttachEndpoint replay = %+v, want Replayed queued rev 4", attachOut)
	}

	recordOut, err := c2.RecordLaunch(recordOp, record)
	if err != nil {
		t.Fatalf("RecordLaunch replay after reopen: %v", err)
	}
	if !recordOut.Replayed || recordOut.Phase != PhaseQueued || recordOut.Revision != 5 {
		t.Fatalf("RecordLaunch replay = %+v, want Replayed queued rev 5", recordOut)
	}

	beOut, err := c2.BindEndpoint(beOp, be)
	if err != nil {
		t.Fatalf("BindEndpoint replay after reopen: %v", err)
	}
	if !beOut.Replayed || beOut.Phase != PhaseWorking || beOut.Revision != 6 {
		t.Fatalf("BindEndpoint replay = %+v, want Replayed working rev 6", beOut)
	}

	// The recovered launch state is complete and current.
	agg, err := c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseWorking || agg.Launch == nil || agg.AcquiredEndpoint == nil || agg.LaunchEvidence == nil {
		t.Fatalf("recovered launch state = %+v", agg)
	}
}

// TestCanonicalDeliveryAuthorizationAndOutcomeSurviveReopen proves the
// committed delivery authorization, the committed outcome, and the operation
// receipts survive a real Home reopen: a fresh Canonical over the reopened
// home re-reads the same records and replays the original committed outcomes
// idempotently.
func TestCanonicalDeliveryAuthorizationAndOutcomeSurviveReopen(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")

	authReq := authorizeRequest(c, "t1", preconditionOf(1, 3))
	authOp := mustOperation(t, "op-auth-persist", authReq)
	authRes, err := c.AuthorizeDelivery(authOp, authReq)
	if err != nil {
		t.Fatalf("AuthorizeDelivery: %v", err)
	}

	outReq := CanonicalDeliveryOutcomeRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4),
		AuthorizationOperationID: authRes.Authorization.OperationID,
		Status:                   DeliveryOutcomeCompleted,
		Detail:                   "merged and verified",
		MergedSHA:                "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	}
	outOp := mustOperation(t, "op-outcome-persist", outReq)
	outRes, err := c.CommitDeliveryOutcome(outOp, outReq)
	if err != nil {
		t.Fatalf("CommitDeliveryOutcome: %v", err)
	}

	// Reopen the home and re-read canonical state through a fresh Canonical.
	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}

	auth, err := c2.DeliveryAuthorization(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("DeliveryAuthorization after reopen: %v", err)
	}
	if auth.OperationID != authRes.Authorization.OperationID || auth.Revision != 4 || auth.Identity != deliveryIdentity() {
		t.Fatalf("reopened authorization = %+v, want the committed record", auth)
	}
	if auth.BindingDigest != authRes.Authorization.BindingDigest || auth.HoldsDigest != authRes.Authorization.HoldsDigest {
		t.Fatalf("reopened digests changed: %+v", auth)
	}

	out, err := c2.DeliveryOutcome(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatalf("DeliveryOutcome after reopen: %v", err)
	}
	if out.OperationID != outRes.Outcome.OperationID || out.Status != DeliveryOutcomeCompleted || out.MergedSHA != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Fatalf("reopened outcome = %+v", out)
	}

	// The operation receipts replay the original outcomes after reopen, and
	// the currency read reports the consumed authorization truthfully: the
	// committed outcome advanced the revision, so the authorization is no
	// longer current (revision-mismatch).
	replayed, err := c2.AuthorizeDelivery(authOp, authReq)
	if err != nil {
		t.Fatalf("authorization replay after reopen: %v", err)
	}
	if !replayed.Replayed || replayed.Authorization.OperationID != authRes.Authorization.OperationID {
		t.Fatalf("authorization replay after reopen = %+v", replayed)
	}

	outReplayed, err := c2.CommitDeliveryOutcome(outOp, outReq)
	if err != nil {
		t.Fatalf("outcome replay after reopen: %v", err)
	}
	if !outReplayed.Replayed || outReplayed.Outcome.OperationID != outRes.Outcome.OperationID {
		t.Fatalf("outcome replay after reopen = %+v", outReplayed)
	}

	cur2, err := c2.DeliveryCurrency(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if cur2.Valid || !hasCurrencyReason(cur2, DeliveryCurrencyRevision) {
		t.Fatalf("currency after outcome = %+v, want revision-mismatch", cur2)
	}
}

// TestCanonicalDeliveryRevocationEvidenceSurvivesReopen proves the revocation
// evidence bound to a prior authorization survives a Home reopen and the
// prior record stays identified by its operation identity.
func TestCanonicalDeliveryRevocationEvidenceSurvivesReopen(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")

	auth1 := mustAuthorize(t, c, "t1", 3, "op-auth-persist-revoke")
	revokeReq := CanonicalRevokeDeliveryRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4),
		AuthorizationOperationID: auth1.OperationID,
		Reason:                   "abandoned before execution",
	}
	revokeOp := mustOperation(t, "op-revoke-persist", revokeReq)
	if _, err := c.RevokeDeliveryAuthorization(revokeOp, revokeReq); err != nil {
		t.Fatalf("RevokeDeliveryAuthorization: %v", err)
	}

	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := c2.DeliveryAuthorizationByOperation(mustTaskID(t, "t1"), auth1.OperationID)
	if err != nil {
		t.Fatalf("identified authorization after reopen: %v", err)
	}
	if prior.Revoked == nil || prior.Revoked.OperationID != "op-revoke-persist" || prior.Revoked.Reason != "abandoned before execution" {
		t.Fatalf("reopened revocation evidence = %+v", prior.Revoked)
	}
	replayed, err := c2.RevokeDeliveryAuthorization(revokeOp, revokeReq)
	if err != nil {
		t.Fatalf("revoke replay after reopen: %v", err)
	}
	if !replayed.Replayed || replayed.Authorization.Revoked == nil {
		t.Fatalf("revoke replay after reopen = %+v", replayed)
	}
}

// TestCanonicalDeliveryCurrencyReadSurvivesReopen proves the narrow currency
// read is a pure read across a Home reopen: no receipt is created and the
// recomputed facts are identical.
func TestCanonicalDeliveryCurrencyReadSurvivesReopen(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")
	mustAuthorize(t, c, "t1", 3, "op-auth-currency-persist")

	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := c2.DeliveryCurrency(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if !cur.Valid || cur.Revision != 4 || cur.HoldsDigest == "" || cur.BindingDigest == "" {
		t.Fatalf("reopened currency = %+v", cur)
	}
	if cur.Authorization == nil || cur.Authorization.Revision != 4 {
		t.Fatalf("reopened currency authorization = %+v", cur.Authorization)
	}

	// Currency read creates no receipt and never mutates the ledger.
	if _, err := c2.DeliveryCurrency(mustTaskID(t, "t1")); err != nil {
		t.Fatal(err)
	}
	rec, exists, err := c2.readDeliveryRecord("t1")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || len(rec.Authorizations) != 1 || len(rec.Outcomes) != 0 {
		t.Fatalf("currency read mutated the delivery record: %+v", rec)
	}
}

// TestCanonicalDeliveryWrongHomeFailsClosed proves delivery operations bind to
// the canonical home identity and reject requests targeting another home.
func TestCanonicalDeliveryWrongHomeFailsClosed(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDeliveryTask(t, c, "t1")
	otherHome, err := domain.NewHomeID("other-home")
	if err != nil {
		t.Fatal(err)
	}
	req := authorizeRequest(c, "t1", preconditionOf(1, 3))
	req.HomeID = otherHome
	if _, err := c.AuthorizeDelivery(mustOperation(t, "op-auth-wrong-home", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("authorize with wrong home = %v, want ErrConflict", err)
	}
}
