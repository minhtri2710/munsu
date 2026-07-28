//go:build !darwin && !linux

package orchestrator

import "fmt"

func processIdentity(pid int) (string, string, error) {
	return "", "", fmt.Errorf("process identity unsupported on this platform")
}
