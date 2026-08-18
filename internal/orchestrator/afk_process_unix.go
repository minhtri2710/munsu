//go:build !windows

package orchestrator

import "syscall"

// stopProcessIsLossy reports whether stopProcess can lose daemon state that a
// graceful shutdown would have written. On unix it cannot: stopProcess is
// SIGTERM, the daemon catches it (afk_daemon.go step 6) and flushes the digest
// window in its deferred shutdown before the process ends. True on windows,
// where Kill is TerminateProcess and that flush never runs
// (afk_process_windows.go). Return consults it to decide whether the stop
// must be surfaced as lossy (afk_return.go).
func stopProcessIsLossy() bool { return false }

func stopProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
