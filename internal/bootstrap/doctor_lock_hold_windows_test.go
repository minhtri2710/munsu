//go:build windows

package bootstrap

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// holdConvergeLock takes an exclusive non-blocking LockFileEx on lockPath and
// returns a release function. It creates the state a running converge holds,
// so doctor tests can assert that a held lock reads as converge-in-progress
// and never gets an rm -f repair.
func holdConvergeLock(t *testing.T, lockPath string) func() {
	t.Helper()
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatal(err)
	}
	overlapped := new(windows.Overlapped)
	ret, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("LockFileEx").Call(
		uintptr(f.Fd()), uintptr(windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY), 0,
		^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
	if ret == 0 {
		f.Close()
		t.Fatalf("LockFileEx converge lock: %v", callErr)
	}
	return func() {
		windows.NewLazySystemDLL("kernel32.dll").NewProc("UnlockFileEx").Call(
			uintptr(f.Fd()), 0, ^uintptr(0), ^uintptr(0), uintptr(unsafe.Pointer(overlapped)))
		f.Close()
	}
}
