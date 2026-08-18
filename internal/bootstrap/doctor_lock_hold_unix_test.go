//go:build !windows

package bootstrap

import (
	"os"
	"syscall"
	"testing"
)

// holdConvergeLock takes an exclusive non-blocking flock on lockPath and
// returns a release function. It creates the state a running converge holds,
// so doctor tests can assert that a held lock reads as converge-in-progress
// and never gets an rm -f repair.
func holdConvergeLock(t *testing.T, lockPath string) func() {
	t.Helper()
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		t.Fatalf("flock converge lock: %v", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}
