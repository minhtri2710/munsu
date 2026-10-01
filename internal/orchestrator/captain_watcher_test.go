package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	mhome "github.com/minhtri2710/munsu/internal/home"
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
	child := armWatcherChild(t)
	if err := EnsureWatcher(tmp, true); err != nil {
		t.Fatalf("EnsureWatcher(true): %v", err)
	}

	// The started process is this test binary re-exec'd, running in tmp. Reap it
	// before the TempDir cleanup removes the directory it was launched in; that
	// removal fails while a process is still there. Beat validation stays out of
	// scope here -- integration tests cover the full path with a real binary.
	child.reap(t)
}

func TestEnsureWatcher_StopsWhenNoChildWork(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// A real watcher child held alive, published as the running watcher: its
	// identity, a fresh beat and the lease all name its PID.
	t.Setenv(watcherChildHoldEnv, "1m")
	child := armWatcherChild(t)
	if err := EnsureWatcher(tmp, true); err != nil {
		t.Fatalf("EnsureWatcher(true): %v", err)
	}
	pid, ok := readWatcherChildPID(child.pidPath, child.waits.pid)
	if !ok {
		t.Fatalf("watcher child never recorded a PID at %s", child.pidPath)
	}
	id := NewIdentity(tmp)
	executable, processStart, err := mhome.ProcessIdentity(pid)
	if err != nil {
		t.Fatalf("reading watcher child identity: %v", err)
	}
	id.PID, id.Executable, id.ProcessStart = pid, executable, processStart
	if err := WriteIdentity(tmp, id); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mhome.WatcherBeatPath(tmp), []byte(fmt.Sprintf("%d %d", time.Now().Unix(), pid)), 0644); err != nil {
		t.Fatal(err)
	}
	if claimed, err := mhome.ClaimWatcherLease(tmp, pid); err != nil || !claimed {
		t.Fatalf("claiming watcher lease for child: claimed=%v err=%v", claimed, err)
	}
	if status := WatcherStatusSummary(tmp); status != WatcherRunning {
		t.Fatalf("precondition: watcher status = %s, want running", status)
	}

	// Reap concurrently: this test process is the child's parent, so an exited
	// but unwaited child is a zombie that still reads alive, and EnsureWatcher
	// would rightly report it as a watcher that did not exit.
	exited := make(chan bool, 1)
	go func() { exited <- awaitProcessExit(pid, child.waits.exit) }()
	if err := EnsureWatcher(tmp, false); err != nil {
		t.Fatalf("EnsureWatcher(false) with a running watcher: %v", err)
	}
	if !<-exited {
		t.Fatalf("EnsureWatcher(false) did not stop watcher PID %d", pid)
	}
	child.state = reapCompleted
	if status := WatcherStatusSummary(tmp); status != WatcherAbsent {
		t.Errorf("watcher status after stop = %s, want absent (beat and identity cleared)", status)
	}
	if lease, err := mhome.ReadWatcherLease(tmp); err != nil || lease != nil {
		t.Errorf("watcher lease after stop = %+v, err=%v; want released", lease, err)
	}
}
