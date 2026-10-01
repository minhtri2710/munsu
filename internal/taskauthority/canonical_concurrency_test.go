package taskauthority

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
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

// runAddHoldRace pauses AddHold after it has read the new hold and before its
// commit. On the unfixed implementation a hold-checked mutation can then
// commit independently; with the dispatch scope it cannot pass its hold check
// until AddHold has committed.
func runAddHoldRace(t *testing.T, action DispatchAction, mutationName string, mutate func(*Canonical) error) {
	t.Helper()
	cAdd, _, root := newTestCanonical(t)
	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	cMutate, err := NewCanonical(h2)
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	mustCreate(t, cAdd, "t1")

	prepared := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	cAdd.now = func() time.Time {
		close(prepared)
		<-release
		return time.Now()
	}

	hold := CanonicalAddHoldRequest{
		HomeID:  cAdd.HomeID(),
		HoldID:  "hold-race-" + mutationName,
		Scope:   DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{action},
		Reason:  "race barrier",
	}
	addResult := make(chan error, 1)
	go func() {
		_, err := cAdd.AddHold(mustOperation(t, "op-add-"+mutationName, hold), hold)
		addResult <- err
	}()
	<-prepared

	mutationResult := make(chan error, 1)
	go func() {
		mutationResult <- mutate(cMutate)
	}()

	var earlyErr error
	var early bool
	select {
	case earlyErr = <-mutationResult:
		early = true
	case <-time.After(250 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-addResult; err != nil {
		t.Fatalf("AddHold: %v", err)
	}
	if early {
		if earlyErr == nil {
			t.Fatalf("%s committed before AddHold returned", mutationName)
		}
		t.Fatalf("%s returned before AddHold committed: %v", mutationName, earlyErr)
	}
	if err := <-mutationResult; !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("%s after AddHold = %v, want ErrDispatchHeld", mutationName, err)
	}
}

func TestCanonicalAddHoldSerializesWithStartAcrossCanonicalInstances(t *testing.T) {
	id := mustTaskID(t, "t1")
	req := CanonicalStartRequest{
		TaskID:       id,
		Precondition: preconditionOf(1, 1),
		Reason:       "start",
	}
	runAddHoldRace(t, DispatchActionStart, "start", func(c *Canonical) error {
		req.HomeID = c.HomeID()
		op := mustOperation(t, "op-mutate-start", req)
		_, err := c.Start(op, req)
		return err
	})
}

func TestCanonicalAddHoldSerializesWithBeginSpawnAcrossCanonicalInstances(t *testing.T) {
	id := mustTaskID(t, "t1")
	req := launchRequestForConcurrency(id)
	runAddHoldRace(t, DispatchActionSpawn, "begin-spawn", func(c *Canonical) error {
		req.HomeID = c.HomeID()
		op := mustOperation(t, "op-mutate-begin-spawn", req)
		_, err := c.BeginSpawn(op, req)
		return err
	})
}

func launchRequestForConcurrency(taskID domain.TaskID) CanonicalBeginSpawnRequest {
	return CanonicalBeginSpawnRequest{
		TaskID:                taskID,
		Precondition:          preconditionOf(1, 1),
		SnapshotDigest:        digestOf("concurrency-snapshot"),
		Backend:               "claude",
		Harness:               "pi",
		Model:                 "opus",
		Effort:                "high",
		Mode:                  "direct-PR",
		Kind:                  "ship",
		Project:               "proj",
		ParentTaskID:          "parent",
		LaunchID:              "launch-concurrency",
		WindowLabel:           "window-concurrency",
		WorktreeReservationID: "wt-res-concurrency",
		WorktreeFenceToken:    "wt-fence-concurrency",
		EndpointReservationID: "ep-res-concurrency",
		EndpointFenceToken:    "ep-fence-concurrency",
		EndpointIncarnation:   "inc-concurrency",
		Reason:                "spawn",
	}
}
