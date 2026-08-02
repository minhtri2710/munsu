package taskauthority

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// mustEndpointBinding builds a valid generation-bound endpoint binding.
func mustEndpointBinding(t *testing.T, leaseID, fenceToken string) EndpointBinding {
	t.Helper()
	b := EndpointBinding{
		Backend:      "herdr",
		Handle:       "session:pane-1",
		LeaseID:      leaseID,
		FenceToken:   fenceToken,
		SessionOwner: "session",
		WorkspaceID:  "workspace-1",
		TabID:        "tab-1",
		BoundAtUnix:  time.Now().Unix(),
	}
	if err := validateEndpointBinding(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// bindWorktreeForConfirmSpawn binds a generation-scoped worktree so
// ConfirmSpawn can evaluate its worktree-binding precondition.
func bindWorktreeForConfirmSpawn(t *testing.T, a *Authority, taskID string) {
	t.Helper()
	if _, err := a.BindWorktree(BindWorktreeRequest{
		OperationID: "op-bind-wt-" + taskID, Actor: Actor{ID: "test", Rank: "general"},
		TaskID: taskID, ExpectedGeneration: 1,
		Binding: mustWorktreeBinding(t, "wt-lease-"+taskID, "wt-fence-"+taskID),
		Reason:  "spawn",
	}); err != nil {
		t.Fatalf("BindWorktree(%s): %v", taskID, err)
	}
}

// TestAuthorityConfirmSpawnCommitsEndpointAndWorkingAtomically proves one
// ConfirmSpawn operation commits the endpoint binding and the queued →
// working transition together: the aggregate gains the full binding, the
// phase becomes working, the Revision advances by exactly one, and the typed
// lifecycle audit event records the transition with the reason.
func TestAuthorityConfirmSpawnCommitsEndpointAndWorkingAtomically(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	bindWorktreeForConfirmSpawn(t, a, "t1")

	confirmed, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-1", "fence-1"),
		Reason:  "spawned",
	})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.TaskID != "t1" || confirmed.Generation != 1 || confirmed.Revision != 3 || confirmed.Phase != PhaseWorking || confirmed.Reopened {
		t.Fatalf("confirm result = %+v, want revision 3 working", confirmed)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Endpoint == nil {
		t.Fatal("endpoint binding missing after confirm spawn")
	}
	if agg.Endpoint.Backend != "herdr" || agg.Endpoint.Handle != "session:pane-1" ||
		agg.Endpoint.LeaseID != "lease-1" || agg.Endpoint.FenceToken != "fence-1" ||
		agg.Endpoint.SessionOwner != "session" || agg.Endpoint.WorkspaceID != "workspace-1" ||
		agg.Endpoint.TabID != "tab-1" || agg.Endpoint.BoundAtUnix <= 0 {
		t.Fatalf("endpoint binding = %+v", agg.Endpoint)
	}
	if agg.Phase != PhaseWorking {
		t.Fatalf("phase = %q, want working", agg.Phase)
	}
	if agg.Revision != 3 {
		t.Fatalf("revision = %d, want 3", agg.Revision)
	}
	if agg.Worktree == nil {
		t.Fatal("worktree binding must survive confirm spawn")
	}

	// A typed lifecycle audit event committed with the transition.
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var spawnEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.OperationID == "op-confirm" {
			spawnEvents = append(spawnEvents, ev)
		}
	}
	if len(spawnEvents) != 1 {
		t.Fatalf("confirm audit events = %d, want 1 (%+v)", len(spawnEvents), v.Audit)
	}
	ev := spawnEvents[0]
	if ev.Kind != AuditLifecycle || ev.TaskID != "t1" || ev.Generation != 1 ||
		ev.Before != PhaseQueued || ev.After != PhaseWorking || ev.Reason != "spawned" {
		t.Fatalf("confirm audit event = %+v", ev)
	}
}

