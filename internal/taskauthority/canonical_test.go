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

// newTestCanonical builds a canonical Task Authority over a fresh real
// temporary home. There is no in-memory fake on the canonical path.
func newTestCanonical(t *testing.T) (*Canonical, *home.Home, string) {
	t.Helper()
	root := t.TempDir()
	h, err := home.Init(root)
	if err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	c, err := NewCanonical(h)
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	return c, h, root
}

func mustOperation(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID(id)
	if err != nil {
		t.Fatalf("NewOperationID(%s): %v", id, err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		t.Fatalf("NewOperation(%s): %v", id, err)
	}
	return op
}

func mustTaskID(t *testing.T, value string) domain.TaskID {
	t.Helper()
	id, err := domain.NewTaskID(value)
	if err != nil {
		t.Fatalf("NewTaskID(%s): %v", value, err)
	}
	return id
}

func createRequest(c *Canonical, taskID string) CanonicalCreateRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalCreateRequest{
		HomeID:      c.HomeID(),
		TaskID:      id,
		Owner:       "owner",
		Description: "work",
		Kind:        "ship",
		Reason:      "create",
	}
}

func mustCreate(t *testing.T, c *Canonical, taskID string) Outcome {
	t.Helper()
	req := createRequest(c, taskID)
	op := mustOperation(t, "op-create-"+taskID, req)
	out, err := c.Create(op, req)
	if err != nil {
		t.Fatalf("Create(%s): %v", taskID, err)
	}
	return out
}

func preconditionOf(gen, rev uint64) domain.Precondition {
	return domain.Of(gen, rev)
}

func TestCanonicalCreateGetList(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	out := mustCreate(t, c, "t1")
	if out.TaskID.Value() != "t1" || out.Generation != 1 || out.Revision != FirstRevision || out.Phase != PhaseQueued {
		t.Fatalf("create outcome = %+v", out)
	}
	mustCreate(t, c, "t2")

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.TaskID != "t1" || agg.Generation != 1 || agg.Revision != FirstRevision || agg.Phase != PhaseQueued {
		t.Fatalf("Get = %+v", agg)
	}
	if agg.Definition.Owner != "owner" || agg.Definition.Kind != "ship" {
		t.Fatalf("definition = %+v", agg.Definition)
	}

	list, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].TaskID != "t1" || list[1].TaskID != "t2" {
		t.Fatalf("List = %+v", list)
	}

	if _, err := c.Get(mustTaskID(t, "missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) = %v, want ErrNotFound", err)
	}
}

func TestCanonicalCreateDuplicateConflicts(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := createRequest(c, "t1")
	op := mustOperation(t, "op-create-dup", req)
	if _, err := c.Create(op, req); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate create = %v, want ErrConflict", err)
	}
}

func TestCanonicalLifecycleTransitions(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// queued -> working
	start := CanonicalStartRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 1),
		Reason:       "start",
	}
	if _, err := c.Start(mustOperation(t, "op-start-1", start), start); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// working -> blocked
	block := CanonicalBlockRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2),
		Detail:       "waiting",
		Reason:       "block",
	}
	if _, err := c.Block(mustOperation(t, "op-block-1", block), block); err != nil {
		t.Fatalf("Block: %v", err)
	}

	// blocked -> queued
	unblock := CanonicalUnblockRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 3),
		Reason:       "unblock",
	}
	if _, err := c.Unblock(mustOperation(t, "op-unblock-1", unblock), unblock); err != nil {
		t.Fatalf("Unblock: %v", err)
	}

	// queued -> working -> done
	if _, err := c.Start(mustOperation(t, "op-start-2", startWithRev(c, "t1", 4)), startWithRev(c, "t1", 4)); err != nil {
		t.Fatalf("Start 2: %v", err)
	}
	complete := CanonicalCompleteRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 5),
		To:           PhaseDone,
		Reason:       "done",
	}
	res, err := c.Complete(mustOperation(t, "op-complete-1", complete), complete)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Phase != PhaseDone || res.Revision != 6 {
		t.Fatalf("Complete outcome = %+v", res)
	}

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseDone || agg.Revision != 6 {
		t.Fatalf("final aggregate = %+v", agg)
	}
}

func startWithRev(c *Canonical, taskID string, rev uint64) CanonicalStartRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalStartRequest{
		HomeID:       c.HomeID(),
		TaskID:       id,
		Precondition: preconditionOf(1, rev),
		Reason:       "start",
	}
}

