package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// Actionable: payloads that need the Human.
	if err := EnqueueWake(home, "signal", "task-1", "failed: CI red"); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueWake(home, "signal", "task-2", "blocked: missing auth"); err != nil {
		t.Fatal(err)
	}
	// Routine: not general-relevant, including completion lines (audit only).
	if err := EnqueueWake(home, "stale", "task-3", "still working on tests"); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueWake(home, "signal", "task-4", "done: PR merged"); err != nil {
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
	if report.RoutineCount != 2 {
		t.Errorf("RoutineCount = %d, want 2", report.RoutineCount)
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
	if err := EnqueueWake(home, "signal", "task-1", "needs-decision: pick a branch"); err != nil {
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
	// An in-flight task with a window, as the fleet snapshot reports it.
	provider := func(string) ([]FleetTaskSnapshot, error) {
		return []FleetTaskSnapshot{{ID: "task-ship", Kind: "ship", Window: "@9999"}}, nil
	}

	report, err := DrainCycle(DrainCycleOptions{
		HomeDir:       home,
		Consumer:      "general",
		PeekFleet:     true,
		FleetSnapshot: provider,
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
		t.Errorf("Alive = %d, want 1 (task has window, CurrentState reports alive)", report.FleetPeek.Alive)
	}
	// HasActionable is wake-gated, not fleet-gated.
	if report.HasActionable() {
		t.Error("HasActionable() = true without wakes, want false")
	}
}

func TestDrainCycle_PeekFleetCountsReviewSoldiersOnly(t *testing.T) {
	provider := func(string) ([]FleetTaskSnapshot, error) {
		return []FleetTaskSnapshot{
			{ID: "task-review", Kind: "review", Window: "@9999"},
			{ID: "task-other", Kind: "captain", Window: "@9999"},
		}, nil
	}
	report, err := DrainCycle(DrainCycleOptions{HomeDir: t.TempDir(), Consumer: "general", PeekFleet: true, FleetSnapshot: provider})
	if err != nil {
		t.Fatalf("DrainCycle: %v", err)
	}
	if report.FleetPeek == nil || report.FleetPeek.InFlight != 1 {
		t.Fatalf("FleetPeek = %+v, want exactly the review soldier in flight", report.FleetPeek)
	}
}

// TestDrainCycle_PeekFleetNeedsSnapshotProvider pins that the peek reads the
// fleet only through the snapshot provider: without one it reports the failure
// as guidance and does not scan .meta files.
func TestDrainCycle_PeekFleetNeedsSnapshotProvider(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "task-1.meta"), []byte("window = @9999\n"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := DrainCycle(DrainCycleOptions{HomeDir: home, Consumer: "general", PeekFleet: true})
	if err != nil {
		t.Fatalf("DrainCycle: %v", err)
	}
	if report.FleetPeek != nil {
		t.Fatalf("FleetPeek = %+v, want none without a provider", report.FleetPeek)
	}
	if len(report.Guidance) == 0 || !strings.Contains(report.Guidance[0], "no fleet snapshot provider") {
		t.Fatalf("Guidance = %v, want the provider-missing failure", report.Guidance)
	}
}
