//go:build windows

package home

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errScopedLockUnavailable = errors.New("Windows scoped file locking unavailable")

// lockScopedFile takes an exclusive byte-range lock on the whole file without
// blocking. On Windows, flock is unavailable, so LockFileEx is used, matching
// the repo's existing watcher lock implementation. LOCKFILE_FAIL_IMMEDIATELY is
// the counterpart of LOCK_NB: without it LockFileEx parks the caller until the
// holder releases, and the bounded retry loop in Home.Lock never runs.
//
// This requests an EXCLUSIVE lock: LOCKFILE_EXCLUSIVE_LOCK is the windows
// counterpart of the unix sibling's LOCK_EX, and without it LockFileEx requests
// a SHARED lock, which denies write access to all processes including this one
// -- and nextFence (canonical_lock.go) reads and rewrites the fence while the
// lock is held. Added here together with the nextFence second-handle fix in
// #532; either alone relocates the breakage rather than removing it.
//
// ERROR_LOCK_VIOLATION is the Windows counterpart of EWOULDBLOCK and is the
// only status the retry loop may spin on; anything else means LockFileEx is
// unusable here, which no amount of retrying fixes.
func lockScopedFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("LockFileEx").Call(
		uintptr(file.Fd()), uintptr(windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY), 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
	if ret == 0 {
		if callErr == windows.ERROR_LOCK_VIOLATION {
			return errLockBusy
		}
		return errors.Join(errScopedLockUnavailable, callErr)
	}
	return nil
}

func unlockScopedFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("UnlockFileEx").Call(
		uintptr(file.Fd()), 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
	if ret == 0 {
		return errors.Join(errScopedLockUnavailable, callErr)
	}
	return nil
}
