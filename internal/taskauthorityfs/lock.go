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

// lockFile opens (creating if needed) path, re-secures it to FilePerm, and
// acquires an exclusive advisory lock on it. The lock is released by
// releaseLock and automatically by the OS on process exit. Re-securing every
// acquisition closes the window where a pre-existing lock file (or a new one
// narrowed by a umask) stays wider than 0600.
func lockFile(path string) (*os.File, error) {
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("creating lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, FilePerm)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	if err := os.Chmod(path, FilePerm); err != nil {
		f.Close()
		return nil, fmt.Errorf("securing lock file %s: %w", path, err)
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

// stateDirSafe ensures homeDir/state exists and is a real directory, never
// a symlink. Lock files live under state/; acquiring a lock through a
// symlinked state would place the lock file outside the home, so lock
// acquisition fails closed on a linked or non-directory state. homeDir is
// the trust boundary and is created (or OS-resolved) exactly as the caller
// named it.
func stateDirSafe(homeDir string) error {
	state := filepath.Join(homeDir, "state")
	info, err := os.Lstat(state)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("inspecting lock directory %s: %w", state, err)
		}
		if err := os.MkdirAll(homeDir, DirPerm); err != nil {
			return fmt.Errorf("creating lock home %s: %w", homeDir, err)
		}
		if err := os.Mkdir(state, DirPerm); err != nil {
			return fmt.Errorf("creating lock directory %s: %w", state, err)
		}
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("lock directory %s is a symlink or not a directory", state)
	}
	return nil
}

// withDispatchLock runs fn while holding the shared dispatch-control lock.
// It is the base of every authority mutation: the dispatch lock serializes
// all authority transactions and dispatch-control writes across processes.
func withDispatchLock(homeDir string, fn func() error) error {
	if err := stateDirSafe(homeDir); err != nil {
		return err
	}
	f, err := lockFile(dispatchLockPath(homeDir))
	if err != nil {
		return err
	}
	defer releaseLock(f)
	return fn()
}

// stateDirExists reports whether homeDir/state exists at all, without
// creating or following anything. Every v1 and v2 authority record and every
// transaction manifest lives under state/, so a missing state/ means the
// home carries no authority state of any kind. The check deliberately does
// not read through the directory: a symlinked state/ is reported as existing
// and later fails closed exactly as today.
func stateDirExists(homeDir string) (bool, error) {
	_, err := os.Lstat(filepath.Join(homeDir, "state"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// withDispatchLockRead runs fn while holding the shared dispatch-control
// lock, exactly like withDispatchLock, except that a home with no state/
// directory at all has nothing to lock: fn is not run, nil is returned, and
// the caller serves the empty committed view. A missing state/ cannot hide
// records or interrupted transactions, because every v1 and v2 authority
// record lives under state/. Taking the lock here would create state/ and
// state/.dispatch.lock, mutating a home that a pure read must leave
// untouched. Any home that has state/ — including a symlinked or
// non-directory state — takes the lock exactly as today and fails closed.
func withDispatchLockRead(homeDir string, fn func() error) error {
	exists, err := stateDirExists(homeDir)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return withDispatchLock(homeDir, fn)
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
