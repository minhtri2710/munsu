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

func worktreeBinding() WorktreeBinding {
	return WorktreeBinding{
		RepositoryIdentity: "repo",
		Path:               "/work/area",
		GitDir:             "/work/area/.git",
		CommonDir:          "/work/shared.git",
		Head:               "abc123",
		LeaseID:            "lease-wt",
		FenceToken:         "fence-wt",
		BoundAtUnix:        1000,
	}
}

func endpointBinding() EndpointBinding {
	return EndpointBinding{
		Backend:     "claude",
		Handle:      "handle-1",
		LeaseID:     "lease-ep",
		FenceToken:  "fence-ep",
		SessionOwner: "owner",
		WorkspaceID: "ws",
		TabID:       "tab",
		BoundAtUnix: 2000,
	}
}

func bindWorktreeRequest(c *Canonical, taskID string, prec domain.Precondition) CanonicalBindWorktreeRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalBindWorktreeRequest{
		HomeID:       c.HomeID(),
		TaskID:       id,
		Precondition: prec,
		Binding:      worktreeBinding(),
		Reason:       "bind worktree",
	}
}

func bindEndpointRequest(c *Canonical, taskID string, prec domain.Precondition) CanonicalBindEndpointRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalBindEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       id,
		Precondition: prec,
		Binding:      endpointBinding(),
		Reason:       "spawn",
	}
}

// mustBindWorktree creates a task then binds a worktree, returning the outcome
// of the bind and the resulting aggregate.
func mustBindWorktree(t *testing.T, c *Canonical, taskID string) (Outcome, Aggregate) {
	t.Helper()
	mustCreate(t, c, taskID)
	out, err := c.BindWorktree(mustOperation(t, "op-bind-wt-"+taskID, bindWorktreeRequest(c, taskID, preconditionOf(1, 1))), bindWorktreeRequest(c, taskID, preconditionOf(1, 1)))
	if err != nil {
		t.Fatalf("BindWorktree(%s): %v", taskID, err)
	}
	agg, err := c.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	return out, agg
}

func TestCanonicalBindWorktree(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	out, agg := mustBindWorktree(t, c, "t1")

	if out.Revision != 2 || out.Phase != PhaseQueued {
		t.Fatalf("bind worktree outcome = %+v", out)
	}
	if agg.Worktree == nil || agg.Worktree.RepositoryIdentity != "repo" || agg.Worktree.LeaseID != "lease-wt" {
		t.Fatalf("aggregate worktree = %+v", agg.Worktree)
	}
	if agg.Revision != 2 {
		t.Fatalf("aggregate revision = %d, want 2", agg.Revision)
	}
}

