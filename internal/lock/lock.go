// Package lock provides per-home session file locking using flock.
package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const lockFile = "state/.lock"

// Acquire attempts to acquire an exclusive file lock for the given munsu home.
// Returns true if the lock was acquired, false if it is held by another process.
func Acquire(home string) (bool, error) {
	path := filepath.Join(home, lockFile)

	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("creating lock directory %s: %w", dir, err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return false, fmt.Errorf("opening lock file %s: %w", path, err)
	}

	// Try exclusive lock without blocking
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return false, nil
	}

	// Write our PID into the lock file for diagnostics
	fmt.Fprintf(f, "%d\n", os.Getpid())

	// Leak the FD intentionally — it's held for the lifetime of the session.
	// Release() will close it.
	return true, nil
}

// Release releases the session lock for the given home.
func Release(home string) error {
	path := filepath.Join(home, lockFile)

	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No lock to release
		}
		return fmt.Errorf("opening lock file %s: %w", path, err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlocking %s: %w", path, err)
	}

	return nil
}

// IsLocked checks whether the lock is held by another process.
func IsLocked(home string) bool {
	path := filepath.Join(home, lockFile)

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return false
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// Lock would block — someone else holds it
		return true
	}

	// We acquired it — release immediately since we were just checking
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
