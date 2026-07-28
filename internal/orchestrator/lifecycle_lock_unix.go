//go:build !windows

package orchestrator

import (
	"os"
	"syscall"
)

func lockExclusive(f *os.File, nonblock bool) error {
	flags := syscall.LOCK_EX
	if nonblock {
		flags |= syscall.LOCK_NB
	}
	return syscall.Flock(int(f.Fd()), flags)
}
func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
