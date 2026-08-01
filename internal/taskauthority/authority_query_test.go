package taskauthority

import (
	"errors"
	"testing"
)

// newTestAuthority builds an Authority over a fresh in-memory store.
func newTestAuthority(t *testing.T) *Authority {
	t.Helper()
	return New(newMemStore())
}

// createTask is a test helper that creates one queued task.
func createTask(t *testing.T, a *Authority, taskID string) Result {
	t.Helper()
	res, err := a.Create(CreateRequest{
		OperationID: "op-create-" + taskID,
		Actor:       Actor{ID: "test", Rank: "general"},
		TaskID:      taskID,
		Owner:       "owner",
		Description: "work",
		Kind:        "ship",
		Project:     "proj",
		Reason:      "create",
	})
	if err != nil {
		t.Fatalf("Create(%s): %v", taskID, err)
	}
	return res
}

func TestAuthorityGetAndListReturnCanonicalRecords(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	createTask(t, a, "t2")

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.TaskID != "t1" || agg.Generation != 1 || agg.Phase != PhaseQueued {
		t.Fatalf("Get = %+v", agg)
	}
	if agg.Definition.Owner != "owner" || agg.Definition.Kind != "ship" || agg.Definition.Project != "proj" {
		t.Fatalf("definition = %+v", agg.Definition)
	}

	list, err := a.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].TaskID != "t1" || list[1].TaskID != "t2" {
		t.Fatalf("List order = %+v", list)
	}

	if _, err := a.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
	}
}

func TestAuthorityReadinessTypedReasons(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t-queued")
	createTask(t, a, "t-blocked")
	createTask(t, a, "t-working")
	createTask(t, a, "t-done")

	if _, err := a.Block(BlockRequest{
		OperationID: "op-block", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t-blocked", ExpectedGeneration: 1, Detail: "dep", Reason: "dep",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(StartRequest{
		OperationID: "op-start", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t-working", ExpectedGeneration: 1, Reason: "go",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-done", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t-done", ExpectedGeneration: 1, To: PhaseDone, Reason: "done",
	}); err != nil {
		t.Fatal(err)
	}

	queued, err := a.Readiness("t-queued")
	if err != nil {
		t.Fatal(err)
	}
	if !queued.Ready || len(queued.BlockingReasons) != 0 {
		t.Fatalf("queued readiness = %+v", queued)
	}
	blocked, err := a.Readiness("t-blocked")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Ready || len(blocked.BlockingReasons) != 1 || blocked.BlockingReasons[0] != ReadinessBlocked {
		t.Fatalf("blocked readiness = %+v", blocked)
	}
	working, err := a.Readiness("t-working")
	if err != nil {
		t.Fatal(err)
	}
	if working.Ready || len(working.BlockingReasons) != 1 || working.BlockingReasons[0] != ReadinessInFlight {
		t.Fatalf("working readiness = %+v", working)
	}
	done, err := a.Readiness("t-done")
	if err != nil {
		t.Fatal(err)
	}
	if done.Ready || len(done.BlockingReasons) != 1 || done.BlockingReasons[0] != ReadinessTerminal {
		t.Fatalf("terminal readiness = %+v", done)
	}

	missing, err := a.Readiness("missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.BlockingReasons) != 1 || missing.BlockingReasons[0] != ReadinessNotFound {
		t.Fatalf("missing readiness = %+v", missing)
	}
}

func TestAuthorityReadinessReportsDispatchHold(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.CreateHold(CreateHoldRequest{
		OperationID: "op-hold", Actor: Actor{ID: "test", Rank: "general"},
		ID: "hold-t1", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionStart}, Reason: "review",
	}); err != nil {
		t.Fatal(err)
	}
	r, err := a.Readiness("t1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Ready || len(r.BlockingReasons) != 1 || r.BlockingReasons[0] != ReadinessDispatchHold {
		t.Fatalf("held readiness = %+v", r)
	}
}
