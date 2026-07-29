//go:build windows

package cli

import (
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

func configureWatchProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
func signalWatchProcess(process *os.Process) error { return process.Kill() }
func processIsAlive(process *os.Process) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.Pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == 259
}
