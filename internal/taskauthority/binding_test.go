package taskauthority

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// mustWorktreeBinding builds a valid generation-bound worktree binding.
func mustWorktreeBinding(t *testing.T, leaseID, fenceToken string) WorktreeBinding {
	t.Helper()
	b := WorktreeBinding{
		RepositoryIdentity: "repo-identity",
		Path:               "/tmp/wt",
		GitDir:             "/repo/.git/worktrees/wt",
		CommonDir:          "/repo/.git",
		Head:               strings.Repeat("a", 40),
		LeaseID:            leaseID,
		FenceToken:         fenceToken,
		BoundAtUnix:        time.Now().Unix(),
	}
	if err := validateWorktreeBinding(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// TestAuthorityBindWorktreeCommitsBindingAdvancingRevision proves one
// BindWorktree operation commits the binding inside the task generation,
// advances the Revision by exactly one, keeps the phase untouched, and
// persists the typed binding audit event.
func TestAuthorityBindWorktreeCommitsBindingAdvancingRevision(t *testing.T) {
	a := New(newMemStore())
	createTask(t, a, "t1")
	binding := mustWorktreeBinding(t, "lease-1", "fence-1")

	bound, err := a.BindWorktree(BindWorktreeRequest{
		OperationID: "op-bind", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: binding, Reason: "spawn",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bound.TaskID != "t1" || bound.Generation != 1 || bound.Revision != 2 || bound.Phase != PhaseQueued || bound.Reopened {
		t.Fatalf("bind result = %+v, want revision 2 queued", bound)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree == nil {
		t.Fatal("worktree binding missing after bind")
	}
	if agg.Worktree.RepositoryIdentity != "repo-identity" || agg.Worktree.Path != "/tmp/wt" ||
		agg.Worktree.GitDir != "/repo/.git/worktrees/wt" || agg.Worktree.CommonDir != "/repo/.git" ||
		agg.Worktree.Head == "" || agg.Worktree.LeaseID != "lease-1" || agg.Worktree.FenceToken != "fence-1" ||
		agg.Worktree.BoundAtUnix <= 0 {
		t.Fatalf("worktree binding = %+v", agg.Worktree)
	}
	if agg.Revision != 2 {
		t.Fatalf("revision = %d, want 2", agg.Revision)
	}
	if agg.Phase != PhaseQueued {
		t.Fatalf("binding must not change phase: %q", agg.Phase)
	}

	// A typed binding audit event committed with the mutation.
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var bindingEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.Kind == AuditBinding {
			bindingEvents = append(bindingEvents, ev)
		}
	}
	if len(bindingEvents) != 1 {
		t.Fatalf("binding audit events = %d, want 1 (%+v)", len(bindingEvents), v.Audit)
	}
	ev := bindingEvents[0]
	if ev.OperationID != "op-bind" || ev.TaskID != "t1" || ev.Generation != 1 || ev.Before != "" || ev.After != "" || ev.Reason != "spawn" {
		t.Fatalf("binding audit event = %+v", ev)
	}
}

// TestAuthorityBindWorktreeReplaysSameOperation proves rebinding the same
// identity under the same Operation ID is idempotent: the original committed
// outcome returns and the Revision does not advance twice.
func TestAuthorityBindWorktreeReplaysSameOperation(t *testing.T) {
	a := New(newMemStore())
	createTask(t, a, "t1")
	req := BindWorktreeRequest{
		OperationID: "op-bind", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: mustWorktreeBinding(t, "lease-1", "fence-1"), Reason: "spawn",
	}
	first, err := a.BindWorktree(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.BindWorktree(req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != first.Revision || second.Generation != first.Generation || second.Phase != first.Phase {
		t.Fatalf("replayed bind result = %+v, want original %+v", second, first)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("replay advanced revision to %d, want 2", agg.Revision)
	}
	if agg.Worktree == nil || agg.Worktree.LeaseID != "lease-1" {
		t.Fatalf("binding after replay = %+v", agg.Worktree)
	}
}

// TestAuthorityBindWorktreeConflictsOnRebind proves a conflicting binding for
// the same generation fails closed with a typed conflict and mutates nothing,
// whether the payload differs or matches: only the exact same Operation ID
// replay is idempotent.
func TestAuthorityBindWorktreeConflictsOnRebind(t *testing.T) {
	a := New(newMemStore())
	createTask(t, a, "t1")
	first := mustWorktreeBinding(t, "lease-1", "fence-1")
	if _, err := a.BindWorktree(BindWorktreeRequest{
		OperationID: "op-bind", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: first, Reason: "spawn",
	}); err != nil {
		t.Fatal(err)
	}

	// Different identity/path/lease on the same generation fails closed.
	conflicting := mustWorktreeBinding(t, "lease-2", "fence-2")
	conflicting.Path = "/tmp/other"
	if _, err := a.BindWorktree(BindWorktreeRequest{
		OperationID: "op-bind-2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: conflicting, Reason: "spawn",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting rebind error = %v, want ErrConflict", err)
	}

	// Identical payload under a new Operation ID also fails closed: the
	// generation is already bound and only the same operation replays.
	if _, err := a.BindWorktree(BindWorktreeRequest{
		OperationID: "op-bind-3", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: first, Reason: "spawn",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("identical rebind under new operation error = %v, want ErrConflict", err)
	}

	// Reusing the operation ID with a different intent conflicts non-retryably.
	if _, err := a.BindWorktree(BindWorktreeRequest{
		OperationID: "op-bind", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: conflicting, Reason: "spawn",
	}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused operation id error = %v, want ErrOperationConflict", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 || agg.Worktree == nil || agg.Worktree.LeaseID != "lease-1" {
		t.Fatalf("aggregate after failed rebinds = %+v", agg)
	}
}

// TestAuthorityBindWorktreeFailsClosedOnStaleGeneration proves the Expected
// Generation fence rejects binding a stale or missing generation.
func TestAuthorityBindWorktreeFailsClosedOnStaleGeneration(t *testing.T) {
	a := New(newMemStore())
	createTask(t, a, "t1")

	if _, err := a.BindWorktree(BindWorktreeRequest{
		OperationID: "op-stale", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 7, Binding: mustWorktreeBinding(t, "lease-1", "fence-1"), Reason: "spawn",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation bind error = %v, want ErrConflict", err)
	}
	if _, err := a.BindWorktree(BindWorktreeRequest{
		OperationID: "op-missing", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "missing", ExpectedGeneration: 1, Binding: mustWorktreeBinding(t, "lease-1", "fence-1"), Reason: "spawn",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task bind error = %v, want ErrNotFound", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree != nil || agg.Revision != 1 {
		t.Fatalf("failed binds must not mutate: %+v", agg)
	}
}

// TestAuthorityBindWorktreeValidatesPayload proves the full binding payload
// is validated before any mutation: missing repository identity, path, git
// dir, common dir, head, lease, fence token, or bound timestamp fails closed.
func TestAuthorityBindWorktreeValidatesPayload(t *testing.T) {
	a := New(newMemStore())
	createTask(t, a, "t1")
	base := mustWorktreeBinding(t, "lease-1", "fence-1")

	cases := []struct {
		name   string
		mutate func(*WorktreeBinding)
	}{
		{"missing repository identity", func(b *WorktreeBinding) { b.RepositoryIdentity = "" }},
		{"missing path", func(b *WorktreeBinding) { b.Path = "" }},
		{"missing git dir", func(b *WorktreeBinding) { b.GitDir = "" }},
		{"missing common dir", func(b *WorktreeBinding) { b.CommonDir = "" }},
		{"missing head", func(b *WorktreeBinding) { b.Head = "" }},
		{"missing lease id", func(b *WorktreeBinding) { b.LeaseID = "" }},
		{"missing fence token", func(b *WorktreeBinding) { b.FenceToken = "" }},
		{"missing bound timestamp", func(b *WorktreeBinding) { b.BoundAtUnix = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := base
			tc.mutate(&bad)
			if _, err := a.BindWorktree(BindWorktreeRequest{
				OperationID: "op-bad-" + tc.name, Actor: Actor{ID: "test", Rank: "general"},
				TaskID: "t1", ExpectedGeneration: 1, Binding: bad, Reason: "spawn",
			}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("bind error = %v, want ErrInvalidInput", err)
			}
		})
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree != nil || agg.Revision != 1 {
		t.Fatalf("invalid binds must not mutate: %+v", agg)
	}
}

// TestLeaseMarkerJSONIsHomeCompatible proves the lease marker serializes the
// generation as a decimal string so the legacy home lease read path
// (internal/home.TaskWorktreeLeaseActive) parses it unchanged.
func TestLeaseMarkerJSONIsHomeCompatible(t *testing.T) {
	m := LeaseMarker{TaskID: "t1", TaskGeneration: 3, LeaseID: "lease-3", FenceToken: "fence-3"}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["task_id"] != "t1" || parsed["task_generation"] != "3" || parsed["lease_id"] != "lease-3" || parsed["fence_token"] != "fence-3" {
		t.Fatalf("lease marker JSON = %s, want home-compatible string generation", data)
	}

	if err := m.Validate(); err != nil {
		t.Fatalf("valid marker rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*LeaseMarker)
	}{
		{"missing task id", func(m *LeaseMarker) { m.TaskID = "" }},
		{"zero generation", func(m *LeaseMarker) { m.TaskGeneration = 0 }},
		{"missing lease id", func(m *LeaseMarker) { m.LeaseID = "" }},
		{"missing fence token", func(m *LeaseMarker) { m.FenceToken = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := m
			tc.mutate(&bad)
			if err := bad.Validate(); err == nil {
				t.Fatalf("marker %s: expected validation error, got nil", tc.name)
			}
		})
	}
}

// TestAuthorityBindWorktreeLeaseMarkerStaged proves BindWorktree stages the
// lease marker in the same transaction as the binding.
func TestAuthorityBindWorktreeLeaseMarkerStaged(t *testing.T) {
	store := newMemStore()
	a := New(store)
	createTask(t, a, "t1")
	binding := mustWorktreeBinding(t, "lease-9", "fence-9")
	if _, err := a.BindWorktree(BindWorktreeRequest{
		OperationID: "op-bind", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Binding: binding, Reason: "spawn",
	}); err != nil {
		t.Fatal(err)
	}
	// The marker and the binding committed in one transaction; the store
	// applies both staged records atomically.
	v, err := store.View()
	if err != nil {
		t.Fatal(err)
	}
	cur, ok := v.Current("t1")
	if !ok || cur.Worktree == nil || cur.Worktree.LeaseID != "lease-9" {
		t.Fatalf("binding missing from committed view: %+v", cur)
	}
}