func TestCanonicalBindWorktreeAlreadyBoundConflicts(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	_, agg := mustBindWorktree(t, c, "t1")

	req := bindWorktreeRequest(c, "t1", preconditionOf(1, uint64(agg.Revision)))
	if _, err := c.BindWorktree(mustOperation(t, "op-bind-wt-dup", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-bind worktree = %v, want ErrConflict", err)
	}
}

func TestCanonicalBindWorktreeStalePrecondition(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Expect generation 1 revision 9, but current is revision 1.
	req := bindWorktreeRequest(c, "t1", preconditionOf(1, 9))
	if _, err := c.BindWorktree(mustOperation(t, "op-bind-wt-stale", req), req); !errors.Is(err, domain.ErrStalePrecondition) {
		t.Fatalf("stale bind worktree = %v, want domain.ErrStalePrecondition", err)
	}
}

func TestCanonicalBindWorktreeIdempotentReplay(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := bindWorktreeRequest(c, "t1", preconditionOf(1, 1))
	op := mustOperation(t, "op-bind-wt-replay", req)
	first, err := c.BindWorktree(op, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.BindWorktree(op, req)
	if err != nil {
		t.Fatalf("replay bind worktree: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
	if second.Revision != first.Revision {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}
}

func TestCanonicalBindWorktreeOperationReusedConflict(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := bindWorktreeRequest(c, "t1", preconditionOf(1, 1))
	op := mustOperation(t, "op-shared-bind", req)
	if _, err := c.BindWorktree(op, req); err != nil {
		t.Fatal(err)
	}

	// Reuse the same Operation ID with a different intent (a different worktree).
	diff := bindWorktreeRequest(c, "t1", preconditionOf(1, 1))
	diff.Binding.Path = "/other/area"
	reused, err := domain.NewOperation(op.ID, diff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.BindWorktree(reused, diff); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

func TestCanonicalBindEndpoint(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustBindWorktree(t, c, "t1")

	req := bindEndpointRequest(c, "t1", preconditionOf(1, 2))
	out, err := c.BindEndpoint(mustOperation(t, "op-bind-ep", req), req)
	if err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}
	if out.Phase != PhaseWorking || out.Revision != 3 {
		t.Fatalf("bind endpoint outcome = %+v", out)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Endpoint == nil || agg.Endpoint.Backend != "claude" || agg.Endpoint.LeaseID != "lease-ep" {
		t.Fatalf("aggregate endpoint = %+v", agg.Endpoint)
	}
	if agg.Phase != PhaseWorking || agg.Revision != 3 {
		t.Fatalf("aggregate = phase %s rev %d, want working/3", agg.Phase, agg.Revision)
	}
}

func TestCanonicalBindEndpointRequiresWorktree(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := bindEndpointRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BindEndpoint(mustOperation(t, "op-bind-ep-no-wt", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("bind endpoint without worktree = %v, want ErrConflict", err)
	}
}

func TestCanonicalBindEndpointRequiresQueued(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustBindWorktree(t, c, "t1")

	// Move to working first via Start (no endpoint), then bind endpoint fails.
	start := startWithRev(c, "t1", 2)
	if _, err := c.Start(mustOperation(t, "op-start-before-bind", start), start); err != nil {
		t.Fatalf("Start: %v", err)
	}
	req := bindEndpointRequest(c, "t1", preconditionOf(1, 3))
	if _, err := c.BindEndpoint(mustOperation(t, "op-bind-ep-working", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("bind endpoint on working = %v, want ErrConflict", err)
	}
}

func TestCanonicalBindEndpointAlreadyBound(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustBindWorktree(t, c, "t1")
	req := bindEndpointRequest(c, "t1", preconditionOf(1, 2))
	if _, err := c.BindEndpoint(mustOperation(t, "op-bind-ep-1", req), req); err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}
	// Second bind with next revision fails: already bound.
	again := bindEndpointRequest(c, "t1", preconditionOf(1, 3))
	if _, err := c.BindEndpoint(mustOperation(t, "op-bind-ep-2", again), again); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-bind endpoint = %v, want ErrConflict", err)
	}
}

func TestCanonicalBindEndpointBlockedBySpawnHold(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustBindWorktree(t, c, "t1")

	hold := CanonicalAddHoldRequest{
		HomeID:  c.HomeID(),
		HoldID:  "spawn-hold",
		Scope:   DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionSpawn},
		Reason:  "freeze spawn",
	}
	if _, err := c.AddHold(mustOperation(t, "op-hold-spawn", hold), hold); err != nil {
		t.Fatalf("AddHold: %v", err)
	}

	req := bindEndpointRequest(c, "t1", preconditionOf(1, 2))
	if _, err := c.BindEndpoint(mustOperation(t, "op-bind-ep-held", req), req); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("bind endpoint held = %v, want ErrDispatchHeld", err)
	}

	// Releasing the hold allows the spawn.
	release := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "spawn-hold", Reason: "resume"}
	if _, err := c.ReleaseHold(mustOperation(t, "op-release-spawn", release), release); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if _, err := c.BindEndpoint(mustOperation(t, "op-bind-ep-after", req), req); err != nil {
		t.Fatalf("bind endpoint after release: %v", err)
	}
}

func TestCanonicalBindEndpointIdempotentReplay(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustBindWorktree(t, c, "t1")

	req := bindEndpointRequest(c, "t1", preconditionOf(1, 2))
	op := mustOperation(t, "op-bind-ep-replay", req)
	first, err := c.BindEndpoint(op, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.BindEndpoint(op, req)
	if err != nil {
		t.Fatalf("replay bind endpoint: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
	if second.Phase != PhaseWorking || second.Revision != first.Revision {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}
}

func TestCanonicalBindEndpointOperationReusedConflict(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustBindWorktree(t, c, "t1")

	req := bindEndpointRequest(c, "t1", preconditionOf(1, 2))
	op := mustOperation(t, "op-shared-bind-ep", req)
	if _, err := c.BindEndpoint(op, req); err != nil {
		t.Fatal(err)
	}

	diff := bindEndpointRequest(c, "t1", preconditionOf(1, 2))
	diff.Binding.Handle = "handle-2"
	reused, err := domain.NewOperation(op.ID, diff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.BindEndpoint(reused, diff); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

// TestCanonicalBindingsSurviveHomeReopen proves the worktree and endpoint
// bindings committed on the canonical surface are durable across a home
// reopen and reread through a fresh Canonical.
func TestCanonicalBindingsSurviveHomeReopen(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")
	wt := bindWorktreeRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-persist", wt), wt); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	ep := bindEndpointRequest(c, "t1", preconditionOf(1, 2))
	if _, err := c.BindEndpoint(mustOperation(t, "op-ep-persist", ep), ep); err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}

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
	if agg.Worktree == nil || agg.Endpoint == nil {
		t.Fatalf("bindings lost across reopen: worktree=%+v endpoint=%+v", agg.Worktree, agg.Endpoint)
	}
	if agg.Phase != PhaseWorking || agg.Revision != 3 {
		t.Fatalf("reread aggregate = phase %s rev %d, want working/3", agg.Phase, agg.Revision)
	}
}

// TestCanonicalBindWorktreeInterruptedCommitRecovers proves an interrupted
// home.Commit of a worktree binding is recovered mechanically on the next
// home.Open: the binding is present exactly once, the revision advances exactly
// once, and no duplicate or contradictory Task state is left behind.
func TestCanonicalBindWorktreeInterruptedCommitRecovers(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Simulate an interrupted BindWorktree: plant a write-ahead journal record
	// that would commit the worktree binding and receipt at scope revision 1,
	// exactly as a real interrupted home.Commit would.
	next := Aggregate{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        "t1",
		Generation:    1,
		Revision:      2,
		Current:       true,
		Definition:    TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"},
		Phase:         PhaseQueued,
	}
	wt := worktreeBinding()
	next.Worktree = &wt

	docData, err := json.Marshal(taskDoc{HomeRevision: 2, Aggregate: next})
	if err != nil {
		t.Fatal(err)
	}
	rec := receipt{OperationID: "op-interrupted-bind", Digest: "intent", TaskID: "t1", Generation: 1, Revision: 2, Phase: string(PhaseQueued)}
	recData, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	scope := taskScope("t1")
	txnID := "op-interrupted-bind"
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
			{Root: home.RootState, Key: receiptKey("op-interrupted-bind"), Data: recData},
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
		t.Fatalf("read recovered binding: %v", err)
	}
	if agg.Worktree == nil || agg.Worktree.LeaseID != "lease-wt" {
		t.Fatalf("recovered aggregate worktree = %+v", agg.Worktree)
	}
	if agg.Revision != 2 {
		t.Fatalf("recovered revision = %d, want 2 (advanced exactly once)", agg.Revision)
	}

	// The recovered scope revision is 2: a fresh mutation must use it.
	ep := bindEndpointRequest(c2, "t1", preconditionOf(1, 2))
	if _, err := c2.BindEndpoint(mustOperation(t, "op-bind-after-recovery", ep), ep); err != nil {
		t.Fatalf("bind endpoint after recovery: %v", err)
	}
}

// TestCanonicalMalformedInterruptedStateFailsClosed proves a journal record
// that replays to a malformed current document fails closed on read rather
// than serving contradictory state.
func TestCanonicalMalformedInterruptedStateFailsClosed(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Plant an interrupted journal record whose item writes malformed JSON to
	// the current document. Recovery replays it verbatim; the canonical read
	// must then fail closed instead of serving the corrupt document.
	scope := taskScope("t1")
	txnID := "op-corrupt-bind"
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