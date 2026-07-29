//go:build !windows

package home

import (
	"os"
	"syscall"
)

func lockWatcherFile(f *os.File, nonblock bool) error {
	flags := syscall.LOCK_EX
	if nonblock {
		flags |= syscall.LOCK_NB
	}
	return syscall.Flock(int(f.Fd()), flags)
}
func unlockWatcherFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
