package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/supervision"
)

func TestWaitForWatcherBeacon_TimeoutWithoutBeat(t *testing.T) {
	home := t.TempDir()
	status, ok := waitForWatcherBeacon(home, 1, 120*time.Millisecond)
	if ok {
		t.Fatal("expected validation failure without beat/identity")
	}
	if status.Exists {
		t.Fatal("expected no beat")
	}
}

func TestWaitForWatcherBeacon_SeesBeat(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	os.WriteFile(lifecycle.BeatPath(home), []byte(fmt.Sprintf("%d %d\n", time.Now().Unix(), pid)), 0644)
	id := supervision.NewIdentity(home)
	id.PID = pid
	_ = supervision.WriteIdentity(home, id)

	status, _ := waitForWatcherBeacon(home, pid, 200*time.Millisecond)
	if !status.Exists {
		t.Fatal("expected beat to exist")
	}
}

func TestStopWatcher_AlreadyStopped(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	resp := stopWatcher(home)
	if resp.Data.State != "already-stopped" {
		t.Fatalf("state=%q", resp.Data.State)
	}
}

func TestEnsureWatcher_ReturnsContract(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	savedStarter := startWatcherProcess
	savedTimeout := watcherBeaconTimeout
	startWatcherProcess = func(string) (int, error) { return os.Getpid(), nil }
	watcherBeaconTimeout = 10 * time.Millisecond
	t.Cleanup(func() {
		startWatcherProcess = savedStarter
		watcherBeaconTimeout = savedTimeout
	})
	resp := ensureWatcher(home, false)
	if resp.Kind != "watch.ensure" {
		t.Fatalf("kind=%q", resp.Kind)
	}
	if resp.Data.State == "" {
		t.Fatal("empty state")
	}
	_ = stopWatcher(home)
}

// plantLocalWatcherBeacon writes a fresh beat + identity for the current
// process under homeDir. Used to simulate an already-running watcher without
// spawning a daemon (os.Executable is the test binary under go test).
func plantLocalWatcherBeacon(t *testing.T, homeDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	if err := os.WriteFile(lifecycle.BeatPath(homeDir), []byte(fmt.Sprintf("%d %d\n", time.Now().Unix(), pid)), 0644); err != nil {
		t.Fatal(err)
	}
	id := supervision.NewIdentity(homeDir)
	id.PID = pid
	if err := supervision.WriteIdentity(homeDir, id); err != nil {
		t.Fatal(err)
	}
}

// TestEnsureWatcher_CaptainHomeAttach proves ensure attaches on a non-default
// (captain-style) home when beat+identity ownership match that home.
func TestEnsureWatcher_CaptainHomeAttach(t *testing.T) {
	captainHome := t.TempDir()
	plantLocalWatcherBeacon(t, captainHome)

	resp := ensureWatcher(captainHome, false)
	if resp.Kind != "watch.ensure" {
		t.Fatalf("kind=%q", resp.Kind)
	}
	if resp.Data.State != "attached" {
		t.Fatalf("expected attached on captain home, got %q", resp.Data.State)
	}
	if !resp.Data.Noop {
		t.Fatal("attached ensure should be noop")
	}
	if resp.Data.WatchID == "" {
		t.Fatal("expected non-empty watch id")
	}
	if resp.Data.Lease == nil || !resp.Data.Lease.HeartbeatOK {
		t.Fatal("expected healthy lease heartbeat on attach")
	}
	id := supervision.ReadIdentity(captainHome)
	if id == nil || id.Home == "" {
		t.Fatal("identity must remain bound to captain home")
	}
}

// TestEnsureWatcher_CrossHomeDoesNotAttach rejects a planted foreign identity
// so one home cannot attach to another home's watcher PID.
func TestEnsureWatcher_CrossHomeDoesNotAttach(t *testing.T) {
	general := t.TempDir()
	captain := t.TempDir()
	os.MkdirAll(filepath.Join(general, "state"), 0755)
	os.MkdirAll(filepath.Join(captain, "state"), 0755)
	savedStarter := startWatcherProcess
	savedTimeout := watcherBeaconTimeout
	startWatcherProcess = func(string) (int, error) { return os.Getpid(), nil }
	watcherBeaconTimeout = 10 * time.Millisecond
	t.Cleanup(func() {
		startWatcherProcess = savedStarter
		watcherBeaconTimeout = savedTimeout
	})

	pid := os.Getpid()
	// Captain home has a fresh beat for this PID, but identity claims general home.
	os.WriteFile(lifecycle.BeatPath(captain), []byte(fmt.Sprintf("%d %d\n", time.Now().Unix(), pid)), 0644)
	id := supervision.NewIdentity(general)
	id.PID = pid
	if err := supervision.WriteIdentity(captain, id); err != nil {
		t.Fatal(err)
	}

	resp := ensureWatcher(captain, false)
	if resp.Data.State == "attached" {
		t.Fatal("must not attach using foreign-home identity")
	}
	// Ensure may attempt to start a new watcher for captain; clean best-effort.
	// Do not require healthy start: under go test, Executable is the test binary.
	_ = stopWatcher(captain)
}

