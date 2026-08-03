package taskauthority

import (
	"errors"
	"testing"
)

// TestAuthorityPromoteFlipsScoutKindWithFence proves Promote is a named
// semantic operation: it flips a terminal scout Generation to ship kind with
// the Expected Generation fence revalidated inside the Store transaction, one
// Revision advance, one typed audit event, and a durable receipt.
func TestAuthorityPromoteFlipsScoutKindWithFence(t *testing.T) {
	a := newTestAuthority(t)
	createScoutTask(t, a, "t1")
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-complete", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseDone, Reason: "explored",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := a.Promote(PromoteRequest{
		OperationID: "op-promote", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "promote to ship",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != "t1" || res.Generation != 1 || res.Phase != PhaseDone {
		t.Fatalf("result = %+v", res)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Definition.Kind != "ship" {
		t.Fatalf("kind = %q, want ship", agg.Definition.Kind)
	}
	if agg.Revision != 3 { // create + complete + promote
		t.Fatalf("revision = %d, want 3", agg.Revision)
	}
	events := auditForTaskID(t, a, "t1")
	if len(events) != 3 {
		t.Fatalf("audit events = %d, want 3", len(events))
	}
	if events[2].Kind != AuditPromote || events[2].TaskID != "t1" || events[2].Generation != 1 || events[2].Reason != "promote to ship" {
		t.Fatalf("promote audit = %+v", events[2])
	}
}

// TestAuthorityPromoteRefusesNonScout proves Promote fails closed for a task
// that is not a scout: the kind precondition is enforced inside the Store
// transaction and nothing mutates.
func TestAuthorityPromoteRefusesNonScout(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1") // kind ship
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-complete", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseDone, Reason: "done",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Promote(PromoteRequest{
		OperationID: "op-promote", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "promote",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("promote ship = %v, want ErrPrecondition", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Definition.Kind != "ship" || agg.Revision != 2 {
		t.Fatalf("aggregate mutated on refused promote = %+v", agg)
	}
}

// TestAuthorityPromoteRefusesNonTerminal proves Promote refuses a live scout:
// a queued or working scout must fail closed (only done/resolved scouts
// promote), mirroring the legacy CLI preflight.
func TestAuthorityPromoteRefusesNonTerminal(t *testing.T) {
	a := newTestAuthority(t)
	createScoutTask(t, a, "t1")
	if _, err := a.Promote(PromoteRequest{
		OperationID: "op-promote-1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "promote",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("promote queued = %v, want ErrPrecondition", err)
	}
	// working scout
	if _, err := a.Start(StartRequest{
		OperationID: "op-start", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "start",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Promote(PromoteRequest{
		OperationID: "op-promote-2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "promote",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("promote working = %v, want ErrPrecondition", err)
	}
	// retired scouts must never promote.
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-complete", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseDone, Reason: "done",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Retire(RetireRequest{
		OperationID: "op-retire", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "retire",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Promote(PromoteRequest{
		OperationID: "op-promote-3", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "promote",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("promote retired = %v, want ErrPrecondition", err)
	}
}

// TestAuthorityPromoteRefusesStaleGeneration proves Promote revalidates the
// Expected Generation fence inside the transaction: a stale generation
// conflicts and mutates nothing.
func TestAuthorityPromoteRefusesStaleGeneration(t *testing.T) {
	a := newTestAuthority(t)
	createScoutTask(t, a, "t1")
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-complete", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseDone, Reason: "done",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reopen(ReopenRequest{
		OperationID: "op-reopen", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "reopen",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Promote(PromoteRequest{
		OperationID: "op-promote-stale", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "promote",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("promote stale generation = %v, want ErrConflict", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Generation != 2 || agg.Definition.Kind != "scout" {
		t.Fatalf("aggregate = %+v", agg)
	}
}

// TestAuthorityPromoteReplayIsIdempotent proves same-op replay returns the
// original receipt with no second Revision advance or audit, and a changed
// digest under the same Operation ID is a non-retryable conflict.
func TestAuthorityPromoteReplayIsIdempotent(t *testing.T) {
	a := newTestAuthority(t)
	createScoutTask(t, a, "t1")
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-complete", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseDone, Reason: "done",
	}); err != nil {
		t.Fatal(err)
	}
	req := PromoteRequest{
		OperationID: "op-promote", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "promote",
	}
	first, err := a.Promote(req)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := a.Promote(req)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Revision != first.Revision || replayed.Generation != first.Generation || replayed.Phase != first.Phase {
		t.Fatalf("replay = %+v, want original %+v", replayed, first)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 3 {
		t.Fatalf("revision after replay = %d, want 3", agg.Revision)
	}
	// changed digest under the same Operation ID is non-retryable
	changed := req
	changed.Reason = "changed intent"
	if _, err := a.Promote(changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("promote changed digest = %v, want ErrOperationConflict", err)
	}
}

func createScoutTask(t *testing.T, a *Authority, id string) Result {
	t.Helper()
	res, err := a.Create(CreateRequest{
		OperationID: "op-create-" + id, Actor: Actor{ID: "test", Rank: "general"},
		TaskID: id, Owner: "test", Description: "explore", Kind: "scout", Reason: "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func auditForTaskID(t *testing.T, a *Authority, id string) []AuditEvent {
	t.Helper()
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var out []AuditEvent
	for _, ev := range v.Audit {
		if ev.TaskID == id {
			out = append(out, ev)
		}
	}
	return out
}
