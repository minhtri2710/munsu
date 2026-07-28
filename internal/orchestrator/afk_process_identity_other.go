//go:build !darwin && !linux

package orchestrator

import "fmt"

func processIdentity(pid int) (string, string, error) {
	return "", "", fmt.Errorf("AFK process identity unsupported for PID %d", pid)
}