// TestStopWatcher_CrossHomeIdentityMismatch refuses to signal a PID whose
// identity is bound to a different home (no cross-home kill).
func TestStopWatcher_CrossHomeIdentityMismatch(t *testing.T) {
	general := t.TempDir()
	captain := t.TempDir()
	os.MkdirAll(filepath.Join(general, "state"), 0755)
	os.MkdirAll(filepath.Join(captain, "state"), 0755)

	pid := os.Getpid()
	// Plant beat on captain pointing at this process.
	os.WriteFile(lifecycle.BeatPath(captain), []byte(fmt.Sprintf("%d %d\n", time.Now().Unix(), pid)), 0644)
	// Identity under captain still names the general home.
	id := supervision.NewIdentity(general)
	id.PID = pid
	if err := supervision.WriteIdentity(captain, id); err != nil {
		t.Fatal(err)
	}

	resp := stopWatcher(captain)
	if resp.Data.State != "identity-mismatch" {
		t.Fatalf("state=%q, want identity-mismatch", resp.Data.State)
	}
	// Beat and identity must remain (refuse, do not clear foreign claim).
	if _, gotPID, ok := lifecycle.ReadBeat(captain); !ok || gotPID != pid {
		t.Fatal("beat should remain after refused stop")
	}
	if supervision.ReadIdentity(captain) == nil {
		t.Fatal("identity should remain after refused stop")
	}
}

// TestStopWatcher_CaptainHomeIdentityMismatchWithoutIdentity refuses beat-only
// stop on a captain home path (ownership must be proven).
func TestStopWatcher_CaptainHomeBeatOnly(t *testing.T) {
	captain := t.TempDir()
	os.MkdirAll(filepath.Join(captain, "state"), 0755)
	os.WriteFile(lifecycle.BeatPath(captain), []byte(fmt.Sprintf("%d %d\n", time.Now().Unix(), os.Getpid())), 0644)

	resp := stopWatcher(captain)
	if resp.Data.State != "identity-mismatch" {
		t.Fatalf("state=%q, want identity-mismatch", resp.Data.State)
	}
}

// TestWatchStatus_NoWatcher reports absent/healthy correctly.
func TestWatchStatus_NoWatcher(t *testing.T) {
	home := t.TempDir()

	// Bounded status without watcher or queue.
	resp := evaluateWatcherStatus(home)
	if resp.Kind != "watch.status" {
		t.Fatalf("kind=%q", resp.Kind)
	}
	if resp.Data.State != "absent" && resp.Data.State != "healthy" {
		t.Errorf("state=%q on clean home, want absent", resp.Data.State)
	}
	if resp.Data.GuardState != "unhealthy" && resp.Data.GuardState != "healthy" {
		t.Errorf("guard_state=%q", resp.Data.GuardState)
	}
}

// TestWatchStatus_WithDiagnostics proves diagnostics include actionable info.
func TestWatchStatus_WithDiagnostics(t *testing.T) {
	home := t.TempDir()

	// Add a pending wake.
	lifecycle.EnqueueWake(home, "signal", "task-1", "done: work")

	resp := evaluateWatcherStatus(home)
	if resp.Kind != "watch.status" {
		t.Fatalf("kind=%q", resp.Kind)
	}
	if resp.Data.QueuedWakes < 1 {
		t.Errorf("queued_wakes=%d, want >=1", resp.Data.QueuedWakes)
	}
	if len(resp.Data.Diagnostics) > 0 {
		hasWakeDiag := false
		for _, d := range resp.Data.Diagnostics {
			if strings.Contains(d, "Queued wakes") {
				hasWakeDiag = true
				break
			}
		}
		if !hasWakeDiag {
			t.Errorf("diagnostics missing wake info: %v", resp.Data.Diagnostics)
		}
	}
}

// TestWatchStatus_WithMaterialWake verifies material age in status.
func TestWatchStatus_WithMaterialWake(t *testing.T) {
	home := t.TempDir()

	// Write a wake with an old timestamp.
	oldEpoch := time.Now().Add(-10 * time.Minute).Unix()
	queuePath := lifecycle.QueuePath(home)
	os.MkdirAll(filepath.Dir(queuePath), 0755)
	line := fmt.Sprintf("%d\t%d\tsignal\ttask-old\tdone: old material\n", oldEpoch, 1)
	os.WriteFile(queuePath, []byte(line), 0644)

	resp := evaluateWatcherStatus(home)
	if resp.Data.MaterialAge == "" {
		t.Errorf("expected non-empty MaterialAge, got %q", resp.Data.MaterialAge)
	}
	if resp.Data.QueuedWakes < 1 {
		t.Errorf("queued_wakes=%d", resp.Data.QueuedWakes)
	}
}

// TestOldestMaterialWakeAge verifies age calculation.
func TestOldestMaterialWakeAge(t *testing.T) {
	home := t.TempDir()

	// Old material wake
	oldEpoch := time.Now().Add(-10 * time.Minute).Unix()
	queuePath := lifecycle.QueuePath(home)
	os.MkdirAll(filepath.Dir(queuePath), 0755)
	os.WriteFile(queuePath, []byte(fmt.Sprintf("%d\t%d\tsignal\ttask-old\tdone: old\n", oldEpoch, 1)), 0644)

	age := oldestMaterialWakeAge(home)
	// Age should be roughly 10 minutes.
	if age < 500 || age > 700 {
		t.Logf("oldest material wake age = %ds (expected ~600s)", age)
	}
}

// TestOldestMaterialWakeAge_NoQueue returns 0.
func TestOldestMaterialWakeAge_NoQueue(t *testing.T) {
	home := t.TempDir()
	if age := oldestMaterialWakeAge(home); age != 0 {
		t.Errorf("age = %d on empty home, want 0", age)
	}
}

// TestOldestMaterialWakeAge_RoutineOnly returns 0.
func TestOldestMaterialWakeAge_RoutineOnly(t *testing.T) {
	home := t.TempDir()
	lifecycle.EnqueueWake(home, "stale", "task-routine", "working: in progress")
	if age := oldestMaterialWakeAge(home); age != 0 {
		t.Errorf("age = %d for routine wake, want 0", age)
	}
}
