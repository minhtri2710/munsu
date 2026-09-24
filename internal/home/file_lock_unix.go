//go:build !windows

package home

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on f. With nonblock, LOCK_NB makes a held
// lock return EWOULDBLOCK instead of parking the caller; that errno, and only
// that one, is reported as errLockBusy. Every other errno (EBADF, ENOLCK,
// EINVAL) is returned as itself: locking is broken, not busy.
func lockFile(f *os.File, nonblock bool) error {
	how := syscall.LOCK_EX
	if nonblock {
		how |= syscall.LOCK_NB
	}
	err := syscall.Flock(int(f.Fd()), how)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLockBusy
	}
	return err
}

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
