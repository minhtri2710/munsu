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
// LOCKFILE_EXCLUSIVE_LOCK is the counterpart of LOCK_EX and must ride along:
// without that bit LockFileEx requests a SHARED lock, and a shared lock
// excludes nothing — two holders both enter the critical section and neither
// ever sees ERROR_LOCK_VIOLATION for the other, so the retry loop still never
// runs, now over a real race instead of a parked syscall.
//
// ERROR_LOCK_VIOLATION is the Windows counterpart of EWOULDBLOCK and is the
// only status the retry loop may spin on; anything else means LockFileEx is
// unusable here, which no amount of retrying fixes.
func lockScopedFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("LockFileEx").Call(
		uintptr(file.Fd()), uintptr(windows.LOCKFILE_FAIL_IMMEDIATELY|windows.LOCKFILE_EXCLUSIVE_LOCK), 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
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
