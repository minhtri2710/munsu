package taskauthority

import (
	"errors"
	"testing"
)

// TestAuthoritySupersedeRefusesLiveGeneration proves supersede refuses a
// generation that still owns live work: queued, blocked, and working tasks
// must fail closed. Only terminal generations may be superseded.
func TestAuthoritySupersedeRefusesLiveGeneration(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	// queued
	if _, err := a.Supersede(SupersedeRequest{
		OperationID: "op-supersede-1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "retry",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("supersede queued = %v, want ErrPrecondition", err)
	}

	// blocked
	if _, err := a.Block(BlockRequest{
		OperationID: "op-block", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Detail: "dependency", Reason: "blocked",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Supersede(SupersedeRequest{
		OperationID: "op-supersede-2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "retry",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("supersede blocked = %v, want ErrPrecondition", err)
	}

	// working (bound endpoint + worktree)
	if _, err := a.Unblock(UnblockRequest{
		OperationID: "op-unblock", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "cleared",
	}); err != nil {
		t.Fatal(err)
	}
	bindWorktreeForConfirmSpawn(t, a, "t1")
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-1", "fence-1"),
		Reason:  "spawned",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Supersede(SupersedeRequest{
		OperationID: "op-supersede-3", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "retry",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("supersede working = %v, want ErrPrecondition", err)
	}

	// No mutation occurred on failed supersede attempts.
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseWorking || agg.Revision != 5 {
		t.Fatalf("aggregate after failed supersedes = %+v", agg)
	}
}

// TestAuthoritySupersedeClearsStaleBindings proves the new generation never
// inherits the terminal generation's endpoint or worktree bindings; the prior
// generation keeps them as historical evidence.
func TestAuthoritySupersedeClearsStaleBindings(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	bindWorktreeForConfirmSpawn(t, a, "t1")
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-1", "fence-1"),
		Reason:  "spawned",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-done", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseResolved, Reason: "resolved",
	}); err != nil {
		t.Fatal(err)
	}

	superseded, err := a.Supersede(SupersedeRequest{
		OperationID: "op-supersede", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "retry",
	})
	if err != nil {
		t.Fatal(err)
	}
	if superseded.Generation != 2 || superseded.Revision != FirstRevision || superseded.Phase != PhaseQueued || !superseded.Reopened {
		t.Fatalf("supersede result = %+v, want generation 2 queued", superseded)
	}

	cur, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if cur.Endpoint != nil || cur.Worktree != nil {
		t.Fatalf("new generation must not carry stale bindings: endpoint=%+v worktree=%+v", cur.Endpoint, cur.Worktree)
	}

	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	historical, ok := v.Aggregate("t1", 1)
	if !ok || historical.Current {
		t.Fatalf("prior generation must remain as historical: %+v", historical)
	}
	if historical.Endpoint == nil || historical.Worktree == nil {
		t.Fatalf("historical generation lost its evidence bindings: %+v", historical)
	}
	if historical.Phase != PhaseResolved {
		t.Fatalf("historical phase changed: %+v", historical)
	}
}

// TestAuthoritySupersedePreservesTerminalHistory proves superseding a
// terminal generation keeps the prior generation immutable and starts the
// new one queued at Revision one.
func TestAuthoritySupersedePreservesTerminalHistory(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-done", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseDone, Reason: "merged",
	}); err != nil {
		t.Fatal(err)
	}

	superseded, err := a.Supersede(SupersedeRequest{
		OperationID: "op-supersede", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "retry",
	})
	if err != nil {
		t.Fatal(err)
	}
	if superseded.Generation != 2 || superseded.Revision != FirstRevision || superseded.Phase != PhaseQueued || !superseded.Reopened {
		t.Fatalf("supersede result = %+v", superseded)
	}

	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	old, ok := v.Aggregate("t1", 1)
	if !ok || old.Current || old.Phase != PhaseDone || old.Revision != 3 {
		t.Fatalf("historical aggregate = %+v ok=%v", old, ok)
	}
	cur, ok := v.Current("t1")
	if !ok || cur.Generation != 2 || cur.Phase != PhaseQueued {
		t.Fatalf("current aggregate = %+v ok=%v", cur, ok)
	}

	// A fresh supersede of the new queued generation refuses: it is live.
	if _, err := a.Supersede(SupersedeRequest{
		OperationID: "op-supersede-2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 2, Reason: "retry",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("supersede queued gen 2 = %v, want ErrPrecondition", err)
	}
	// A stale generation fence on supersede conflicts.
	if _, err := a.Supersede(SupersedeRequest{
		OperationID: "op-supersede-3", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "retry",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale supersede fence = %v, want ErrConflict", err)
	}
	// A missing task is not found.
	if _, err := a.Supersede(SupersedeRequest{
		OperationID: "op-supersede-4", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "missing", ExpectedGeneration: 1, Reason: "retry",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("supersede missing = %v, want ErrNotFound", err)
	}
}

// TestAuthoritySupersedeReplayReturnsOriginalOutcome proves the same
// Operation ID with the same intent replays the original receipt without
// creating a second generation, and a reused Operation ID with changed intent
// conflicts non-retryably.
func TestAuthoritySupersedeReplayReturnsOriginalOutcome(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-done", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseDone, Reason: "merged",
	}); err != nil {
		t.Fatal(err)
	}
	req := SupersedeRequest{
		OperationID: "op-supersede", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "retry",
	}
	first, err := a.Supersede(req)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := a.Supersede(req)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != first {
		t.Fatalf("replayed supersede = %+v, want %+v", replayed, first)
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := v.Current("t1")
	if cur.Generation != 2 {
		t.Fatalf("replay created a second generation: current = %+v", cur)
	}

	changed := req
	changed.Reason = "retry again"
	if _, err := a.Supersede(changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed-intent supersede = %v, want ErrOperationConflict", err)
	}
}

// TestAuthoritySupersedeEmitsTypedLifecycleAudit proves supersede commits a
// typed lifecycle audit event describing the terminal → queued transition on
// the new generation.
func TestAuthoritySupersedeEmitsTypedLifecycleAudit(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-done", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseResolved, Reason: "resolved",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Supersede(SupersedeRequest{
		OperationID: "op-supersede", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "backlog: retry",
	}); err != nil {
		t.Fatal(err)
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var found *AuditEvent
	for i := range v.Audit {
		if v.Audit[i].OperationID == "op-supersede" {
			found = &v.Audit[i]
		}
	}
	if found == nil {
		t.Fatalf("no supersede audit event: %+v", v.Audit)
	}
	if found.Kind != AuditLifecycle || found.TaskID != "t1" || found.Generation != 2 ||
		found.Before != PhaseResolved || found.After != PhaseQueued || found.Reason != "backlog: retry" {
		t.Fatalf("supersede audit = %+v", found)
	}
}
