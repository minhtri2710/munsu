//go:build windows

package backend

import "os/exec"

func configureBackendCommand(cmd *exec.Cmd) {}

func killBackendCommand(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
