package taskauthority

import (
	"sync"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// TestCanonicalIndependentTasksConcurrentProvesNoGlobalLock runs independent
// mutations on two different tasks through the same Canonical concurrently.
// The canonical surface locks the smallest scoped lock per aggregate (home
// task scope), never a global runtime lock, so both tasks must complete their
// mutations without blocking each other.
func TestCanonicalIndependentTasksConcurrentProvesNoGlobalLock(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustCreate(t, c, "t2")

	var wg sync.WaitGroup
	errs := make(chan error, 2)

	for _, taskID := range []string{"t1", "t2"} {
		wg.Add(1)
		go func(taskID string) {
			defer wg.Done()
			id, _ := domain.NewTaskID(taskID)
			req := CanonicalStartRequest{
				HomeID:       c.HomeID(),
				TaskID:       id,
				Precondition: preconditionOf(1, 1),
				Reason:       "start",
			}
			if _, err := c.Start(mustOperationForWorker(t, taskID, req), req); err != nil {
				errs <- err
			}
		}(taskID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent start failed: %v", err)
	}

	// Both tasks must have advanced to working at revision 2 independently.
	for _, taskID := range []string{"t1", "t2"} {
		agg, err := c.Get(mustTaskID(t, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if agg.Phase != PhaseWorking || agg.Revision != 2 {
			t.Fatalf("task %s after concurrent start = phase %s rev %d, want working/2", taskID, agg.Phase, agg.Revision)
		}
	}
}

// mustOperationForWorker builds a named operation for a per-task goroutine.
func mustOperationForWorker(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID("op-concurrent-" + id)
	if err != nil {
		t.Fatalf("NewOperationID(%s): %v", id, err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		t.Fatalf("NewOperation(%s): %v", id, err)
	}
	return op
}