//go:build !windows

package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// TestProcessAliveAnswersWithoutPATH pins #580 at the probe. The e2e lifecycle
// helper narrows PATH to <stubdir>:<dir of git>, and under it the shell-out
// probe could not run `kill` at all -- so it reported the asking process's own
// PID dead. A liveness probe must not have a PATH dependency, because every
// caller reads false as permission to act destructively.
func TestProcessAliveAnswersWithoutPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if !isProcessAlive(os.Getpid()) {
		t.Error("the calling process reads as dead when PATH cannot resolve `kill`")
	}
	// Control: the probe still says dead when the kernel says no such process,
	// so the assertion above is not satisfied by a probe that answers true for
	// everything. Asking the kernel directly is the whole point -- a control
	// that defined "not running" as !isProcessAlive would agree with any
	// answer the function under test gave.
	if pid, ok := unusedPID(); ok {
		if isProcessAlive(pid) {
			t.Errorf("PID %d is not running but reads as alive", pid)
		}
	} else {
		t.Fatal("the kernel supplied no unused PID, so the dead-PID control could not run")
	}
}

// TestProcessAliveTreatsAnUnsignallablePIDAsAlive covers the other half of the
// collapse: EPERM means the process exists and is not ours, which is the one
// answer that must never reach a caller as "dead".
func TestProcessAliveTreatsAnUnsignallablePIDAsAlive(t *testing.T) {
	pid := unsignallablePID(t)
	if err := syscall.Kill(pid, 0); err != syscall.EPERM {
		t.Skipf("EPERM proof did not run: PID %d no longer returns EPERM from raw syscall.Kill(pid, 0): %v", pid, err)
	}
	if !isProcessAlive(pid) {
		t.Errorf("PID %d is unsignallable but reads as dead", pid)
	}
}

func unsignallablePID(t *testing.T) int {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("EPERM proof did not run: tests run as root, so no foreign unsignallable process can be established")
	}

	var pids []int
	var discoveryErrors []string
	if entries, err := os.ReadDir("/proc"); err == nil {
		for _, entry := range entries {
			if pid, err := strconv.Atoi(entry.Name()); err == nil && pid > 0 {
				pids = append(pids, pid)
			}
		}
	} else {
		discoveryErrors = append(discoveryErrors, "read /proc: "+err.Error())
	}
	if len(pids) == 0 {
		output, err := exec.Command("/bin/ps", "-e", "-o", "pid=").Output()
		if err != nil {
			discoveryErrors = append(discoveryErrors, "run ps: "+err.Error())
		} else {
			for _, line := range strings.Split(string(output), "\n") {
				if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
					pids = append(pids, pid)
				}
			}
		}
	}
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == syscall.EPERM {
			return pid
		}
	}
	message := "EPERM proof did not run: checked %d candidate PIDs with raw syscall.Kill(pid, 0), but none returned EPERM"
	if len(discoveryErrors) > 0 {
		message += " (" + strings.Join(discoveryErrors, "; ") + ")"
	}
	t.Skipf(message, len(pids))
	return 0
}

// TestSessionLockIsNotStolenWhenLivenessIsUnprobable builds the state #580
// reported end to end: a live holder's PID in state/.lock, and a PATH under
// which the shell-out probe answered "dead". acquireWatcherLock then removed
// the file, and the next O_CREATE handed the second acquirer a fresh inode to
// flock while the holder's flock stayed on the orphan. The lock file must
// survive as the same inode and the second acquire must be refused.
func TestSessionLockIsNotStolenWhenLivenessIsUnprobable(t *testing.T) {
	h := t.TempDir()
	p := home.SessionLockPath(h)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	holder, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("opening lock file: %v", err)
	}
	defer holder.Close()
	// flock is held per open file description, so this conflicts with the
	// descriptor AcquireSession opens even though both live in this process.
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("locking lock file: %v", err)
	}
	if _, err := fmt.Fprintf(holder, "%d\n", os.Getpid()); err != nil {
		t.Fatalf("writing holder PID: %v", err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}

	t.Setenv("PATH", t.TempDir())

	ok, err := AcquireSession(h)
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	if ok {
		t.Error("AcquireSession took the session lock while a live holder held it")
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatalf("a live holder's lock file was removed: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Error("a live holder's lock file was replaced by a fresh inode")
	}
}

// unusedPID returns a PID the kernel reports as not running, and whether one
// was found. It probes a few high candidates rather than scanning, because
// there is no PID a test is entitled to assume is free.
//
// A miss is returned to the caller so the caller can fail without skipping the
// enclosing test and cancelling assertions that need no dead PID.
func unusedPID() (int, bool) {
	for _, pid := range []int{1 << 22, 1<<22 - 1, 1<<22 - 2, 1 << 21, 1<<21 - 1} {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return pid, true
		}
	}
	return 0, false
}
