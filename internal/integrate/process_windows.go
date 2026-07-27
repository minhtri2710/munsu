//go:build windows

package integrate

import (
	"errors"
	"os/exec"
)

func setProcessIsolation(*exec.Cmd) {}
func killProcessTree(int) error {
	return errors.New("process-tree termination capability unavailable on Windows")
}
