package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Sentinel tests ---

func TestSentinelMark(t *testing.T) {
	payload := "PR #42 merged"
	marked := Mark(payload)
	if !strings.HasPrefix(marked, FM_INJECT_MARK) {
		t.Errorf("Mark(%q) = %q, expected %q prefix", payload, marked, FM_INJECT_MARK)
	}
}

func TestSentinelRoundTrip(t *testing.T) {
	payloads := []string{
		"PR #42 merged",
		"needs-decision: which target branch",
		"",
		"done: build green",
	}
	for _, p := range payloads {
		marked := Mark(p)
		got := strings.TrimPrefix(marked, FM_INJECT_MARK)
		if got != p {
			t.Errorf("Mark/Marked round-trip failed: input=%q, got=%q", p, got)
		}
	}
}

// --- Lock tests ---

func TestLockAcquireRelease(t *testing.T) {
	tmp := t.TempDir()
	lock, acquired, err := AcquireLock(tmp)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if !acquired {
		t.Fatal("AcquireLock returned not acquired, want acquired")
	}
	if lock.pid == 0 {
		t.Error("Lock.pid = 0, want non-zero")
	}
	if lock.startAt.IsZero() {
		t.Error("Lock.startAt is zero, want non-zero")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Lock.Release: %v", err)
	}
	// Lock file should be gone
	if _, err := os.Stat(lock.path); !os.IsNotExist(err) {
		t.Errorf("lock file still exists after Release, stat err: %v", err)
	}
}

func TestLockIdempotentSameProcess(t *testing.T) {
	tmp := t.TempDir()
	lock1, acquired1, err := AcquireLock(tmp)
	if err != nil {
		t.Fatalf("AcquireLock #1: %v", err)
	}
	if !acquired1 {
		t.Fatal("AcquireLock #1: not acquired, want acquired")
	}
	defer lock1.Release()

	// Second acquire should be no-op (lock held by same process or
	// more precisely, by the PID we wrote — which is ourselves).
	// The lock file exists and names our PID (which is alive), so
	// it should return (nil, false, nil).
	lock2, acquired2, err := AcquireLock(tmp)
	if err != nil {
		t.Fatalf("AcquireLock #2: %v", err)
	}
	if acquired2 {
		t.Fatal("AcquireLock #2: acquired, want no-op (already held)")
	}
	if lock2 != nil {
		t.Fatalf("AcquireLock #2: lock != nil, want nil for no-op")
	}
}

