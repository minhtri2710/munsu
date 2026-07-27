//go:build windows

package cli

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureWatchProcessCreatesIndependentGroup(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	configureWatchProcess(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("SysProcAttr=%+v", cmd.SysProcAttr)
	}
}
