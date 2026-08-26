//go:build !windows

package fsaccess

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

const chmodRelevantMode = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky

func MakeUnreadable(t *testing.T, path string) error {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat unreadable path %q: %w", path, err)
	}
	old := info.Mode() & chmodRelevantMode
	if err := os.Chmod(path, old&^0o500); err != nil {
		return fmt.Errorf("make path unreadable %q: %w", path, err)
	}
	restore := registerRestore(t, func() error {
		err := os.Chmod(path, old)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	})
	if err := verifyUnreadableAccess(path, info.IsDir()); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			return fmt.Errorf("%v; restore failed: %w", err, restoreErr)
		}
		if errors.Is(err, errAccessBypassed) {
			return &UnsupportedFixtureError{Operation: "unreadable path"}
		}
		return err
	}
	return nil
}

func MakeReadOnly(t *testing.T, path string) error {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat read-only path %q: %w", path, err)
	}
	old := info.Mode() & chmodRelevantMode
	if err := os.Chmod(path, old&^0o222); err != nil {
		return fmt.Errorf("make path read-only %q: %w", path, err)
	}
	restore := registerRestore(t, func() error {
		err := os.Chmod(path, old)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	})
	if err := verifyReadOnlyAccess(path, info.IsDir()); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			return fmt.Errorf("%v; restore failed: %w", err, restoreErr)
		}
		if errors.Is(err, errAccessBypassed) {
			return &UnsupportedFixtureError{Operation: "read-only path"}
		}
		return err
	}
	return nil
}

var errAccessBypassed = errors.New("access restriction bypassed")
var verifyUnreadableAccess = verifyUnreadable
var verifyReadOnlyAccess = verifyReadOnly

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
			return errAccessBypassed
		}
		if !errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("directory listing failed without permission denial: %w", err)
		}
		return nil
	}
	f, err := os.Open(path)
	if err == nil {
		f.Close()
		return errAccessBypassed
	}
	if !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("file open failed without permission denial: %w", err)
	}
	return nil
}

func verifyReadOnly(path string, isDir bool) error {
	if isDir {
		f, err := os.Create(path + "/.fsaccess-write-probe")
		if err == nil {
			f.Close()
			_ = os.Remove(path + "/.fsaccess-write-probe")
			return errAccessBypassed
		}
		if !errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("directory create failed without permission denial: %w", err)
		}
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err == nil {
		f.Close()
		return errAccessBypassed
	}
	if !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("file write failed without permission denial: %w", err)
	}
	return nil
}
