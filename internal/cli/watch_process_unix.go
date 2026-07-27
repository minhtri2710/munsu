//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

func configureWatchProcess(cmd *exec.Cmd)          { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func signalWatchProcess(process *os.Process) error { return process.Signal(syscall.SIGTERM) }
func processIsAlive(process *os.Process) bool      { return process.Signal(syscall.Signal(0)) == nil }
