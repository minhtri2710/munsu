package orchestrator

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// holdAFKLockFor takes the AFK lock in this process, as a running daemon holds
// it, and makes it name pid. The flock is what makes Return read the pid at
// all; the content is rewritten through a second handle because the daemon
// under test is a different process from the one holding the flock here.
func holdAFKLockFor(t *testing.T, homeDir string, pid int) {
	t.Helper()
	lock, acquired, err := home.AcquireAFKLock(homeDir)
	if err != nil || !acquired {
		t.Fatalf("AcquireAFKLock = %v, %v; want held", acquired, err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	content := fmt.Sprintf("%d\t2024-01-01T00:00:00Z\n", pid)
	if err := os.WriteFile(home.AFKLockPath(homeDir), []byte(content), 0600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}

// seedLiveDaemonLock holds the AFK lock naming the test process itself, plus
// the consent flag. The PID is genuinely alive, so Return reaches the
// ownership gate; whether it gets past it is what these tests are about.
func seedLiveDaemonLock(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	holdAFKLockFor(t, tmp, os.Getpid())
	if err := os.WriteFile(filepath.Join(tmp, "state", ".afk"), []byte("2024-01-01T00:00:00Z\n"), 0644); err != nil {
		t.Fatalf("write flag: %v", err)
	}
	return tmp
}

// assertAFKLockNames checks Return left the daemon's lock alone: it still
// names pid.
func assertAFKLockNames(t *testing.T, homeDir string, pid int) {
	t.Helper()
	if got, err := home.ReadAFKLockPID(homeDir); err != nil || got != pid {
		t.Errorf("AFK lock pid = %d, %v; want %d", got, err, pid)
	}
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
	// a live daemon, and clearing either would tell the next start AFK is off
	// while that daemon still writes under it.
	assertAFKLockNames(t, homeDir, os.Getpid())
	if !IsActive(homeDir) {
		t.Error("consent flag cleared after refusal, want kept")
	}
}

// TestReturn_RefusesPIDWithNoPublishedIdentity covers the reused-PID case with
// no evidence at all: the lock names a live PID, but nothing in the home claims
// that PID is the AFK daemon. home.IsProcessAlive says "something holds this PID",
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

	executable, _, err := home.ProcessIdentity(os.Getpid())
	if err != nil {
		t.Skipf("home.ProcessIdentity unsupported on %s: %v", runtime.GOOS, err)
	}
	publishAfkIdentity(t, tmp, os.Getpid(), executable, "not-the-token-the-kernel-reports")

	_, err = Return(tmp)
	assertRefusedToStop(t, tmp, err)
}

// TestReturn_StopsAVerifiedDaemon is the positive half: a PID whose published
// identity still matches what the kernel reports is stopped, and the consent
// flag and the identity artifact are gone afterwards -- the last one because
// Kill skips the daemon's own deferred cleanup. The lock is not Return's to
// clear: a real daemon's flock goes with its process.
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
	pid := child.Process.Pid
	// Reap concurrently. Production never hits this: Return runs in its own CLI
	// process and is not the daemon's parent. Here it is the parent, so without
	// a waiter the killed child stays a zombie -- which the unix home.IsProcessAlive
	// (a signal-0 kill) reports as alive, and waitForDaemonExit would correctly
	// time out on a state that cannot occur at the real call site.
	reaped := make(chan struct{})
	go func() {
		_, _ = child.Process.Wait()
		close(reaped)
	}()
	defer func() {
		_ = child.Process.Kill()
		<-reaped
	}()

	executable, startToken, err := home.ProcessIdentity(pid)
	if err != nil {
		t.Skipf("home.ProcessIdentity unsupported on %s: %v", runtime.GOOS, err)
	}
	publishAfkIdentity(t, tmp, pid, executable, startToken)
	holdAFKLockFor(t, tmp, pid)
	if err := os.WriteFile(filepath.Join(tmp, "state", ".afk"), []byte("2024-01-01T00:00:00Z\n"), 0644); err != nil {
		t.Fatalf("write flag: %v", err)
	}

	report, err := Return(tmp)
	if err != nil {
		t.Fatalf("Return on a verified daemon: %v", err)
	}
	// The report must carry the predicate's lossiness answer for this stop:
	// false on unix (SIGTERM flushes in the daemon's deferred shutdown), true
	// on windows (Kill skips that flush). This test skips on windows, so the
	// wired value verified here is the unix answer; the windows value is
	// asserted by TestStopProcessLossinessMatchesPlatform on the
	// windows-observation lane.
	if report.LossyStop != stopProcessIsLossy() {
		t.Errorf("report.LossyStop = %v, want the predicate answer %v", report.LossyStop, stopProcessIsLossy())
	}
	if home.IsProcessAlive(pid) {
		t.Error("child still alive after Return")
	}
	if IsActive(tmp) {
		t.Error("consent flag kept after a confirmed stop")
	}
	if _, err := os.Stat(home.WriterIdentityPath(tmp, "afk")); err == nil {
		t.Error("writer identity artifact kept after a confirmed stop")
	}
}

// TestReturn_RefusesWhenDaemonSurvivesStopRequest covers the last refusal in
// Return (afk_return.go): the identity checks out and the stop request is
// accepted, but the daemon is still alive at the afkStopWait bound, so Return
// must refuse rather than clear the consent flag for a process whose
// death is unconfirmed. The state that builds it is a daemon that ignores the
// stop request: stopProcess is SIGTERM on unix, SIGTERM is ignorable, so a child
// that installs SIG_IGN survives it and waitForDaemonExit polls to its bound and
// reports the daemon not gone.
func TestReturn_RefusesWhenDaemonSurvivesStopRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a SIGTERM-ignoring child; no lane runs tests on windows anyway")
	}

	// Shorten the bounded poll for this test and restore the production values
	// afterwards -- afkStopWait/afkStopPoll are vars only so a test can do this.
	origWait, origPoll := afkStopWait, afkStopPoll
	afkStopWait, afkStopPoll = 150*time.Millisecond, 10*time.Millisecond
	defer func() { afkStopWait, afkStopPoll = origWait, origPoll }()

	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "state"), 0755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}

	pid := startSIGTERMIgnoringDaemon(t)

	executable, startToken, err := home.ProcessIdentity(pid)
	if err != nil {
		t.Skipf("home.ProcessIdentity unsupported on %s: %v", runtime.GOOS, err)
	}
	publishAfkIdentity(t, tmp, pid, executable, startToken)
	holdAFKLockFor(t, tmp, pid)
	if err := os.WriteFile(filepath.Join(tmp, "state", ".afk"), []byte("2024-01-01T00:00:00Z\n"), 0644); err != nil {
		t.Fatalf("write flag: %v", err)
	}

	_, err = Return(tmp)
	if err == nil {
		t.Fatal("Return: err = nil, want a refusal for a daemon that survived the stop request")
	}
	// Name what this guard refuses: the PID, the survival, and the bound it was
	// observed against -- not just "some error happened".
	if !strings.Contains(err.Error(), fmt.Sprintf("PID %d", pid)) {
		t.Errorf("Return: err = %v, want the daemon PID named", err)
	}
	if !strings.Contains(err.Error(), "remained alive") {
		t.Errorf("Return: err = %v, want the survival named", err)
	}
	if !strings.Contains(err.Error(), afkStopWait.String()) {
		t.Errorf("Return: err = %v, want the observation bound %s named", err, afkStopWait)
	}

	// The residual state is the whole reason expiry is an error here: the
	// daemon's death is unconfirmed, so the lock and the consent flag must
	// survive for a later Return to find them, and the daemon itself must still
	// be running.
	assertAFKLockNames(t, tmp, pid)
	if !IsActive(tmp) {
		t.Error("consent flag cleared after a refused stop, want kept")
	}
	if !home.IsProcessAlive(pid) {
		t.Error("daemon no longer alive after a refused stop, want still running")
	}
}

