package taskauthorityfs

import (
	"fmt"
	"os"
	"path/filepath"
)

// Lock order: every taskauthorityfs operation that takes more than one lock
// takes state/.dispatch.lock first and then state/<taskID>.meta.lock,
// releasing in reverse order. The dispatch lock is the shared coordination
// point with home's dispatch-control operations; the per-task lock matches
// home's per-task meta lock. withLocks is the only entry point that touches
// per-task locks, so no reverse acquisition order is reachable.

// dispatchLockPath returns the shared dispatch-control lock path.
func dispatchLockPath(homeDir string) string {
	return filepath.Join(homeDir, "state", ".dispatch.lock")
}

// taskLockPath returns the shared per-task meta lock path.
func taskLockPath(homeDir, taskID string) (string, error) {
	if err := validateTaskID(taskID); err != nil {
		return "", err
	}
	return filepath.Join(homeDir, "state", taskID+".meta.lock"), nil
}

// lockFile opens (creating if needed) path and acquires an exclusive advisory
// lock on it. The lock is released by releaseLock and automatically by the OS
// on process exit.
func lockFile(path string) (*os.File, error) {
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("creating lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, FilePerm)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	if err := lockExclusive(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquiring lock on %s: %w", path, err)
	}
	return f, nil
}

// releaseLock releases and closes a locked file.
func releaseLock(f *os.File) {
	_ = unlockFile(f)
	_ = f.Close()
}

// withDispatchLock runs fn while holding the shared dispatch-control lock.
// It is the base of every authority mutation: the dispatch lock serializes
// all authority transactions and dispatch-control writes across processes.
func withDispatchLock(homeDir string, fn func() error) error {
	f, err := lockFile(dispatchLockPath(homeDir))
	if err != nil {
		return err
	}
	defer releaseLock(f)
	return fn()
}

// withLocks runs fn under the canonical lock order: the dispatch lock, then
// the per-task lock, released in reverse order.
func withLocks(homeDir, taskID string, fn func() error) error {
	return withLocksOrdered(homeDir, taskID, lockFile, fn)
}

// withLocksOrdered is withLocks with an injectable acquisition function so
// tests can record the acquisition order deterministically. acquire must
// return an exclusively locked file or an error.
func withLocksOrdered(homeDir, taskID string, acquire func(string) (*os.File, error), fn func() error) error {
	taskPath, err := taskLockPath(homeDir, taskID)
	if err != nil {
		return err
	}
	dispatchFile, err := acquire(dispatchLockPath(homeDir))
	if err != nil {
		return err
	}
	defer releaseLock(dispatchFile)
	taskFile, err := acquire(taskPath)
	if err != nil {
		return err
	}
	defer releaseLock(taskFile)
	return fn()
}
