package taskauthority

import (
	"errors"
	"testing"
)

func TestAuthorityCreateStartBlockUnblockComplete(t *testing.T) {
	a := newTestAuthority(t)
	res := createTask(t, a, "t1")
	if res.Generation != 1 || res.Revision != FirstRevision || res.Phase != PhaseQueued {
		t.Fatalf("create result = %+v", res)
	}

	started, err := a.Start(StartRequest{
		OperationID: "op-start", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Phase != PhaseWorking || started.Revision != 2 {
		t.Fatalf("start result = %+v", started)
	}

	if _, err := a.Block(BlockRequest{
		OperationID: "op-block", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Detail: "dep", Reason: "dep",
	}); err != nil {
		t.Fatal(err)
	}
	unblocked, err := a.Unblock(UnblockRequest{
		OperationID: "op-unblock", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "free",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unblocked.Phase != PhaseQueued {
		t.Fatalf("unblock result = %+v", unblocked)
	}

	done, err := a.Complete(CompleteRequest{
		OperationID: "op-done", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseDone, Reason: "done",
	})
	if err != nil {
		t.Fatal(err)
	}
	if done.Phase != PhaseDone {
		t.Fatalf("complete result = %+v", done)
	}

	// Revision advanced once per committed mutation: create(1), start(2),
	// block(3), unblock(4), complete(5).
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 5 {
		t.Fatalf("revision = %d, want 5", agg.Revision)
	}
}

func TestAuthorityInvalidTransitionsAreTypedConflicts(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	// Start requires queued.
	if _, err := a.Block(BlockRequest{
		OperationID: "op-block", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Detail: "d", Reason: "d",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(StartRequest{
		OperationID: "op-start", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("start on blocked task = %v, want ErrPrecondition", err)
	}

	// Unblock requires blocked.
	createTask(t, a, "t2")
	if _, err := a.Unblock(UnblockRequest{
		OperationID: "op-unblock", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t2", ExpectedGeneration: 1, Reason: "free",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("unblock on non-blocked task = %v, want ErrPrecondition", err)
	}

	// Generation fence.
	if _, err := a.Start(StartRequest{
		OperationID: "op-start-2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 7, Reason: "go",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation start = %v, want ErrConflict", err)
	}

	// No mutation occurred on failed transitions.
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseBlocked || agg.Revision != 2 {
		t.Fatalf("aggregate after failed transitions = %+v", agg)
	}
}

func TestAuthorityReopenCreatesNextGeneration(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-done", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseResolved, Reason: "resolved",
	}); err != nil {
		t.Fatal(err)
	}

	reopened, err := a.Reopen(ReopenRequest{
		OperationID: "op-reopen", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "redo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Reopened || reopened.Generation != 2 || reopened.Revision != FirstRevision || reopened.Phase != PhaseQueued {
		t.Fatalf("reopen result = %+v", reopened)
	}

	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	historical, ok := v.Aggregate("t1", 1)
	if !ok || historical.Current {
		t.Fatalf("prior generation must remain as historical: %+v", historical)
	}
	if historical.Phase != PhaseResolved {
		t.Fatalf("prior generation phase changed: %+v", historical)
	}
	cur, ok := v.Current("t1")
	if !ok || cur.Generation != 2 {
		t.Fatalf("current = %+v", cur)
	}

	// Reopen requires terminal.
	if _, err := a.Reopen(ReopenRequest{
		OperationID: "op-reopen-2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 2, Reason: "redo",
	}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("reopen non-terminal = %v, want ErrPrecondition", err)
	}

	// Stale generation fence on reopen.
	if _, err := a.Reopen(ReopenRequest{
		OperationID: "op-reopen-3", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "redo",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reopen = %v, want ErrConflict", err)
	}
}

func TestAuthorityCreateIsIdempotentAndConflictsOnExisting(t *testing.T) {
	a := newTestAuthority(t)
	first := createTask(t, a, "t1")

	// Same operation replayed: same result, no new record.
	replayed, err := a.Create(CreateRequest{
		OperationID: "op-create-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", Owner: "owner", Description: "work", Kind: "ship", Project: "proj",
		Reason: "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed != first {
		t.Fatalf("replayed create = %+v, want %+v", replayed, first)
	}

	// Different operation ID, existing task: conflict, no projection duplicate.
	_, err = a.Create(CreateRequest{
		OperationID: "op-create-t1-bis", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", Owner: "owner", Description: "work", Kind: "ship", Project: "proj",
		Reason: "create",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("create existing = %v, want ErrConflict", err)
	}
	list, err := a.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %d tasks, want 1", len(list))
	}
}

func TestAuthorityOperationIDReusedWithDifferentIntentConflicts(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	if _, err := a.Block(BlockRequest{
		OperationID: "op-reused", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Detail: "d", Reason: "d",
	}); err != nil {
		t.Fatal(err)
	}
	// Same operation ID, different intent (start instead of block): conflict.
	if _, err := a.Start(StartRequest{
		OperationID: "op-reused", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id = %v, want ErrOperationConflict", err)
	}
}

func TestAuthorityLifecycleEmitsTypedAuditEvents(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.Start(StartRequest{
		OperationID: "op-start", Actor: Actor{ID: "general-1", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	}); err != nil {
		t.Fatal(err)
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Audit) != 2 {
		t.Fatalf("audit = %d events, want 2 (create + start)", len(v.Audit))
	}
	start := v.Audit[1]
	if start.Kind != AuditLifecycle || start.OperationID != "op-start" || start.TaskID != "t1" || start.Generation != 1 {
		t.Fatalf("start audit = %+v", start)
	}
	if start.Actor.ID != "general-1" || start.Before != PhaseQueued || start.After != PhaseWorking {
		t.Fatalf("start audit phases = %+v", start)
	}
}
