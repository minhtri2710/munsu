//go:build windows

package bootstrap

import "os/exec"

func setProcessIsolation(*exec.Cmd) {}

// killProcessTree is a no-op: Windows has no process group to signal here, so a
// timeout reaps only the direct child that exec.CommandContext kills.
func killProcessTree(int) {}
