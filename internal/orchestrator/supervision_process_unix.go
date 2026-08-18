//go:build !windows

package orchestrator

import (
	"os"
	"os/exec"
	"syscall"
)

func configureWatcherProcess(cmd *exec.Cmd)          { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func signalWatcherProcess(process *os.Process) error { return process.Signal(syscall.SIGTERM) }
