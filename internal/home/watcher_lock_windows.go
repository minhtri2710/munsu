//go:build windows

package home

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errWatcherLockUnavailable = errors.New("Windows file locking unavailable")

func lockWatcherFile(file *os.File, nonblock bool) error {
	overlapped := new(windows.Overlapped)
	flags := uint32(0)
	if nonblock {
		flags = windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("LockFileEx").Call(
		uintptr(file.Fd()), uintptr(flags), 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
	if ret == 0 {
		if callErr == windows.ERROR_LOCK_VIOLATION {
			return os.ErrPermission
		}
		return errors.Join(errWatcherLockUnavailable, callErr)
	}
	return nil
}

func unlockWatcherFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("UnlockFileEx").Call(
		uintptr(file.Fd()), 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
	if ret == 0 {
		return errors.Join(errWatcherLockUnavailable, callErr)
	}
	return nil
}
