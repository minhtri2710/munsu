//go:build !windows

package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func makePathUnreadable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("MakePathUnreadable: stat %s: %v", path, err)
	}
	originalMode := info.Mode().Perm()
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatalf("MakePathUnreadable: chmod 0000 %s: %v", path, err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, originalMode)
	})

	// Verify the path is genuinely unreadable.
	if info.IsDir() {
		if _, err := os.ReadDir(path); err == nil {
			t.Fatalf("MakePathUnreadable: directory %s remains readable after chmod 0000", path)
		}
	} else {
		if f, err := os.Open(path); err == nil {
			f.Close()
			t.Fatalf("MakePathUnreadable: file %s remains readable after chmod 0000", path)
		}
	}
}

func makeDirectoryReadOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("MakeDirectoryReadOnly: stat %s: %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("MakeDirectoryReadOnly: %s is not a directory", path)
	}
	originalMode := info.Mode().Perm()
	if err := os.Chmod(path, 0500); err != nil {
		t.Fatalf("MakeDirectoryReadOnly: chmod 0500 %s: %v", path, err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, originalMode)
	})

	// Verify write is refused.
	probe := filepath.Join(path, ".test_write_probe")
	if f, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE, 0600); err == nil {
		f.Close()
		_ = os.Remove(probe)
		t.Fatalf("MakeDirectoryReadOnly: directory %s remains writable after chmod 0500", path)
	}
}

func assertOwnerPrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("AssertOwnerPrivate: stat %s: %v", path, err)
	}
	perm := info.Mode().Perm()
	if info.IsDir() {
		if perm != 0700 {
			t.Fatalf("AssertOwnerPrivate: directory %s permissions = %04o, want 0700", path, perm)
		}
	} else {
		if perm != 0600 {
			t.Fatalf("AssertOwnerPrivate: file %s permissions = %04o, want 0600", path, perm)
		}
	}
	if perm&0o77 != 0 {
		t.Fatalf("AssertOwnerPrivate: %s grants group/other access (%04o)", path, perm)
	}
}

func assertOwnerReadOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("AssertOwnerReadOnly: stat %s: %v", path, err)
	}
	perm := info.Mode().Perm()
	if info.IsDir() {
		if perm != 0500 {
			t.Fatalf("AssertOwnerReadOnly: directory %s permissions = %04o, want 0500", path, perm)
		}
	} else {
		if perm != 0400 {
			t.Fatalf("AssertOwnerReadOnly: file %s permissions = %04o, want 0400", path, perm)
		}
	}
}
