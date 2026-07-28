//go:build windows

package orchestrator

import "os/exec"

func configureWatcherProcess(*exec.Cmd) {}
