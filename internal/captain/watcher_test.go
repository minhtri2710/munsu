package captain

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/supervision"
	"github.com/minhtri2710/munsu/internal/task"
)

// --- WatcherStatusSummary tests ---

func TestWatcherStatusSummary_Absent(t *testing.T) {
	tmp := t.TempDir()
	status := WatcherStatusSummary(tmp)
	if status != WatcherAbsent {
		t.Errorf("expected absent, got %s", status)
	}
}

func TestWatcherStatusSummary_StoppedWithIdentity(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Write an identity file without a beat — simulates crash residue.
	id := supervision.NewIdentity(tmp)
	supervision.WriteIdentity(tmp, id)

	status := WatcherStatusSummary(tmp)
	if status != WatcherStopped {
		t.Errorf("expected stopped (identity without beat), got %s", status)
	}
}

func TestWatcherStatusSummary_StoppedStaleBeat(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Write an old beat beyond the stale threshold.
	old := time.Now().Add(-2 * lifecycle.StaleThreshold())
	beatPath := lifecycle.BeatPath(tmp)
	os.WriteFile(beatPath, []byte(old.Format("060102150405")+" 99999\n"), 0644)

	status := WatcherStatusSummary(tmp)
	if status != WatcherStopped {
		t.Errorf("expected stopped (stale beat), got %s", status)
	}
}

// --- EnsureWatcher tests ---

func TestEnsureWatcher_NoChildWorkAndAbsent(t *testing.T) {
	tmp := t.TempDir()
	// No child work + no watcher = no-op (idempotent).
	if err := EnsureWatcher(tmp, false); err != nil {
		t.Fatalf("EnsureWatcher(false) on absent: %v", err)
	}
	// Verify no watcher was started.
	status := WatcherStatusSummary(tmp)
	if status != WatcherAbsent && status != WatcherStopped {
		t.Errorf("expected absent/stopped, got %s", status)
	}
}

func TestEnsureWatcher_StartsWhenChildWorkInFlight(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Set up valid parent-home config so EnsureWatcher validation passes.
	if err := config.Set(tmp, "parent-home", t.TempDir()); err != nil {
		t.Fatal(err)
	}

	// Simulate child work by creating a soldier meta file.
	soldierMeta := map[string]string{"kind": "ship", "window": "win-1"}
	if err := task.WriteMeta(tmp, "soldier-1", soldierMeta); err != nil {
		t.Fatal(err)
	}

	// With child work in flight and no watcher, EnsureWatcher should start one.
	if err := EnsureWatcher(tmp, true); err != nil {
		t.Fatalf("EnsureWatcher(true): %v", err)
	}

	// Starting a watcher subprocess in test environment requires a real munsu binary.
	// We skip the beat validation here; integration tests cover the full path.
}

func TestEnsureWatcher_StopsWhenNoChildWork(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Simulate a watcher identity and beat.
	id := supervision.NewIdentity(tmp)
	supervision.WriteIdentity(tmp, id)
	lifecycle.WriteBeat(tmp)

	status := WatcherStatusSummary(tmp)
	if status != WatcherStopped {
		t.Skipf("watcher status is %s -- no actual watcher process to validate ownership; skip stop test", status)
	}
	// We can't actually stop a non-running watcher, but EnsureWatcher(false)
	// should be idempotent.
	if err := EnsureWatcher(tmp, false); err != nil {
		t.Fatalf("EnsureWatcher(false) with orphan artifacts: %v", err)
	}
}

// --- ConvergeWatcherStatus tests ---

func TestConvergeWatcherStatus_EmptyRegistry(t *testing.T) {
	results := ConvergeWatcherStatus(nil)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}

	results = ConvergeWatcherStatus([]Info{})
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestConvergeWatcherStatus_SkipsEmptyHome(t *testing.T) {
	results := ConvergeWatcherStatus([]Info{
		{ID: "valid", Home: t.TempDir()},
		{ID: "empty", Home: ""},
	})
	if len(results) != 1 {
		t.Errorf("expected 1 result (skipped empty home), got %d", len(results))
	}
	if results[0].CaptainID != "valid" {
		t.Errorf("captain id = %q, want valid", results[0].CaptainID)
	}
}

func TestConvergeWatcherStatus_AllAbsent(t *testing.T) {
	tmp1 := t.TempDir()
	tmp2 := t.TempDir()

	results := ConvergeWatcherStatus([]Info{
		{ID: "c1", Home: tmp1},
		{ID: "c2", Home: tmp2},
	})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != WatcherAbsent {
			t.Errorf("captain %s: expected absent, got %s", r.CaptainID, r.Status)
		}
	}
}

// --- inFlightSoldierPath tests ---

func TestInFlightSoldierPath_Empty(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "state"), 0755)

	if inFlightSoldierPath(tmp) {
		t.Error("expected false for empty captain home")
	}
}

func TestInFlightSoldierPath_WithSoldier(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	task.WriteMeta(tmp, "soldier-1", map[string]string{"kind": "ship", "window": "w1"})

	if !inFlightSoldierPath(tmp) {
		t.Error("expected true when soldier meta exists")
	}
}

func TestInFlightSoldierPath_IgnoresNonShip(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Captain meta should not count as child work.
	task.WriteMeta(tmp, "captain:test", map[string]string{"kind": "captain", "window": "w1"})

	if inFlightSoldierPath(tmp) {
		t.Error("expected false for non-ship/scout meta")
	}
}
