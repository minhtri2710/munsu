//go:build windows

package home

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errScopedLockUnavailable = errors.New("Windows scoped file locking unavailable")

// lockScopedFile takes an exclusive byte-range lock on the whole file. On
// Windows, flock is unavailable, so LockFileEx is used, matching the repo's
// existing watcher lock implementation.
func lockScopedFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("LockFileEx").Call(
		uintptr(file.Fd()), 0, 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
	if ret == 0 {
		return errors.Join(errScopedLockUnavailable, callErr)
	}
	return nil
}

func unlockScopedFile(file *os.File) error {
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("UnlockFileEx").Call(
		uintptr(file.Fd()), 0, ^uintptr(0), ^uintptr(0), 0)
	if ret == 0 {
		return errors.Join(errScopedLockUnavailable, callErr)
	}
	return nil
}
