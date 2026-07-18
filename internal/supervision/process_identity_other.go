//go:build !darwin && !linux

package supervision

import "fmt"

func processIdentity(pid int) (string, string, error) {
	return "", "", fmt.Errorf("process identity unsupported on this platform")
}
