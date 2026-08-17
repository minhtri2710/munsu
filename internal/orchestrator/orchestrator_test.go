package orchestrator_test

import (
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestDurableWakeQueue(t *testing.T) {
	home := testutil.TempHome(t)

	if orchestrator.HasQueuedWakes(home) {
		t.Error("expected no queued wakes initially")
	}

	if err := orchestrator.EnqueueWake(home, "stale", "task-1", "idle 5m"); err != nil {
		t.Fatalf("EnqueueWake failed: %v", err)
	}

	if !orchestrator.HasQueuedWakes(home) {
		t.Error("expected queued wakes after enqueue")
	}

	records, err := orchestrator.DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Kind != "stale" || records[0].Key != "task-1" {
		t.Errorf("wake record mismatch: %v", records[0])
	}

	if orchestrator.HasQueuedWakes(home) {
		t.Error("expected queue to be empty after drain")
	}
}

func TestAFKStatusAndDisable(t *testing.T) {
	home := testutil.TempHome(t)

	if orchestrator.IsActive(home) {
		t.Error("expected AFK to be inactive initially")
	}

	if err := orchestrator.Disable(home); err != nil {
		t.Errorf("Disable on inactive should succeed, got: %v", err)
	}
}
