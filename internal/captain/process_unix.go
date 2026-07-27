//go:build !windows

package captain

import (
	"os/exec"
	"syscall"
)

func configureWatcherProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
