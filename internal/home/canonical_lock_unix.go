//go:build !windows

package home

import (
	"errors"
	"os"
	"syscall"
)

// lockScopedFile takes the exclusive flock without blocking: LOCK_NB is what
// makes a held lock return EWOULDBLOCK instead of parking the caller in the
// syscall, and the bounded retry loop in Home.Lock only runs at all because
// this returns.
//
// Only EWOULDBLOCK means "held right now, try again"; every other errno
// (EBADF, ENOLCK, EINVAL) is returned as itself, so the retry loop can refuse
// it immediately instead of spending the budget and blaming a timeout.
func lockScopedFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLockBusy
	}
	return err
}

func unlockScopedFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