// TestAuthorityConfirmSpawnReplaysSameOperation proves repeating the same
// Operation ID with the same intent replays the original receipt and never
// advances the Revision or mutates the committed binding.
func TestAuthorityConfirmSpawnReplaysSameOperation(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	bindWorktreeForConfirmSpawn(t, a, "t1")
	req := ConfirmSpawnRequest{
		OperationID: "op-confirm", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-1", "fence-1"),
		Reason:  "spawned",
	}
	first, err := a.ConfirmSpawn(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.ConfirmSpawn(req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != first.Revision || second.Generation != first.Generation || second.Phase != first.Phase {
		t.Fatalf("replayed confirm result = %+v, want original %+v", second, first)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 3 {
		t.Fatalf("replay advanced revision to %d, want 3", agg.Revision)
	}
	if agg.Endpoint == nil || agg.Endpoint.LeaseID != "lease-1" {
		t.Fatalf("endpoint after replay = %+v", agg.Endpoint)
	}
}

// TestAuthorityConfirmSpawnConflictsOnAlreadyBoundEndpoint proves a second
// ConfirmSpawn for the same generation fails closed with a typed conflict —
// whether the payload matches (only the exact same Operation ID replays) or
// differs — and reusing the Operation ID with a different intent conflicts
// non-retryably.
func TestAuthorityConfirmSpawnConflictsOnAlreadyBoundEndpoint(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	bindWorktreeForConfirmSpawn(t, a, "t1")
	first := mustEndpointBinding(t, "lease-1", "fence-1")
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: first, Reason: "spawned",
	}); err != nil {
		t.Fatal(err)
	}

	// Different lease on the same generation fails closed.
	conflicting := mustEndpointBinding(t, "lease-2", "fence-2")
	conflicting.Handle = "session:pane-2"
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm-2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: conflicting, Reason: "spawned",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting confirm error = %v, want ErrConflict", err)
	}

	// Identical payload under a new Operation ID also fails closed: the
	// endpoint is already bound and only the same operation replays.
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm-3", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: first, Reason: "spawned",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("identical confirm under new operation error = %v, want ErrConflict", err)
	}

	// Reusing the operation ID with a different intent conflicts non-retryably.
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: conflicting, Reason: "spawned",
	}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused operation id error = %v, want ErrOperationConflict", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 3 || agg.Endpoint == nil || agg.Endpoint.LeaseID != "lease-1" {
		t.Fatalf("aggregate after failed confirms = %+v", agg)
	}
}

// TestAuthorityConfirmSpawnFailsClosedOnStaleGeneration proves the Expected
// Generation fence and task identity are revalidated inside the transaction:
// a stale generation conflicts and a missing task is not found.
func TestAuthorityConfirmSpawnFailsClosedOnStaleGeneration(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	bindWorktreeForConfirmSpawn(t, a, "t1")

	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-stale", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 7,
		Binding: mustEndpointBinding(t, "lease-1", "fence-1"), Reason: "spawned",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation confirm error = %v, want ErrConflict", err)
	}
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-missing", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "missing", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-1", "fence-1"), Reason: "spawned",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task confirm error = %v, want ErrNotFound", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Endpoint != nil || agg.Phase != PhaseQueued || agg.Revision != 2 {
		t.Fatalf("failed confirms must not mutate: %+v", agg)
	}
}

// TestAuthorityConfirmSpawnFailsClosedOnNonQueuedPhase proves ConfirmSpawn
// only applies to a queued task: a working task conflicts and nothing
// mutates.
func TestAuthorityConfirmSpawnFailsClosedOnNonQueuedPhase(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	bindWorktreeForConfirmSpawn(t, a, "t1")
	if _, err := a.Start(StartRequest{
		OperationID: "op-start", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-1", "fence-1"), Reason: "spawned",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("confirm on working task error = %v, want ErrConflict", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Endpoint != nil || agg.Phase != PhaseWorking || agg.Revision != 3 {
		t.Fatalf("failed confirm must not mutate: %+v", agg)
	}
}

// TestAuthorityConfirmSpawnRequiresWorktreeBinding proves ConfirmSpawn is
// only valid after the generation-bound worktree binding (Task 4.1) exists:
// a queued task without one fails closed with a typed conflict.
func TestAuthorityConfirmSpawnRequiresWorktreeBinding(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-1", "fence-1"), Reason: "spawned",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("confirm without worktree binding error = %v, want ErrConflict", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Endpoint != nil || agg.Phase != PhaseQueued || agg.Revision != 1 {
		t.Fatalf("failed confirm must not mutate: %+v", agg)
	}
}

