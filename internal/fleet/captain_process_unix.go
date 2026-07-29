//go:build !windows

package fleet

import (
	"os/exec"
	"syscall"
)

func configureWatcherProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
