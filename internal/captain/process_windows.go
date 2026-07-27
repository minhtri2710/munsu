//go:build windows

package captain

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

func configureWatcherProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
