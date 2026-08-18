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
// This requests a SHARED lock, not an exclusive one, which is tracked as its
// own defect in #532 along with the two nextFence problems that make it
// unfixable in isolation. Deliberately not corrected here: #525 is the
// UnlockFileEx lpOverlapped bug, and adding the exclusive bit without #532's
// nextFence fix relocates the breakage rather than removing it.
//
// ERROR_LOCK_VIOLATION is the Windows counterpart of EWOULDBLOCK and is the
// only status the retry loop may spin on; anything else means LockFileEx is
// unusable here, which no amount of retrying fixes.
func lockScopedFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("LockFileEx").Call(
		uintptr(file.Fd()), uintptr(windows.LOCKFILE_FAIL_IMMEDIATELY), 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
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
