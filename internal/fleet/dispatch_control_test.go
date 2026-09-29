package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestSpawnFailsClosedOnDegradedSupervision proves the supervision gate fires
// before authority or store calls and leaves the queued task untouched.
func TestSpawnFailsClosedOnDegradedSupervision(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustFleetCreate(t, c, "task")

	// Unhealthy watcher lease: dead PID with a lease file present.
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	mhome.ClaimWatcherLease(homeDir, 9999999)

	r := NewRunner(Args{ID: "task", ProjectName: "project", HomeDir: homeDir, Authority: c})
	if _, err := r.Run(); !errors.Is(err, mhome.ErrUnhealthyWatcher) {
		t.Fatalf("Run err = %v, want ErrUnhealthyWatcher", err)
	}

	// No Authority call happened: the task is still the seeded queued
	// generation and no Dispatch Hold exists.
	taskID := mustFleetTaskID(t, "task")
	agg, err := c.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseQueued || agg.Revision != taskauthority.FirstRevision {
		t.Fatalf("aggregate after failed spawn = %+v, want untouched queued seed", agg)
	}
	holds, err := c.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) > 0 {
		t.Fatalf("degraded supervision created Dispatch Holds: %v", holds)
	}
}

// TestHandoffFailsClosedOnDegradedSupervision proves the handoff supervision
// gate fires before the handoff saga begins: an unhealthy watcher lease on
// either home fails Handoff closed with ErrUnhealthyWatcher and leaves no
// handoff journal behind.
func TestHandoffFailsClosedOnDegradedSupervision(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captains", "test-sm")
	if err := os.MkdirAll(captain, 0755); err != nil {
		t.Fatal(err)
	}
	if err := mhome.SeedCaptainProvenance(captain, "test-sm"); err != nil {
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
