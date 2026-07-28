//go:build !windows

package orchestrator

import "syscall"

func stopProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