func TestLockStaleRecovery(t *testing.T) {
	tmp := t.TempDir()

	// Write a lock file with a non-existent PID
	lockPath := filepath.Join(tmp, afkLockFile)
	os.MkdirAll(filepath.Dir(lockPath), 0755)
	content := fmt.Sprintf("%d\t%s\n", 99999999, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(lockPath, []byte(content), 0644); err != nil {
		t.Fatalf("writing stale lock: %v", err)
	}

	// Acquire should reclaim the stale lock
	lock, acquired, err := AcquireLock(tmp)
	if err != nil {
		t.Fatalf("AcquireLock after stale: %v", err)
	}
	if !acquired {
		t.Fatal("AcquireLock after stale: not acquired, want acquired (reclaim)")
	}
	defer lock.Release()

	if lock.pid != os.Getpid() {
		t.Errorf("Lock.pid = %d, want %d", lock.pid, os.Getpid())
	}
}

func TestLockFileContents(t *testing.T) {
	tmp := t.TempDir()
	lock, acquired, err := AcquireLock(tmp)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if !acquired {
		t.Fatal("AcquireLock: not acquired")
	}
	defer lock.Release()

	data, err := os.ReadFile(filepath.Join(tmp, afkLockFile))
	if err != nil {
		t.Fatalf("reading lock file: %v", err)
	}
	pid, startStr := parseLockContent(data)
	if pid != os.Getpid() {
		t.Errorf("lock pid = %d, want %d", pid, os.Getpid())
	}
	if startStr == "" {
		t.Error("lock start time is empty")
	}
}

// --- Triage tests ---

func TestTriageNoQueue(t *testing.T) {
	tmp := t.TempDir()
	digest, err := OneCycle(tmp)
	if err != nil {
		t.Fatalf("OneCycle on empty home: %v", err)
	}
	if digest != nil {
		t.Errorf("OneCycle = %+v, want nil (no queue)", digest)
	}
}

func TestTriageEmptyQueue(t *testing.T) {
	tmp := t.TempDir()
	// Write an empty wake queue
	qPath := filepath.Join(tmp, "state", ".wake-queue")
	os.MkdirAll(filepath.Dir(qPath), 0755)
	os.WriteFile(qPath, []byte{}, 0644)

	digest, err := OneCycle(tmp)
	if err != nil {
		t.Fatalf("OneCycle on empty queue: %v", err)
	}
	if digest != nil {
		t.Errorf("OneCycle = %+v, want nil (empty queue)", digest)
	}
}

func TestTriageRoutineWake(t *testing.T) {
	tmp := t.TempDir()
	qPath := filepath.Join(tmp, "state", ".wake-queue")
	os.MkdirAll(filepath.Dir(qPath), 0755)
	line := fmt.Sprintf("%d\t%d\tcheck\thealth\tall green\n", time.Now().Unix(), os.Getpid())
	os.WriteFile(qPath, []byte(line), 0644)

	digest, err := OneCycle(tmp)
	if err != nil {
		t.Fatalf("OneCycle: %v", err)
	}
	if digest == nil {
		t.Fatal("OneCycle: digest is nil, want non-nil")
	}
	if len(digest.Escalated) != 0 {
		t.Errorf("got %d escalated wakes for routine entry, want 0", len(digest.Escalated))
	}
	if len(digest.Routines) != 1 {
		t.Errorf("got %d routine wakes, want 1", len(digest.Routines))
	}
	if digest.Routines[0].Key != "health" {
		t.Errorf("routine key = %q, want %q", digest.Routines[0].Key, "health")
	}
	if digest.Routines[0].Payload != "all green" {
		t.Errorf("routine payload = %q, want %q", digest.Routines[0].Payload, "all green")
	}
	if digest.Routines[0].IsGeneralRelevant {
		t.Error("routine IsGeneralRelevant = true, want false")
	}
}

func TestTriageGeneralRelevantWake(t *testing.T) {
	tmp := t.TempDir()
	qPath := filepath.Join(tmp, "state", ".wake-queue")
	os.MkdirAll(filepath.Dir(qPath), 0755)
	line := fmt.Sprintf("%d\t%d\tafk\ttask-1\tPR merged\n", time.Now().Unix(), os.Getpid())
	os.WriteFile(qPath, []byte(line), 0644)

	digest, err := OneCycle(tmp)
	if err != nil {
		t.Fatalf("OneCycle: %v", err)
	}
	if digest == nil {
		t.Fatal("OneCycle: digest is nil, want non-nil")
	}
	if len(digest.Escalated) != 1 {
		t.Errorf("got %d escalated wakes, want 1", len(digest.Escalated))
	}
	if len(digest.Routines) != 0 {
		t.Errorf("got %d routine wakes, want 0", len(digest.Routines))
	}
	if digest.Escalated[0].Kind != "afk" {
		t.Errorf("escalated kind = %q, want %q", digest.Escalated[0].Kind, "afk")
	}
	if digest.Escalated[0].Key != "task-1" {
		t.Errorf("escalated key = %q, want %q", digest.Escalated[0].Key, "task-1")
	}
	if digest.Escalated[0].Payload != "PR merged" {
		t.Errorf("escalated payload = %q, want %q", digest.Escalated[0].Payload, "PR merged")
	}
	if !digest.Escalated[0].IsGeneralRelevant {
		t.Error("escalated IsGeneralRelevant = false, want true")
	}
}

func TestTriageMixedWakes(t *testing.T) {
	tmp := t.TempDir()
	qPath := filepath.Join(tmp, "state", ".wake-queue")
	os.MkdirAll(filepath.Dir(qPath), 0755)
	now := time.Now().Unix()
	lines := fmt.Sprintf("%d\t%d\tafk\ttask-1\tPR merged\n%d\t%d\tcheck\thealth\tall green\n",
		now, os.Getpid(), now+1, os.Getpid())
	os.WriteFile(qPath, []byte(lines), 0644)

	digest, err := OneCycle(tmp)
	if err != nil {
		t.Fatalf("OneCycle: %v", err)
	}
	if digest == nil {
		t.Fatal("OneCycle: digest is nil, want non-nil")
	}
	if len(digest.Escalated) != 1 {
		t.Errorf("got %d escalated, want 1", len(digest.Escalated))
	}
	if len(digest.Routines) != 1 {
		t.Errorf("got %d routines, want 1", len(digest.Routines))
	}
}

func TestTriageDrainsQueue(t *testing.T) {
	tmp := t.TempDir()
	qPath := filepath.Join(tmp, "state", ".wake-queue")
	os.MkdirAll(filepath.Dir(qPath), 0755)
	line := fmt.Sprintf("%d\t%d\tafk\ttask-1\tdone: PR merged\n", time.Now().Unix(), os.Getpid())
	os.WriteFile(qPath, []byte(line), 0644)

	digest, err := OneCycle(tmp)
	if err != nil {
		t.Fatalf("OneCycle: %v", err)
	}
	if digest == nil {
		t.Fatal("OneCycle: digest is nil")
	}
	if len(digest.Escalated) != 1 {
		t.Fatalf("escalated count = %d, want 1", len(digest.Escalated))
	}

	// Queue file should be removed after drain
	if _, err := os.Stat(qPath); !os.IsNotExist(err) {
		t.Errorf("wake queue still exists after drain: %v", err)
	}

	// Second call should return nil (empty queue)
	digest2, err := OneCycle(tmp)
	if err != nil {
		t.Fatalf("OneCycle #2: %v", err)
	}
	if digest2 != nil {
		t.Error("OneCycle #2 returned non-nil digest, queue should be empty")
	}
}

// --- Daemon flag lifecycle test ---

// waitForFile waits for an externally observable file to appear. The bounded
// poll is deliberately separate from the behavior that writes that file.
func waitForFile(child *afkDaemonChild, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-child.done:
			return fmt.Errorf("%s was not created because the AFK daemon child exited: %v\n%s", path, child.waitErr, child.output())
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	child.cleanup()
	return fmt.Errorf("%s was not created within %s\n%s", path, timeout, child.output())
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s does not exist after lossy daemon stop: %v", path, err)
	}
}

