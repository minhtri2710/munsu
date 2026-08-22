//go:build !windows

package orchestrator

import (
	"errors"
	"syscall"
)

// isProcessAlive checks whether a process with the given PID is running.
//
// The probe is a signal-0 kill rather than a shell-out to `kill`, because
// exec.Command depends on PATH and `kill` being unreachable failed exactly the
// way "no such process" does. Every caller reads false as permission to act:
// the watcher lock policy deletes the holder's lock file
// (home.acquireWatcherLock), the AFK identity lock is reclaimed (afk_lock.go),
// and the exit waits conclude the process is gone (afk_return.go,
// supervision_watcher.go). Under a narrowed PATH that collapse stole a live
// session's lock (#580).
//
// False therefore means one thing: the kernel reported ESRCH, no such process.
// EPERM -- the PID exists and is not ours to signal -- is alive, and so is any
// errno this does not model, because an unanswerable liveness question must
// leave the destructive branch shut.
func isProcessAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
