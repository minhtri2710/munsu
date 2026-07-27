//go:build windows

package captain

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func tryLockFile(file *os.File) (bool, error) {
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("LockFileEx").Call(
		uintptr(file.Fd()), uintptr(windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY), 0,
		^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
	if ret != 0 {
		return true, nil
	}
	if callErr == windows.ERROR_LOCK_VIOLATION {
		return false, nil
	}
	return false, errors.Join(errors.New("LockFileEx failed"), callErr)
}

func unlockFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("UnlockFileEx").Call(
		uintptr(file.Fd()), 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
	if ret != 0 {
		return nil
	}
	return errors.Join(errors.New("UnlockFileEx failed"), callErr)
}
