//go:build windows

package taskauthorityfs

import (
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

var errLockUnavailable = errors.New("Windows file locking unavailable")

// lockOverlapped retains the OVERLAPPED each exclusive lock was acquired
// with, keyed by file descriptor. LockFileEx binds the lock to the byte
// range carried in the OVERLAPPED, and UnlockFileEx must be passed the same
// OVERLAPPED with the same range; keeping the structure alive across the two
// calls is required for a correct unlock. releaseLock always removes the
// entry, and process exit drops the registry together with the OS-released
// locks.
var (
	overlappedMu   sync.Mutex
	overlappedByFD = map[uintptr]*windows.Overlapped{}
)

func lockExclusive(file *os.File) error {
	overlapped := new(windows.Overlapped)
	// LOCKFILE_EXCLUSIVE_LOCK is required: without it LockFileEx takes a
	// shared lock, which would not serialize writers.
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, ^uint32(0), ^uint32(0), overlapped); err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return os.ErrPermission
		}
		return errors.Join(errLockUnavailable, err)
	}
	overlappedMu.Lock()
	overlappedByFD[file.Fd()] = overlapped
	overlappedMu.Unlock()
	return nil
}

func unlockFile(file *os.File) error {
	fd := file.Fd()
	overlappedMu.Lock()
	overlapped := overlappedByFD[fd]
	delete(overlappedByFD, fd)
	overlappedMu.Unlock()
	if overlapped == nil {
		return errLockUnavailable
	}
	if err := windows.UnlockFileEx(windows.Handle(fd), 0, ^uint32(0), ^uint32(0), overlapped); err != nil {
		return errors.Join(errLockUnavailable, err)
	}
	return nil
}