func TestCanonicalLifecyclePreconditions(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Complete requires non-terminal: creating is queued, so complete works. Test
	// the wrong-phase precondition instead: Unblock requires blocked.
	unblock := CanonicalUnblockRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 1),
		Reason:       "nope",
	}
	if _, err := c.Unblock(mustOperation(t, "op-unblock-wrong", unblock), unblock); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("unblock queued = %v, want ErrPrecondition", err)
	}
}

func TestCanonicalIdempotentReplay(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	req := createRequest(c, "t1")
	op := mustOperation(t, "op-create-replay", req)
	first, err := c.Create(op, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Create(op, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
	if second.TaskID != first.TaskID || second.Revision != first.Revision || second.Phase != first.Phase {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}
}

func TestCanonicalOperationIDReusedWithDifferentIntent(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	req := createRequest(c, "t1")
	op := mustOperation(t, "op-shared", req)
	if _, err := c.Create(op, req); err != nil {
		t.Fatal(err)
	}

	// Reuse the same Operation ID with a different intent (a start).
	start := startWithRev(c, "t1", 1)
	reused := domain.Operation{ID: op.ID, Digest: mustDigest(t, start)}
	reused, err := domain.NewOperation(op.ID, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(reused, start); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

func mustDigest(t *testing.T, intent domain.Intent) string {
	t.Helper()
	d, err := domain.Digest(intent)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCanonicalStalePreconditionConflict(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Expect generation 1 revision 9, but current is revision 1.
	start := CanonicalStartRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 9),
		Reason:       "start",
	}
	_, err := c.Start(mustOperation(t, "op-stale", start), start)
	if !errors.Is(err, domain.ErrStalePrecondition) {
		t.Fatalf("stale start = %v, want domain.ErrStalePrecondition", err)
	}
	var conflict *domain.Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error is not a typed domain.Conflict: %v", err)
	}
	if conflict.ExpectedGeneration != 1 || conflict.ExpectedRevision != 9 {
		t.Fatalf("conflict = %+v", conflict)
	}
	if conflict.ActualGeneration != 1 || conflict.ActualRevision != 1 {
		t.Fatalf("conflict actual = gen %d rev %d, want 1/1", conflict.ActualGeneration, conflict.ActualRevision)
	}
}

func TestCanonicalStartBlockedByDispatchHold(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	hold := CanonicalAddHoldRequest{
		HomeID:  c.HomeID(),
		HoldID:  "hold-1",
		Scope:   DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionStart},
		Reason:  "freeze",
	}
	if _, err := c.AddHold(mustOperation(t, "op-hold-1", hold), hold); err != nil {
		t.Fatalf("AddHold: %v", err)
	}

	start := startWithRev(c, "t1", 1)
	if _, err := c.Start(mustOperation(t, "op-start-held", start), start); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("start held = %v, want ErrDispatchHeld", err)
	}

	// Release the hold: start now succeeds.
	release := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-1", Reason: "resume"}
	if _, err := c.ReleaseHold(mustOperation(t, "op-release-1", release), release); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if _, err := c.Start(mustOperation(t, "op-start-after", start), start); err != nil {
		t.Fatalf("start after release: %v", err)
	}
}

func TestCanonicalReadiness(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	// Missing task reports not-found, not an error.
	r, err := c.Readiness(mustTaskID(t, "missing"))
	if err != nil {
		t.Fatalf("Readiness(missing): %v", err)
	}
	if len(r.BlockingReasons) != 1 || r.BlockingReasons[0] != ReadinessNotFound {
		t.Fatalf("missing readiness = %+v", r)
	}

	mustCreate(t, c, "t1")
	r, err = c.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Ready || len(r.BlockingReasons) != 0 {
		t.Fatalf("queued readiness = %+v", r)
	}

	// A matching dispatch hold blocks readiness even for a queued task.
	hold := CanonicalAddHoldRequest{
		HomeID:  c.HomeID(),
		HoldID:  "hold-2",
		Scope:   DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionStart},
		Reason:  "freeze",
	}
	if _, err := c.AddHold(mustOperation(t, "op-hold-2", hold), hold); err != nil {
		t.Fatalf("AddHold: %v", err)
	}
	r, err = c.Readiness(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready || len(r.BlockingReasons) != 1 || r.BlockingReasons[0] != ReadinessDispatchHold {
		t.Fatalf("held readiness = %+v", r)
	}
}

