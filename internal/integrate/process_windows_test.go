//go:build windows

package integrate

import (
	"os/exec"
	"testing"
)

func TestWindowsProcessTreeTerminationFailsClosed(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit 0")
	setProcessIsolation(cmd)
	if err := killProcessTree(1); err == nil {
		t.Fatal("killProcessTree reported unsupported Windows process-tree termination as success")
	}
}
