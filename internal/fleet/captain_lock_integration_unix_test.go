//go:build integration && !windows

package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests pin the Unix flock contract: the lock pathname remains the
// same flock domain across release and re-acquisition.

func TestAcquireExclusiveLock(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquireExclusiveLock error: %v", err)
	}
	if release == nil {
		t.Fatal("expected non-nil release function")
	}

	// Lock file should exist.
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("lock file was not created")
	}

	// Release leaves the permanent flock pathname in place.
	release()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file disappeared after release: %v", err)
	}
}

func TestAcquireExclusiveLock_ReleasesWithoutUnlinking(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquireExclusiveLock error: %v", err)
	}

	// The lock file is a permanent flock domain and is reused across releases.
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatal(err)
	}

	release()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should survive release: %v", err)
	}
}

func TestAcquireExclusiveLock_TwoHolderInterleaving(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release1, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	firstInfo, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	release1()

	release2, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("second acquire after release: %v", err)
	}
	defer release2()
	secondInfo, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("second holder acquired a different lock file after the first holder released")
	}

	third, err := os.OpenFile(lockPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	acquired, err := tryLockFile(third)
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Fatal("third holder acquired the lock while second holder was active")
	}
}

func TestAcquireExclusiveLock_RefusalPreservesLockFile(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release1, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release1()

	if _, err := acquireExclusiveLock(lockPath); err == nil {
		t.Fatal("expected second acquire to fail")
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("failed acquisition removed the lock file: %v", err)
	}
}
