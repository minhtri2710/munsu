package fleet_test

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestFleetSnapshotEmpty(t *testing.T) {
	home := testutil.TempHome(t)

	snap, err := fleet.Snapshot(home)
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	if snap == nil {
		t.Fatal("expected non-nil fleet snapshot")
	}

	if len(snap.Tasks) != 0 {
		t.Errorf("expected 0 tasks in empty home, got %d", len(snap.Tasks))
	}
}

func TestPhaseFromProjection(t *testing.T) {
	ts := fleet.TaskSnapshot{
		ID:         "task-1",
		Kind:       "ship",
		LastStatus: "working: implementation",
	}

	phase := fleet.PhaseFromProjection(ts)
	if phase == "" {
		t.Error("expected non-empty phase from projection")
	}
}
