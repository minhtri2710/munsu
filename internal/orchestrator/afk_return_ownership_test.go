package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// seedLiveDaemonLock writes a lock naming the test process itself, plus the
// consent flag. The PID is genuinely alive, so Return reaches the ownership
// gate; whether it gets past it is what these tests are about.
func seedLiveDaemonLock(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	lock := fmt.Sprintf("%d\t2024-01-01T00:00:00Z\n", os.Getpid())
	if err := os.WriteFile(filepath.Join(stateDir, ".lock"), []byte(lock), 0644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".afk"), []byte("2024-01-01T00:00:00Z\n"), 0644); err != nil {
		t.Fatalf("write flag: %v", err)
	}
	return tmp
}

func assertRefusedToStop(t *testing.T, homeDir string, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("Return: err = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "refusing to stop") {
		t.Fatalf("Return: err = %v, want a refusal to stop", err)
	}
	// The refusal must keep the lock and the consent flag: the PID may still be
	// a live daemon, and clearing either would hand the next AcquireLock a free
	// lock while that daemon still writes under it.
	if _, statErr := os.Stat(filepath.Join(homeDir, "state", ".lock")); statErr != nil {
		t.Errorf("lock removed after refusal: %v", statErr)
	}
	if !IsActive(homeDir) {
		t.Error("consent flag cleared after refusal, want kept")
	}
}

// TestReturn_RefusesPIDWithNoPublishedIdentity covers the reused-PID case with
// no evidence at all: the lock names a live PID, but nothing in the home claims
// that PID is the AFK daemon. isProcessAlive says "something holds this PID",
// which is not the question, so Return must refuse rather than terminate.
func TestReturn_RefusesPIDWithNoPublishedIdentity(t *testing.T) {
	tmp := seedLiveDaemonLock(t)

	_, err := Return(tmp)
	assertRefusedToStop(t, tmp, err)
}

// TestReturn_RefusesPIDNamedByADifferentIdentity covers a lock and an identity
// that disagree: the published daemon is some other PID, so the PID in the lock
// has no claim to be stopped.
func TestReturn_RefusesPIDNamedByADifferentIdentity(t *testing.T) {
	tmp := seedLiveDaemonLock(t)
	publishAfkIdentity(t, tmp, os.Getpid()+1, "some-other-binary", "some-other-token")

	_, err := Return(tmp)
	assertRefusedToStop(t, tmp, err)
}

// TestReturn_RefusesPIDWhoseIdentityDoesNotMatch covers the reused-PID case with
// stale evidence: an "afk" identity exists for this PID, but its start token is
// not the one the kernel reports, which is exactly what a recycled PID looks
// like.
func TestReturn_RefusesPIDWhoseIdentityDoesNotMatch(t *testing.T) {
	tmp := seedLiveDaemonLock(t)

	executable, _, err := processIdentity(os.Getpid())
	if err != nil {
		t.Skipf("processIdentity unsupported on %s: %v", runtime.GOOS, err)
	}
	publishAfkIdentity(t, tmp, os.Getpid(), executable, "not-the-token-the-kernel-reports")

	_, err = Return(tmp)
	assertRefusedToStop(t, tmp, err)
}

// TestReturn_StopsAVerifiedDaemon is the positive half: a PID whose published
// identity still matches what the kernel reports is stopped, and the lock, the
// consent flag and the identity artifact are all gone afterwards -- the last one
// because Kill skips the daemon's own deferred cleanup.
func TestReturn_StopsAVerifiedDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a spawnable long-running child; no lane runs tests on windows anyway")
	}
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "state"), 0755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}

	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}
	defer func() { _ = child.Process.Kill() }()
	pid := child.Process.Pid
	// Reap concurrently. Production never hits this: Return runs in its own CLI
	// process and is not the daemon's parent. Here it is the parent, so without
	// a waiter the killed child stays a zombie -- which the unix isProcessAlive
	// (kill -0) reports as alive, and waitForDaemonExit would correctly time out
	// on a state that cannot occur at the real call site.
	reaped := make(chan struct{})
	go func() {
		_, _ = child.Process.Wait()
		close(reaped)
	}()
	defer func() { <-reaped }()

	executable, startToken, err := processIdentity(pid)
	if err != nil {
		t.Skipf("processIdentity unsupported on %s: %v", runtime.GOOS, err)
	}
	publishAfkIdentity(t, tmp, pid, executable, startToken)
	lock := fmt.Sprintf("%d\t2024-01-01T00:00:00Z\n", pid)
	if err := os.WriteFile(filepath.Join(tmp, "state", ".lock"), []byte(lock), 0644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "state", ".afk"), []byte("2024-01-01T00:00:00Z\n"), 0644); err != nil {
		t.Fatalf("write flag: %v", err)
	}

	if _, err := Return(tmp); err != nil {
		t.Fatalf("Return on a verified daemon: %v", err)
	}
	if isProcessAlive(pid) {
		t.Error("child still alive after Return")
	}
	if _, err := os.Stat(filepath.Join(tmp, "state", ".lock")); err == nil {
		t.Error("lock kept after a confirmed stop")
	}
	if IsActive(tmp) {
		t.Error("consent flag kept after a confirmed stop")
	}
	if _, err := os.Stat(home.WriterIdentityPath(tmp, "afk")); err == nil {
		t.Error("writer identity artifact kept after a confirmed stop")
	}
}

func publishAfkIdentity(t *testing.T, homeDir string, pid int, executable, startToken string) {
	t.Helper()
	canonical, err := home.CanonicalPath(homeDir)
	if err != nil {
		t.Fatalf("CanonicalPath: %v", err)
	}
	identity := home.WriterIdentity{
		SchemaVersion:  1,
		Kind:           "afk",
		PID:            pid,
		StartToken:     startToken,
		ExecutablePath: executable,
		CanonicalHome:  canonical,
	}
	if err := home.PublishWriterIdentity(homeDir, "afk", identity); err != nil {
		t.Fatalf("PublishWriterIdentity: %v", err)
	}
}
