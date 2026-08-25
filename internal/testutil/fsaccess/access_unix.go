//go:build !windows

package fsaccess

import (
	"fmt"
	"os"
	"testing"
)

// MakeUnreadable removes read and search access for the current user and
// verifies that the path can no longer be read. Access is restored at cleanup.
func MakeUnreadable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat unreadable path %q: %v", path, err)
	}
	old := info.Mode().Perm()
	if err := os.Chmod(path, old&^0o500); err != nil {
		t.Fatalf("make path unreadable %q: %v", path, err)
	}
	restore := registerRestore(t, func() error { return os.Chmod(path, old) })
	if err := verifyUnreadable(path, info.IsDir()); err != nil {
		_ = restore()
		t.Fatalf("unreadable path %q was still readable: %v", path, err)
	}
}

// MakeReadOnly removes write access while preserving read/search access and
// verifies that a write operation is refused by the filesystem.
func MakeReadOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat read-only path %q: %v", path, err)
	}
	old := info.Mode().Perm()
	if err := os.Chmod(path, old&^0o200); err != nil {
		t.Fatalf("make path read-only %q: %v", path, err)
	}
	restore := registerRestore(t, func() error { return os.Chmod(path, old) })
	if info.IsDir() {
		probe := path + "/.fsaccess-write-probe"
		if f, err := os.Create(probe); err == nil {
			f.Close()
			_ = os.Remove(probe)
			_ = restore()
			t.Fatalf("read-only directory %q remained writable", path)
		}
	} else {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err == nil {
			f.Close()
			_ = restore()
			t.Fatalf("read-only file %q remained writable", path)
		}
	}
}

func AssertPrivateFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat private file %q: %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("private file %q is a directory", path)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("private file %q permissions = %04o, want 0600", path, info.Mode().Perm())
	}
}

func AssertPrivateDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat private directory %q: %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("private directory %q is not a directory", path)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("private directory %q permissions = %04o, want 0700", path, info.Mode().Perm())
	}
}

func verifyUnreadable(path string, isDir bool) error {
	if isDir {
		_, err := os.ReadDir(path)
		if err == nil {
			return fmt.Errorf("directory listing succeeded")
		}
		return nil
	}
	f, err := os.Open(path)
	if err == nil {
		f.Close()
		return fmt.Errorf("file open succeeded")
	}
	return nil
}
