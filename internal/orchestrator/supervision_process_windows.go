//go:build windows

package orchestrator

import (
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

func configureWatcherProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
func signalWatcherProcess(process *os.Process) error { return process.Kill() }
