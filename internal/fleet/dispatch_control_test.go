package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
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

// TestSpawnFailsClosedOnDegradedSupervision proves the spawn supervision
// gate (Task 4.3) fires before any Authority or Store call: an unhealthy
// watcher lease fails the Runner closed with ErrUnhealthyWatcher, leaves the
// task phase untouched, and creates no Dispatch Hold or journal state.
func TestSpawnFailsClosedOnDegradedSupervision(t *testing.T) {
	homeDir := t.TempDir()
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	auth := taskauthority.New(store)
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "seed",
		Actor:       taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:      "task",
		Owner:       "owner",
		Description: "work",
		Kind:        "ship",
		Project:     "project",
		Reason:      "seed",
	}); err != nil {
		t.Fatal(err)
	}

	// Unhealthy watcher lease: dead PID with a lease file present.
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	mhome.ClaimWatcherLease(homeDir, 9999999)

	r := NewRunner(Args{ID: "task", ProjectName: "project", HomeDir: homeDir, Authority: auth})
	if _, err := r.Run(); !errors.Is(err, mhome.ErrUnhealthyWatcher) {
		t.Fatalf("Run err = %v, want ErrUnhealthyWatcher", err)
	}

	// No Authority call happened: the task is still the seeded queued
	// generation and no Dispatch Hold exists.
	agg, err := auth.Get("task")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseQueued || agg.Revision != taskauthority.FirstRevision {
		t.Fatalf("aggregate after failed spawn = %+v, want untouched queued seed", agg)
	}
	holdsDir := filepath.Join(homeDir, "state", ".dispatch", "holds")
	if entries, err := os.ReadDir(holdsDir); err == nil && len(entries) > 0 {
		t.Fatalf("degraded supervision created Dispatch Holds: %v", entries)
	}
}

// TestHandoffFailsClosedOnDegradedSupervision proves the handoff supervision
// gate (Task 4.3) fires before the handoff saga begins: an unhealthy watcher
// lease on either home fails Handoff closed with ErrUnhealthyWatcher and
// leaves no handoff journal behind.
func TestHandoffFailsClosedOnDegradedSupervision(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(captain, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captain, "test-sm"); err != nil {
		t.Fatal(err)
	}

	// Unhealthy watcher lease on both homes.
	for _, homeDir := range []string{parent, captain} {
		if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
			t.Fatal(err)
		}
		mhome.ClaimWatcherLease(homeDir, 9999999)
	}

	if err := Handoff(parent, captain, []string{"TASK-1"}); !errors.Is(err, mhome.ErrUnhealthyWatcher) {
		t.Fatalf("Handoff err = %v, want ErrUnhealthyWatcher", err)
	}

	// No handoff journal was staged: the gate fired before the saga began.
	if _, err := os.Stat(filepath.Join(parent, "state", taskHandoffDirName)); !os.IsNotExist(err) {
		t.Error("degraded supervision left handoff journal state behind")
	}
	if _, err := os.Stat(filepath.Join(captain, "state", taskHandoffDirName)); !os.IsNotExist(err) {
		t.Error("degraded supervision left destination handoff journal state behind")
	}
}
