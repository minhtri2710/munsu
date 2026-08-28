//go:build !windows

package testutil

import (
	"os"
	"syscall"
)

// IsProcessAlive reports whether a process with the given PID is running.
func IsProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