// helperDaemonEnv marks a re-execution of this test binary as the child process
// startSIGTERMIgnoringDaemon spawns, rather than an ordinary test run.
const helperDaemonEnv = "MUNSU_TEST_HELPER_DAEMON"

// startSIGTERMIgnoringDaemon spawns a child that survives SIGTERM and returns
// its PID, registering cleanup that kills and reaps it.
//
// The child is this test binary re-executed, not a shell, and that is
// load-bearing rather than stylistic. Return verifies ownership by reading
// home.ProcessIdentity twice -- once here to publish the artifact, once inside
// daemonIdentityForPID -- and requires the executable path to match. On macOS
// /bin/sh is bash, which re-execs itself during startup: kern.procargs2 reports
// "/bin/sh" before that re-exec and "/bin/bash" after, so the two reads
// straddle it and Return refuses at the ownership gate instead of reaching the
// bound this test is about. Measured, not assumed: the start token is stable
// across the window and only the executable path moves. A Go test binary never
// re-execs, so its identity is stable from the moment it is spawned.
//
// This is a property of spawning a shell, not a defect in home.ProcessIdentity: the
// daemon publishes its own identity from inside itself (publishDaemonIdentity
// -> home.ProcessIdentity(os.Getpid())), by which point any re-exec is long past.
//
// The readiness pipe closes the second race: without it the parent could send
// SIGTERM before the child installed SIG_IGN, and the child would die on a
// signal it is supposed to ignore -- an outcome that looks exactly like the
// daemon exiting normally, i.e. a flake that would pass the wrong branch.
func startSIGTERMIgnoringDaemon(t *testing.T) int {
	t.Helper()

	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatalf("readiness pipe: %v", err)
	}
	defer readyR.Close()

	child := exec.Command(os.Args[0], "-test.run=^TestHelperDaemonIgnoresSIGTERM$")
	child.Env = append(os.Environ(), helperDaemonEnv+"=1")
	child.ExtraFiles = []*os.File{readyW}
	if err := child.Start(); err != nil {
		readyW.Close()
		t.Fatalf("starting helper daemon: %v", err)
	}
	// The parent's copy must go, or the read below cannot see EOF if the child
	// dies before signalling.
	readyW.Close()

	reaped := make(chan struct{})
	t.Cleanup(func() {
		_ = child.Process.Kill()
		<-reaped
	})
	go func() {
		_, _ = child.Process.Wait()
		close(reaped)
	}()

	buf := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(readyR, buf); err != nil {
		t.Fatalf("helper daemon never signalled readiness: %v", err)
	}
	return child.Process.Pid
}

