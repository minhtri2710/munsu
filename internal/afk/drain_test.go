package afk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/lifecycle"
)

func TestDrainCycle_NoConsumer(t *testing.T) {
	_, err := DrainCycle(DrainCycleOptions{HomeDir: t.TempDir()})
	if err == nil {
		t.Fatal("DrainCycle without consumer: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "consumer") {
		t.Errorf("expected consumer error, got: %v", err)
	}
}

func TestDrainCycle_EmptyQueue(t *testing.T) {
	home := t.TempDir()
	report, err := DrainCycle(DrainCycleOptions{
		HomeDir:   home,
		Consumer:  "general",
		PeekFleet: true,
	})
	if err != nil {
		t.Fatalf("DrainCycle on empty queue: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if len(report.Actionable) != 0 {
		t.Errorf("Actionable = %d, want 0", len(report.Actionable))
	}
	if report.RoutineCount != 0 {
		t.Errorf("RoutineCount = %d, want 0", report.RoutineCount)
	}
	if report.HasActionable() {
		t.Error("HasActionable() = true on empty, want false")
	}
}

func TestDrainCycle_ClassifiesActionable(t *testing.T) {
	home := t.TempDir()
	// Actionable: general-relevant payloads.
	if err := lifecycle.EnqueueWake(home, "signal", "task-1", "done: PR merged"); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.EnqueueWake(home, "signal", "task-2", "blocked: missing auth"); err != nil {
		t.Fatal(err)
	}
	// Routine: not general-relevant.
	if err := lifecycle.EnqueueWake(home, "stale", "task-3", "still working on tests"); err != nil {
		t.Fatal(err)
	}

	report, err := DrainCycle(DrainCycleOptions{
		HomeDir:   home,
		Consumer:  "general",
		Limit:     10,
		PeekFleet: false,
	})
	if err != nil {
		t.Fatalf("DrainCycle: %v", err)
	}
	if len(report.Actionable) != 2 {
		t.Fatalf("Actionable = %d, want 2: %+v", len(report.Actionable), report.Actionable)
	}
	if report.RoutineCount != 1 {
		t.Errorf("RoutineCount = %d, want 1", report.RoutineCount)
	}
	if !report.HasActionable() {
		t.Error("HasActionable() = false, want true")
	}
	if report.LeaseID == "" {
		t.Error("LeaseID empty")
	}
}

func TestDrainCycle_DrainsQueue(t *testing.T) {
	home := t.TempDir()
	if err := lifecycle.EnqueueWake(home, "signal", "task-1", "done: shipped"); err != nil {
		t.Fatal(err)
	}

	report, err := DrainCycle(DrainCycleOptions{
		HomeDir:  home,
		Consumer: "general",
	})
	if err != nil {
		t.Fatalf("DrainCycle: %v", err)
	}
	if len(report.Actionable) != 1 {
		t.Fatalf("Actionable = %d, want 1", len(report.Actionable))
	}
	if report.LeaseID == "" {
		t.Error("LeaseID empty after drain")
	}

	// A second drain on the now-empty queue (wakes are leased, not re-queued)
	// should yield nothing actionable.
	report2, err := DrainCycle(DrainCycleOptions{
		HomeDir:  home,
		Consumer: "general",
	})
	if err != nil {
		t.Fatalf("second DrainCycle: %v", err)
	}
	if report2.HasActionable() {
		t.Errorf("second drain HasActionable = true, want false: %+v", report2)
	}
}

func TestDrainCycle_PeekFleet(t *testing.T) {
	home := t.TempDir()
	// Create an in-flight task. Snapshot assumes pane alive when window is set
	// unless a liveness probe is wired — so Dead will be 0 here.
	meta := []byte("id = task-ship\nproject = demo\nkind = ship\nwindow = @9999\n")
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "task-ship.meta"), meta, 0644); err != nil {
		t.Fatal(err)
	}

	report, err := DrainCycle(DrainCycleOptions{
		HomeDir:   home,
		Consumer:  "general",
		PeekFleet: true,
	})
	if err != nil {
		t.Fatalf("DrainCycle: %v", err)
	}
	if report.FleetPeek == nil {
		t.Fatal("FleetPeek nil, want populated")
	}
	if report.FleetPeek.InFlight != 1 {
		t.Errorf("InFlight = %d, want 1", report.FleetPeek.InFlight)
	}
	if report.FleetPeek.Alive != 1 {
		t.Errorf("Alive = %d, want 1 (Snapshot assumes alive with window)", report.FleetPeek.Alive)
	}
	// HasActionable is wake-gated, not fleet-gated.
	if report.HasActionable() {
		t.Error("HasActionable() = true without wakes, want false")
	}
}
