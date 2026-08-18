//go:build !windows

package home

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestLockScopedFileClassifiesBusyApartFromBrokenLocking pins the distinction
// Home.Lock's retry loop depends on: EWOULDBLOCK is "held right now, spin
// again", and every other errno is returned as itself so the loop can refuse
// it at once. Reporting a broken descriptor as ErrLockTimeout after burning
// the whole budget would name the wrong cause on a host where file locking is
// unusable.
func TestLockScopedFileClassifiesBusyApartFromBrokenLocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scope.lock")

	held, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := lockScopedFile(held); err != nil {
		t.Fatalf("first lock on a free scope: %v", err)
	}
	defer unlockScopedFile(held)

	contender, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	if err := lockScopedFile(contender); !errors.Is(err, errLockBusy) {
		t.Errorf("lock on a held scope: got %v, want errLockBusy", err)
	}

	// A closed descriptor is EBADF: locking is not busy here, it is broken,
	// and retrying it until the budget runs out would never succeed.
	closed, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	fd := closed.Fd()
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	broken := os.NewFile(fd, path)
	err = lockScopedFile(broken)
	if errors.Is(err, errLockBusy) {
		t.Fatalf("lock on a closed descriptor reported busy, so the retry loop would spin the full budget: %v", err)
	}
	if !errors.Is(err, syscall.EBADF) {
		t.Errorf("lock on a closed descriptor: got %v, want EBADF passed through", err)
	}
}
