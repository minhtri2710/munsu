package fleet

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
)

// TestStartTaskRespectsDispatchHoldWithoutChangingQueuedState proves the
// Authority Start operation evaluates durable Dispatch Holds inside the same
// Store transaction: a matching hold leaves the queued phase unchanged.
func TestStartTaskRespectsDispatchHoldWithoutChangingQueuedState(t *testing.T) {
	homeDir := t.TempDir()
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	auth := taskauthority.New(store)
	actor := taskauthority.Actor{ID: "owner", Rank: "general"}
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "seed",
		Actor:       actor,
		TaskID:      "task",
		Owner:       "owner",
		Description: "work",
		Kind:        "ship",
		Project:     "project",
		Reason:      "seed",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreateHold(taskauthority.CreateHoldRequest{
		OperationID: "hold-seed",
		Actor:       actor,
		ID:          "start-pause",
		Scope:       taskauthority.DispatchHoldScope{TaskIDs: []string{"task"}},
		Actions:     []taskauthority.DispatchAction{taskauthority.DispatchActionStart},
		Reason:      "pause",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Start(taskauthority.StartRequest{
		OperationID:        "start-1",
		Actor:              actor,
		TaskID:             "task",
		ExpectedGeneration: 1,
		Reason:             "start",
	}); !errors.Is(err, taskauthority.ErrDispatchHeld) {
		t.Fatalf("start err = %v, want dispatch hold", err)
	}
	agg, err := auth.Get("task")
	if err != nil || agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("current = %+v err=%v, want queued", agg, err)
	}
}
