//go:build !windows

package fsaccess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccessModifiersPreserveSpecialModeBits(t *testing.T) {
	t.Run("unreadable", func(t *testing.T) {
		dir, before := newSpecialModeDir(t)
		if err := MakeUnreadable(t, dir); err != nil {
			if IsUnsupportedFixture(err) {
				t.Errorf("access-control observation unavailable: %v", err)
				return
			}
			t.Fatal(err)
		}
		assertMode(t, dir, before&^0o500)
	})

	t.Run("read-only", func(t *testing.T) {
		dir, before := newSpecialModeDir(t)
		if err := MakeReadOnly(t, dir); err != nil {
			if IsUnsupportedFixture(err) {
				t.Errorf("access-control observation unavailable: %v", err)
				return
			}
			t.Fatal(err)
		}
		assertMode(t, dir, before&^0o222)
	})

	t.Run("restored", func(t *testing.T) {
		dir, before := newSpecialModeDir(t)
		if !t.Run("unreadable", func(t *testing.T) {
			if err := MakeUnreadable(t, dir); err != nil {
				if IsUnsupportedFixture(err) {
					t.Errorf("access-control observation unavailable: %v", err)
					return
				}
				t.Fatal(err)
			}
		}) {
			t.Fatal("MakeUnreadable failed")
		}
		assertMode(t, dir, before)
		if !t.Run("read-only", func(t *testing.T) {
			if err := MakeReadOnly(t, dir); err != nil {
				if IsUnsupportedFixture(err) {
					t.Errorf("access-control observation unavailable: %v", err)
					return
				}
				t.Fatal(err)
			}
		}) {
			t.Fatal("MakeReadOnly failed")
		}
		assertMode(t, dir, before)
	})
}

func TestAccessModifiersReportPrivilegedBypassAndRestore(t *testing.T) {
	dir, before := newSpecialModeDir(t)
	oldUnreadable := verifyUnreadableAccess
	t.Cleanup(func() { verifyUnreadableAccess = oldUnreadable })
	verifyUnreadableAccess = func(string, bool) error { return errAccessBypassed }
	if err := MakeUnreadable(t, dir); !IsUnsupportedFixture(err) {
		t.Fatalf("MakeUnreadable error = %v, want unsupported fixture", err)
	}
	assertMode(t, dir, before)

	oldReadOnly := verifyReadOnlyAccess
	t.Cleanup(func() { verifyReadOnlyAccess = oldReadOnly })
	verifyReadOnlyAccess = func(string, bool) error { return errAccessBypassed }
	if err := MakeReadOnly(t, dir); !IsUnsupportedFixture(err) {
		t.Fatalf("MakeReadOnly error = %v, want unsupported fixture", err)
	}
	assertMode(t, dir, before)
}

func newSpecialModeDir(t *testing.T) (string, os.FileMode) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dir")
	if err := os.Mkdir(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	requested := os.FileMode(0o770) | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	if err := os.Chmod(dir, requested); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	before := info.Mode() & chmodRelevantMode
	if before&os.ModeSticky == 0 {
		t.Fatalf("special mode baseline %v does not retain sticky bit", before)
	}
	return dir, before
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode() & chmodRelevantMode; got != want {
		t.Fatalf("mode = %v, want %v", got, want)
	}
}
