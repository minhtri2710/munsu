//go:build !windows

package orchestrator

import (
	"os/exec"
	"syscall"
)

func configureWatcherProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