// TestAuthorityConfirmSpawnFailsClosedOnMissingOwner proves ConfirmSpawn
// revalidates owner presence inside the transaction and reports the typed
// readiness reason when the committed aggregate has no owner.
func TestAuthorityConfirmSpawnFailsClosedOnMissingOwner(t *testing.T) {
	store := newMemStore()
	a := New(store)
	createTask(t, a, "t1")
	bindWorktreeForConfirmSpawn(t, a, "t1")
	// Strip the owner directly in the committed store state: every public
	// mutation path validates owner presence, so only an adversarial record
	// can reach ConfirmSpawn ownerless. The operation must still fail closed.
	store.mu.Lock()
	cur := store.state.aggregates[aggregateKey("t1", 1)]
	cur.Definition.Owner = ""
	store.state.aggregates[aggregateKey("t1", 1)] = cur
	store.mu.Unlock()

	_, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-1", "fence-1"), Reason: "spawned",
	})
	if !errors.Is(err, ErrPrecondition) {
		t.Fatalf("ownerless confirm error = %v, want ErrPrecondition", err)
	}
	if !strings.Contains(err.Error(), string(ReadinessMissingOwner)) {
		t.Fatalf("ownerless confirm error %v does not name the typed readiness reason %q", err, ReadinessMissingOwner)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Endpoint != nil || agg.Phase != PhaseQueued {
		t.Fatalf("failed confirm must not mutate: %+v", agg)
	}
}

// TestAuthorityConfirmSpawnRespectsDispatchHolds proves ConfirmSpawn
// evaluates applicable durable Dispatch Holds for the spawn action inside the
// same transaction as the transition, so a concurrent hold cannot interleave:
// a matching hold blocks with ErrDispatchHeld, a hold scoped to another
// action does not, and releasing the hold unblocks the transition.
func TestAuthorityConfirmSpawnRespectsDispatchHolds(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	bindWorktreeForConfirmSpawn(t, a, "t1")

	if _, err := a.CreateHold(CreateHoldRequest{
		OperationID: "op-hold-spawn", Actor: Actor{ID: "test", Rank: "general"},
		ID: "hold-spawn", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionSpawn}, Reason: "freeze spawn",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-1", "fence-1"), Reason: "spawned",
	})
	if !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("confirm under spawn hold = %v, want ErrDispatchHeld", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Endpoint != nil || agg.Phase != PhaseQueued {
		t.Fatalf("held confirm must not mutate: %+v", agg)
	}

	// A hold scoped to another action does not block the spawn transition.
	if _, err := a.CreateHold(CreateHoldRequest{
		OperationID: "op-hold-start", Actor: Actor{ID: "test", Rank: "general"},
		ID: "hold-start", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionStart}, Reason: "freeze start",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm-2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-2", "fence-2"), Reason: "spawned",
	}); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("confirm under start-only hold = %v, want ErrDispatchHeld (spawn hold still active)", err)
	}

	// Releasing the spawn hold unblocks the transition.
	if _, err := a.ReleaseHold(ReleaseHoldRequest{
		OperationID: "op-release", Actor: Actor{ID: "test", Rank: "general"},
		ID: "hold-spawn", Reason: "approved",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-confirm-3", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "lease-3", "fence-3"), Reason: "spawned",
	}); err != nil {
		t.Fatalf("confirm after hold release: %v", err)
	}
}

// TestAuthorityConfirmSpawnValidatesPayload proves the endpoint binding
// payload is validated before any mutation: missing backend, handle, lease,
// fence token, or bound timestamp fails closed.
func TestAuthorityConfirmSpawnValidatesPayload(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	bindWorktreeForConfirmSpawn(t, a, "t1")
	base := mustEndpointBinding(t, "lease-1", "fence-1")

	cases := []struct {
		name   string
		mutate func(*EndpointBinding)
	}{
		{"missing backend", func(b *EndpointBinding) { b.Backend = "" }},
		{"missing handle", func(b *EndpointBinding) { b.Handle = "" }},
		{"missing lease id", func(b *EndpointBinding) { b.LeaseID = "" }},
		{"missing fence token", func(b *EndpointBinding) { b.FenceToken = "" }},
		{"missing bound timestamp", func(b *EndpointBinding) { b.BoundAtUnix = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := base
			tc.mutate(&bad)
			if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
				OperationID: "op-bad-" + tc.name, Actor: Actor{ID: "test", Rank: "general"},
				TaskID: "t1", ExpectedGeneration: 1, Binding: bad, Reason: "spawned",
			}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("confirm error = %v, want ErrInvalidInput", err)
			}
		})
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Endpoint != nil || agg.Phase != PhaseQueued || agg.Revision != 2 {
		t.Fatalf("invalid confirms must not mutate: %+v", agg)
	}
}