func TestCanonicalReopen(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Complete to a terminal phase.
	complete := CanonicalCompleteRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 1),
		To:           PhaseDone,
		Reason:       "done",
	}
	if _, err := c.Complete(mustOperation(t, "op-complete-r", complete), complete); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	reopen := CanonicalReopenRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2),
		Reason:       "reopen",
	}
	out, err := c.Reopen(mustOperation(t, "op-reopen-1", reopen), reopen)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if !out.Reopened || out.Generation != 2 || out.Revision != FirstRevision || out.Phase != PhaseQueued {
		t.Fatalf("reopen outcome = %+v", out)
	}

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Generation != 2 || agg.Revision != FirstRevision || agg.Phase != PhaseQueued || !agg.Current {
		t.Fatalf("reopened aggregate = %+v", agg)
	}

	// Reopen replays.
	replayed, err := c.Reopen(mustOperation(t, "op-reopen-1", reopen), reopen)
	if err != nil {
		t.Fatalf("reopen replay: %v", err)
	}
	if !replayed.Replayed || replayed.Generation != 2 {
		t.Fatalf("reopen replay = %+v", replayed)
	}
}

func TestCanonicalMalformedCurrentStateFailsClosed(t *testing.T) {
	c, h, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Corrupt the current document on disk.
	path, err := h.Path(home.RootState, taskCurrentKey("t1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	// A fresh canonical over the same home (no cached state) must fail closed.
	c2, err := NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Get(mustTaskID(t, "t1")); err == nil {
		t.Fatalf("Get on malformed state = nil error, want failure")
	}
	if _, err := c2.List(); err == nil {
		t.Fatalf("List on malformed state = nil error, want failure")
	}
	if _, err := c2.Readiness(mustTaskID(t, "t1")); err == nil {
		t.Fatalf("Readiness on malformed state = nil error, want failure")
	}
}

func TestCanonicalRereadAfterHomeReopen(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")
	start := startWithRev(c, "t1", 1)
	if _, err := c.Start(mustOperation(t, "op-start-reread", start), start); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Reopen the home (mechanical recovery runs) and re-read canonical state
	// through a fresh Canonical.
	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseWorking || agg.Revision != 2 {
		t.Fatalf("reread aggregate = %+v", agg)
	}
}

func TestCanonicalOperationReceiptSurvivesReopen(t *testing.T) {
	c, _, root := newTestCanonical(t)
	req := createRequest(c, "t1")
	op := mustOperation(t, "op-create-persist", req)
	if _, err := c.Create(op, req); err != nil {
		t.Fatal(err)
	}

	h2, err := home.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c2.Create(op, req)
	if err != nil {
		t.Fatalf("replay after reopen: %v", err)
	}
	if !out.Replayed {
		t.Fatalf("replay after reopen not marked replayed: %+v", out)
	}
}

func TestCanonicalWrongHomeFailsClosed(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	otherHome, err := domain.NewHomeID("other-home")
	if err != nil {
		t.Fatal(err)
	}
	req := CanonicalCreateRequest{
		HomeID:      otherHome,
		TaskID:      mustTaskID(t, "t1"),
		Owner:       "owner",
		Description: "work",
		Kind:        "ship",
		Reason:      "create",
	}
	if _, err := c.Create(mustOperation(t, "op-wrong-home", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong home create = %v, want ErrConflict", err)
	}
}

func TestCanonicalHoldsListAndRelease(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	hold := CanonicalAddHoldRequest{
		HomeID:  c.HomeID(),
		HoldID:  "hold-a",
		Scope:   DispatchHoldScope{ProjectIDs: []string{"proj"}},
		Actions: []DispatchAction{DispatchActionStart, DispatchActionSpawn},
		Reason:  "freeze proj",
	}
	if _, err := c.AddHold(mustOperation(t, "op-hold-a", hold), hold); err != nil {
		t.Fatalf("AddHold: %v", err)
	}
	holdB := CanonicalAddHoldRequest{HomeID: c.HomeID(), HoldID: "hold-b", Actions: []DispatchAction{DispatchActionStart}, Reason: "freeze all"}
	if _, err := c.AddHold(mustOperation(t, "op-hold-b", holdB), holdB); err != nil {
		t.Fatalf("AddHold b: %v", err)
	}

	list, err := c.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "hold-a" || list[1].ID != "hold-b" {
		t.Fatalf("ListHolds = %+v", list)
	}

	// Identical re-add is a no-op.
	again, err := c.AddHold(mustOperation(t, "op-hold-a-again", hold), hold)
	if err != nil {
		t.Fatalf("re-add identical hold: %v", err)
	}
	if again.HoldID != "hold-a" {
		t.Fatalf("re-add outcome = %+v", again)
	}

	// Different definition under the same ID conflicts.
	diff := hold
	diff.Reason = "different"
	if _, err := c.AddHold(mustOperation(t, "op-hold-a-diff", diff), diff); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-add different definition = %v, want ErrConflict", err)
	}

	// Release and re-release no-op.
	release := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-a", Reason: "done"}
	if _, err := c.ReleaseHold(mustOperation(t, "op-release-a", release), release); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if _, err := c.ReleaseHold(mustOperation(t, "op-release-a2", release), release); err != nil {
		t.Fatalf("ReleaseHold again: %v", err)
	}
	if _, err := c.ReleaseHold(mustOperation(t, "op-release-missing", CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "nope", Reason: "x"}), CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "nope", Reason: "x"}); !errors.Is(err, ErrHoldNotFound) {
		t.Fatalf("release missing = %v, want ErrHoldNotFound", err)
	}
}

