//go:build !windows

package home

import (
	"os"
	"syscall"
)

// lockScopedFile takes the exclusive flock without blocking: LOCK_NB is what
// makes a held lock return EWOULDBLOCK instead of parking the caller in the
// syscall, and the bounded retry loop in Home.Lock only runs at all because
// this returns.
func lockScopedFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockScopedFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
