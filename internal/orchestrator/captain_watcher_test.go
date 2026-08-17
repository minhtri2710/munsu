package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

type captainTestProbe struct{}

func (captainTestProbe) Probe(string, map[string]string) (bool, error) { return false, nil }

type captainTestSender struct{}

func (captainTestSender) Alive(string, map[string]string) (bool, error) { return false, nil }
func (captainTestSender) Send(string, map[string]string, string) BoundSendResult {
	return BoundSendResult{}
}

func captainRunCycle(home string) (bool, error) {
	return RunCycleWithProbeAndSender(home, captainTestProbe{}, captainTestSender{}, NewCaptainWatcherHooks(&captainNotificationTransport{acknowledged: true}, nil), NoopRetirementPort{}, NoopTaskStatePort{})
}

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
	id := NewIdentity(tmp)
	WriteIdentity(tmp, id)

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
	old := time.Now().Add(-2 * StaleThreshold())
	beatPath := mhome.WatcherBeatPath(tmp)
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
	if err := mhome.WriteMeta(tmp, "soldier-1", soldierMeta); err != nil {
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
	id := NewIdentity(tmp)
	WriteIdentity(tmp, id)
	WriteBeat(tmp)

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
