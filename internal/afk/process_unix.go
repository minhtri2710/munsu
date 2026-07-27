//go:build !windows

package afk

import "syscall"

func stopProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
