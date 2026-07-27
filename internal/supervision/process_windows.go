//go:build windows

package supervision

import "os/exec"

func configureWatcherProcess(*exec.Cmd) {}
