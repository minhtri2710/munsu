//go:build !windows

package home

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestProcessAliveAnswersWithoutPATH pins the lease half of #580 at the probe.
// The shell-out form could not run `kill` under a narrowed PATH, so it reported
// every PID dead -- and ClaimWatcherLease reads that as "reclaim", which is the
// singleton watcher guard granting itself away.
func TestProcessAliveAnswersWithoutPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if !isProcessAlive(os.Getpid()) {
		t.Error("the calling process reads as dead when PATH cannot resolve `kill`")
	}
	// Control: a PID the kernel reports as not running still reads as dead.
	// findDeadPIDForGuards would not do here -- it defines "not running" as
	// !isProcessAlive, so it agrees with any answer the function under test
	// gives, and under an always-alive probe it finds no candidate and skips
	// the assertion above along with itself.
	if pid, ok := unusedPID(); ok {
		if isProcessAlive(pid) {
			t.Errorf("PID %d is not running but reads as alive", pid)
		}
	} else {
		t.Log("every candidate PID is in use on this machine, so the dead-PID control did not run")
	}
}

// unusedPID returns a PID the kernel reports as not running, and whether one
// was found. It asks the kernel rather than isProcessAlive so that it can
// discriminate against the function under test, and it probes a few high
// candidates rather than scanning, because there is no PID a test is entitled
// to assume is free.
//
// It reports the miss instead of calling t.Skip: a skip would cancel the whole
// enclosing test, including the assertions that need no dead PID at all, and a
// silent green is exactly what this file exists to prevent.
//
// findDeadPIDForGuards (canonical_guards_test.go) stays as it is. That file
// carries no build constraint, so it compiles under `GOOS=windows go vet`,
// where syscall.Kill does not exist.
func unusedPID() (int, bool) {
	for _, pid := range []int{1 << 22, 1<<22 - 1, 1<<22 - 2, 1 << 21, 1<<21 - 1} {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return pid, true
		}
	}
	return 0, false
}

// TestProcessAliveTreatsAnUnsignallablePIDAsAlive covers the other half of the
// collapse #580 named: EPERM means the process exists and is not ours to
// signal, which is the one answer that must never reach a caller as "dead".
// The PATH tests above do not reach it -- they exercise the probe's mechanism,
// not the errno it has to discriminate.
func TestProcessAliveTreatsAnUnsignallablePIDAsAlive(t *testing.T) {
	pid := unsignallablePID(t)
	if err := syscall.Kill(pid, 0); err != syscall.EPERM {
		t.Skipf("EPERM proof did not run: PID %d no longer returns EPERM from raw syscall.Kill(pid, 0): %v", pid, err)
	}
	if !isProcessAlive(pid) {
		t.Errorf("PID %d is unsignallable but reads as dead", pid)
	}
}

// TestClaimWatcherLeaseRefusesAnUnsignallableHolder is that answer's
// consequence, and it is the state the original defect produced: a lease held
// by a live PID belonging to another user is reclaimed, and two watchers run
// over one home. The lease layer would still read like a singleton guard while
// having stopped being one.
func TestClaimWatcherLeaseRefusesAnUnsignallableHolder(t *testing.T) {
	pid := unsignallablePID(t)
	if err := syscall.Kill(pid, 0); err != syscall.EPERM {
		t.Skipf("EPERM proof did not run: PID %d no longer returns EPERM from raw syscall.Kill(pid, 0): %v", pid, err)
	}
	dir := t.TempDir()
	if _, err := writeLeaseFile(WatcherLeasePath(dir), &WatcherLease{
		Home:      Canonical(dir),
		PID:       pid,
		StartedAt: time.Now().Unix(),
		UpdatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("write existing lease: %v", err)
	}
	claimed, err := ClaimWatcherLease(dir, os.Getpid())
	if claimed || err == nil {
		t.Fatalf("ClaimWatcherLease over a live holder this process may not signal = (%v, %v), want the live-holder refusal", claimed, err)
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

// TestClaimWatcherLeaseRefusesALiveHolderWithoutPATH is the consequence of the
// probe above: two watchers over one home, which is exactly what the lease
// exists to prevent.
func TestClaimWatcherLeaseRefusesALiveHolderWithoutPATH(t *testing.T) {
	dir := t.TempDir()

	// Started before PATH is narrowed -- exec.Command resolves the binary at
	// Start, and the narrowing is about the probe, not about spawning.
	live := exec.Command("sleep", "60")
	if err := live.Start(); err != nil {
		t.Fatalf("start live process: %v", err)
	}
	t.Cleanup(func() {
		_ = live.Process.Kill()
		_ = live.Wait()
	})

	if _, err := writeLeaseFile(WatcherLeasePath(dir), &WatcherLease{
		Home:      Canonical(dir),
		PID:       live.Process.Pid,
		StartedAt: time.Now().Unix(),
		UpdatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("write existing lease: %v", err)
	}

	t.Setenv("PATH", t.TempDir())

	claimed, err := ClaimWatcherLease(dir, os.Getpid())
	if claimed || err == nil {
		t.Fatalf("ClaimWatcherLease over a live holder = (%v, %v), want the live-holder refusal", claimed, err)
	}
}
