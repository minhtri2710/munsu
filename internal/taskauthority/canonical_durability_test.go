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