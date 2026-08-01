package taskauthority

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAuthorityCreateHoldBlocksStart(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	if _, err := a.CreateHold(CreateHoldRequest{
		OperationID: "op-hold", Actor: Actor{ID: "test", Rank: "general"},
		ID: "hold-1", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionStart}, Reason: "review",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := a.Start(StartRequest{
		OperationID: "op-start", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	})
	if !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("start under hold = %v, want ErrDispatchHeld", err)
	}

	// Release, then start succeeds.
	if _, err := a.ReleaseHold(ReleaseHoldRequest{
		OperationID: "op-release", Actor: Actor{ID: "test", Rank: "general"},
		ID: "hold-1", Reason: "approved",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(StartRequest{
		OperationID: "op-start-2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	}); err != nil {
		t.Fatalf("start after release: %v", err)
	}
}

func TestAuthorityCreateHoldIsIdempotentAndScoped(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	req := CreateHoldRequest{
		OperationID: "op-hold", Actor: Actor{ID: "test", Rank: "general"},
		ID: "hold-1", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionStart}, Reason: "review",
	}
	first, err := a.CreateHold(req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed {
		t.Fatal("first create marked as replayed")
	}
	second, err := a.CreateHold(req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed {
		t.Fatal("repeated identical hold was not replayed")
	}

	// Same ID, different definition: conflict.
	changed := req
	changed.OperationID = "op-hold-bis"
	changed.Reason = "different reason"
	if _, err := a.CreateHold(changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting hold definition = %v, want ErrConflict", err)
	}

	// Hold scoped to another task does not block this task.
	createTask(t, a, "t2")
	if _, err := a.CreateHold(CreateHoldRequest{
		OperationID: "op-hold-2", Actor: Actor{ID: "test", Rank: "general"},
		ID: "hold-2", Scope: DispatchHoldScope{TaskIDs: []string{"other"}},
		Actions: []DispatchAction{DispatchActionStart}, Reason: "other",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(StartRequest{
		OperationID: "op-start", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t2", ExpectedGeneration: 1, Reason: "go",
	}); err != nil {
		t.Fatalf("unrelated hold blocked start: %v", err)
	}
}

func TestAuthorityReleaseHoldIsIdempotent(t *testing.T) {
	a := newTestAuthority(t)
	if _, err := a.CreateHold(CreateHoldRequest{
		OperationID: "op-hold", Actor: Actor{ID: "test", Rank: "general"},
		ID: "hold-1", Scope: DispatchHoldScope{ProjectIDs: []string{"proj"}},
		Actions: []DispatchAction{DispatchActionSpawn}, Reason: "review",
	}); err != nil {
		t.Fatal(err)
	}
	for _, opID := range []string{"op-release", "op-release-2"} {
		res, err := a.ReleaseHold(ReleaseHoldRequest{
			OperationID: opID, Actor: Actor{ID: "test", Rank: "general"},
			ID: "hold-1", Reason: "approved",
		})
		if err != nil {
			t.Fatalf("release %s: %v", opID, err)
		}
		_ = res
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	hold, ok := v.Hold("hold-1")
	if !ok || hold.ReleasedAt == 0 {
		t.Fatalf("hold not released: %+v", hold)
	}

	// Releasing a missing hold is a typed error.
	if _, err := a.ReleaseHold(ReleaseHoldRequest{
		OperationID: "op-release-missing", Actor: Actor{ID: "test", Rank: "general"},
		ID: "nope", Reason: "x",
	}); !errors.Is(err, ErrHoldNotFound) {
		t.Fatalf("release missing = %v, want ErrHoldNotFound", err)
	}
}

// TestStartCannotRaceConcurrentHold is the deterministic barrier proof: a hold
// writer cannot commit inside the Start check-commit span. The only valid
// outcomes are Start-first-then-hold, or hold-first-then-blocked-Start.
func TestStartCannotRaceConcurrentHold(t *testing.T) {
	store := newMemStore()
	a := New(store)
	createTask(t, a, "t1")

	started := make(chan struct{})
	proceed := make(chan struct{})
	var signalOnce sync.Once
	store.beforeCommit = func() error {
		signalOnce.Do(func() { close(started) })
		<-proceed
		return nil
	}

	startDone := make(chan error, 1)
	go func() {
		_, err := a.Start(StartRequest{
			OperationID: "op-start", Actor: Actor{ID: "test", Rank: "general"},
			TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
		})
		startDone <- err
	}()

	<-started // Start passed its hold check and is inside the check-commit span.

	holdDone := make(chan error, 1)
	go func() {
		_, err := a.CreateHold(CreateHoldRequest{
			OperationID: "op-hold", Actor: Actor{ID: "test", Rank: "general"},
			ID: "hold-1", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}},
			Actions: []DispatchAction{DispatchActionStart}, Reason: "review",
		})
		holdDone <- err
	}()

	select {
	case err := <-holdDone:
		t.Fatalf("hold committed inside the Start check-commit span: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(proceed)
	if err := <-startDone; err != nil {
		t.Fatalf("start failed: %v", err)
	}
	select {
	case err := <-holdDone:
		if err != nil {
			t.Fatalf("hold after start failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hold did not commit after start released the transaction")
	}

	// Start won: the task is working, and the hold exists but gates nothing.
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseWorking {
		t.Fatalf("phase = %s, want working", agg.Phase)
	}
	v, err := store.View()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Hold("hold-1"); !ok {
		t.Fatal("hold missing after start-first race")
	}
}

// TestHoldFirstBlocksConcurrentStart is the inverse ordering: when the hold
// commits first, a concurrent Start must observe it and fail closed.
func TestHoldFirstBlocksConcurrentStart(t *testing.T) {
	store := newMemStore()
	a := New(store)
	createTask(t, a, "t1")

	holdStarted := make(chan struct{})
	startBlocked := make(chan struct{})
	var signalOnce sync.Once
	store.beforeCommit = func() error {
		signalOnce.Do(func() { close(holdStarted) })
		<-startBlocked
		return nil
	}

	holdDone := make(chan error, 1)
	go func() {
		_, err := a.CreateHold(CreateHoldRequest{
			OperationID: "op-hold", Actor: Actor{ID: "test", Rank: "general"},
			ID: "hold-1", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}},
			Actions: []DispatchAction{DispatchActionStart}, Reason: "review",
		})
		holdDone <- err
	}()

	<-holdStarted // hold is inside its transaction.

	startDone := make(chan error, 1)
	go func() {
		_, err := a.Start(StartRequest{
			OperationID: "op-start", Actor: Actor{ID: "test", Rank: "general"},
			TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
		})
		startDone <- err
	}()

	select {
	case err := <-startDone:
		t.Fatalf("start committed before the hold transaction finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(startBlocked)
	if err := <-holdDone; err != nil {
		t.Fatalf("hold failed: %v", err)
	}
	if err := <-startDone; !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("start after hold = %v, want ErrDispatchHeld", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseQueued {
		t.Fatalf("phase = %s, want queued (hold-first outcome)", agg.Phase)
	}
}
