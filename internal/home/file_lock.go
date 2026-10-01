package home

import "fmt"

// WithExclusiveFileLock runs fn while holding an exclusive lock on path (created if absent).
// It fails closed: when the lock cannot be taken, fn does not run and the error is returned.
func WithExclusiveFileLock(path string, fn func() error) error {
	f, err := openLockFile(path, 0600)
	if err != nil {
		return fmt.Errorf("opening lock file: %w", err)
	}
	defer f.Close()
	if err := lockFile(f, false); err != nil {
		return fmt.Errorf("acquiring lock %s: %w", path, err)
	}
	defer unlockFile(f)
	return fn()
}