func TestCanonicalMultipleTasksIndependentRevision(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustCreate(t, c, "t2")

	start1 := startWithRev(c, "t1", 1)
	if _, err := c.Start(mustOperation(t, "op-start-t1", start1), start1); err != nil {
		t.Fatalf("Start t1: %v", err)
	}
	// t2 still at revision 1 in generation 1.
	start2 := startWithRev(c, "t2", 1)
	if _, err := c.Start(mustOperation(t, "op-start-t2", start2), start2); err != nil {
		t.Fatalf("Start t2: %v", err)
	}
	if _, err := c.Get(mustTaskID(t, "t1")); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalCrashRecovery(t *testing.T) {
	root := t.TempDir()
	h, err := home.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, c, "t1")

	// Simulate an interrupted home.Commit of a fresh task t2 create by
	// planting a write-ahead journal record: home.Open's mechanical recovery
	// replays it. The journal is the home internal shape, planted here
	// deliberately; the doc encodes the new scope revision (0 -> 1) exactly
	// as a real interrupted commit would.
	agg, err := NewAggregate("t2", "owner", "work", "ship", "", "")
	if err != nil {
		t.Fatal(err)
	}
	docData, err := json.Marshal(taskDoc{HomeRevision: 1, Aggregate: agg})
	if err != nil {
		t.Fatal(err)
	}
	scope := taskScope("t2")
	txnID := "op-interrupted-create"
	rec := struct {
		TxnID            string            `json:"txn_id"`
		Scope            string            `json:"scope"`
		FenceToken       uint64            `json:"fence_token"`
		ExpectedRevision uint64            `json:"expected_revision"`
		NewRevision      uint64            `json:"new_revision"`
		Items            []home.ChangeItem `json:"items"`
		Committed        bool              `json:"committed"`
	}{
		TxnID: txnID, Scope: scope, FenceToken: 1,
		ExpectedRevision: 0, NewRevision: 1, Committed: false,
		Items: []home.ChangeItem{{Root: home.RootState, Key: taskCurrentKey("t2"), Data: docData}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	journalDir := filepath.Join(root, home.JournalDirName)
	if err := os.WriteFile(filepath.Join(journalDir, scope+"."+txnID+".json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	// home.Open recovers the interrupted transaction mechanically, making the
	// fresh task visible and advancing the scope revision to 1.
	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open after interruption: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	agg2, err := c2.Get(mustTaskID(t, "t2"))
	if err != nil {
		t.Fatalf("read recovered task: %v", err)
	}
	if agg2.TaskID != "t2" || agg2.Generation != 1 || agg2.Revision != FirstRevision {
		t.Fatalf("recovered aggregate = %+v", agg2)
	}

	// A fresh mutation on the recovered task succeeds with the recovered
	// expected revision (the recovered scope revision is 1).
	start := startWithRev(c2, "t2", 1)
	if _, err := c2.Start(mustOperation(t, "op-start-recovered", start), start); err != nil {
		t.Fatalf("start after recovery: %v", err)
	}
}
