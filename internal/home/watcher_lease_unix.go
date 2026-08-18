//go:build !windows

package home

import (
	"os/exec"
	"strconv"
)

// isProcessAlive checks whether a process with the given PID is running.
func isProcessAlive(pid int) bool {
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil
}