func TestDaemonSetsAndClearsFlag(t *testing.T) {
	tmp := t.TempDir()
	child := startAFKDaemonChild(t, tmp, true)
	flagPath := filepath.Join(tmp, afkFlagFile)
	lockPath := filepath.Join(tmp, afkLockFile)

	if err := waitForFile(child, afkDaemonReadyPath(tmp), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := waitForFile(child, flagPath, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := waitForFile(child, lockPath, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	stopAFKDaemonChild(t, child)

	if stopProcessIsLossy() {
		assertFileExists(t, flagPath)
		assertFileExists(t, lockPath)
		return
	}
	if _, err := os.Stat(flagPath); !os.IsNotExist(err) {
		t.Error("consent flag still exists after daemon stop")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file still exists after daemon stop")
	}
}

// TestDaemonCatchesSignalAtEarliestReadiness preserves the cross-process
// readiness ordering: the child publishes a marker immediately after its
// Daemon.Start observes d.ready, and the parent stops that child at the first
// exported observation. The marker cannot preserve the old in-process,
// instruction-level proof that the signal arrived before lock creation; it
// proves that an external observer can react at the daemon's readiness seam
// without targeting the test binary.
func TestDaemonCatchesSignalAtEarliestReadiness(t *testing.T) {
	tmp := t.TempDir()
	child := startAFKDaemonChild(t, tmp, true)

	if err := waitForFile(child, afkDaemonReadyPath(tmp), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	stopAFKDaemonChild(t, child)

	if stopProcessIsLossy() {
		// The child may be stopped before either startup artifact is written.
		return
	}
	if _, err := os.Stat(filepath.Join(tmp, afkFlagFile)); !os.IsNotExist(err) {
		t.Error("consent flag still exists after daemon stop")
	}
	if _, err := os.Stat(filepath.Join(tmp, afkLockFile)); !os.IsNotExist(err) {
		t.Error("lock file still exists after daemon stop")
	}
}

// TestDaemonSignalSafeWhenLockIsTheProbe uses state/.lock, the first file the
// child writes, as the readiness signal an outside observer would key on. With
// the handler installed before AcquireLock, stopping the child after that
// observation is safe without ever signalling the test binary.
func TestDaemonSignalSafeWhenLockIsTheProbe(t *testing.T) {
	tmp := t.TempDir()
	child := startAFKDaemonChild(t, tmp, false)

	lockPath := filepath.Join(tmp, afkLockFile)
	if err := waitForFile(child, lockPath, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	stopAFKDaemonChild(t, child)

	if stopProcessIsLossy() {
		assertFileExists(t, lockPath)
		return
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file still exists after daemon stop")
	}
}
