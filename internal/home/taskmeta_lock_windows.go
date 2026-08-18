//go:build windows

package home

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errLockUnavailable = errors.New("Windows file locking unavailable")

func lockExclusive(file *os.File) error {
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("LockFileEx").Call(
		uintptr(file.Fd()), 0, 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
	if ret == 0 {
		if callErr == windows.ERROR_LOCK_VIOLATION {
			return os.ErrPermission
		}
		return errors.Join(errLockUnavailable, callErr)
	}
	return nil
}

func unlockFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("UnlockFileEx").Call(
		uintptr(file.Fd()), 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
	if ret == 0 {
		return errors.Join(errLockUnavailable, callErr)
	}
	return nil
}
