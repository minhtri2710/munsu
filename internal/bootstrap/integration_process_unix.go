//go:build !windows

package bootstrap

import (
	"os/exec"
	"syscall"
)

func setProcessIsolation(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
func killProcessTree(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }
