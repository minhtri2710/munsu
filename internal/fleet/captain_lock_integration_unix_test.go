//go:build integration && !windows

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests encode the all-platform removal contract, which is only true on
// Unix. On Windows the release closure never reaches os.Remove (see
// acquireExclusiveLock), so the lock file survives release, and the
// os.Rename / os.ReadFile steps below would fail on Windows before they could
// assert anything — they belong to the platform where the contract holds.

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

	// Release.
	release()

	// Lock file should be removed.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file was not removed after release")
	}
}

func TestAcquireExclusiveLock_OldReleasePreservesReplacement(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")
	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(lockPath, lockPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte(strings.Repeat("a", 64)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("old generation release removed replacement lock: %v", err)
	}
}

func TestAcquireExclusiveLock_TokenGeneration(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquireExclusiveLock error: %v", err)
	}

	// Lock file should exist with hex token content.
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.TrimSpace(string(data))
	if len(content) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("expected 64 hex chars, got %d: %q", len(content), content)
	}

	// Release should clean up.
	release()
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed after release")
	}
}

func TestAcquireExclusiveLock_NoRemoveOnFailure(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release1, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Create a file with lock-like content to simulate the file still existing.
	// Write a marker so we can detect if it's removed.
	os.WriteFile(lockPath, []byte("other-content\n"), 0644)

	// Second acquire should fail (LOCK_NB) but NOT remove the file.
	_, err = acquireExclusiveLock(lockPath)
	if err == nil {
		t.Fatal("expected second acquire to fail")
	}

	// The file should still exist with its original content (not removed).
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal("lock file was removed — bug: os.Remove on LOCK_NB failure")
	}
	if string(data) != "other-content\n" {
		t.Errorf("lock file content changed: %q", string(data))
	}

	release1()
}