// TestHelperDaemonIgnoresSIGTERM is not a test. It is the child process entry
// point for startSIGTERMIgnoringDaemon, and skips unless this binary was
// re-executed with helperDaemonEnv set.
func TestHelperDaemonIgnoresSIGTERM(t *testing.T) {
	if os.Getenv(helperDaemonEnv) != "1" {
		t.Skip("child process entry point for startSIGTERMIgnoringDaemon")
	}
	signal.Ignore(syscall.SIGTERM)

	ready := os.NewFile(3, "ready")
	if ready == nil {
		t.Fatal("helper daemon: no readiness pipe on fd 3")
	}
	if _, err := ready.WriteString("ready\n"); err != nil {
		t.Fatalf("helper daemon: signalling readiness: %v", err)
	}
	ready.Close()

	// Outlive the parent's stop wait. Sleeping rather than blocking forever
	// keeps a timer pending, so the runtime never calls this a deadlock. The
	// parent kills and reaps this process in its cleanup.
	for {
		time.Sleep(time.Hour)
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

// seedStoppedDaemonHome writes a home whose daemon died without releasing:
// the AFK lock file names a pid, but nobody holds its flock, so Return reads no
// daemon, skips the stop block entirely and reaches the consent cleanup.
func seedStoppedDaemonHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "state"), 0755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.WriteFile(home.AFKLockPath(tmp), []byte("12345\t2024-01-01T00:00:00Z\n"), 0600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	return tmp
}

// TestReturnSurfacesConsentClearFailure proves a Disable failure aborts Return
// before the drain instead of reporting success over a flag that still says
// AFK is on.
//
// The failure is built by making state/.afk a non-empty directory: os.Remove
// then fails with "directory not empty" for every user, including root, and that
// error is not an IsNotExist, so Disable returns it. A read-only flag file would
// not work -- os.Remove is governed by write permission on the parent directory,
// not on the file, so chmod 0400 removes cleanly.
func TestReturnSurfacesConsentClearFailure(t *testing.T) {
	home := seedStoppedDaemonHome(t)
	flagPath := filepath.Join(home, "state", ".afk")
	if err := os.MkdirAll(filepath.Join(flagPath, "child"), 0755); err != nil {
		t.Fatalf("seeding an unremovable consent flag: %v", err)
	}

	_, err := Return(home)
	if err == nil {
		t.Fatal("Return over an unclearable consent flag: got nil error, want the Disable failure surfaced")
	}
	if !strings.Contains(err.Error(), "disabling afk") {
		t.Fatalf("Return error = %v, want it to carry Disable's own message", err)
	}
}

// TestReturnClearsConsentAfterDeadDaemon proves the ordinary path clears
// consent, so the guard above refuses a real failure rather than every call,
// and that the dead daemon's lock is free for the next start.
func TestReturnClearsConsentAfterDeadDaemon(t *testing.T) {
	homeDir := seedStoppedDaemonHome(t)
	flagPath := filepath.Join(homeDir, "state", ".afk")
	if err := os.WriteFile(flagPath, []byte(""), 0644); err != nil {
		t.Fatalf("write consent flag: %v", err)
	}

	if _, err := Return(homeDir); err != nil {
		t.Fatalf("Return over a cleanly stopped daemon: %v", err)
	}
	if _, err := os.Stat(flagPath); !os.IsNotExist(err) {
		t.Fatalf("consent flag after Return: err=%v, want it removed", err)
	}
	assertAFKLockFree(t, homeDir)
}

// TestReturnRefusesUnreadableDaemonLock builds a held AFK lock that records no
// pid, as a daemon between its flock and its pid write looks. "No daemon" would
// let Return clear consent under it, so Return must refuse and keep the flag.
func TestReturnRefusesUnreadableDaemonLock(t *testing.T) {
	homeDir := seedLiveDaemonLock(t)
	if err := os.Truncate(home.AFKLockPath(homeDir), 0); err != nil {
		t.Fatal(err)
	}

	_, err := Return(homeDir)
	if err == nil || !strings.Contains(err.Error(), "reading AFK daemon lock") {
		t.Fatalf("Return error = %v, want the unreadable lock refused", err)
	}
	if !IsActive(homeDir) {
		t.Error("consent flag cleared over an unreadable daemon lock, want kept")
	}
}
