//go:build !windows

package home

import (
	"errors"
	"syscall"
)

// IsProcessAlive checks whether a process with the given PID is running. It is
// the one liveness probe in the tree; orchestrator calls it too.
//
// The probe is a signal-0 kill rather than a shell-out to `kill`, because
// exec.Command depends on PATH and `kill` being unreachable failed exactly the
// way "no such process" does. Every caller reads false as permission to act:
// ClaimWatcherLease reclaims the lease of the reported-dead holder, which is
// the singleton watcher guard granting itself away; IsWatcherLeaseHealthy
// declares the lease unhealthy; and orchestrator's exit waits (afk_return.go,
// supervision_watcher.go) conclude the process is gone. Under a narrowed PATH
// that collapse reported every holder dead (#580).
//
// False therefore means one thing: the kernel reported ESRCH, no such process.
// EPERM -- the PID exists and is not ours to signal -- is alive, and so is any
// errno this does not model, because an unanswerable liveness question must
// leave the destructive branch shut.
func IsProcessAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
