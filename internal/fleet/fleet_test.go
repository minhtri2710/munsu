package fleet_test

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
)

func TestFleetSnapshotEmpty(t *testing.T) {
	// Snapshot reads canonical Task Authority state, which requires an
	// initialized canonical v1 home.
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatalf("home.Init: %v", err)
	}

	snap, err := fleet.Snapshot(homeDir, fleet.SnapshotDependencies{CurrentState: fleet.NewCanonicalCurrentState()})
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
