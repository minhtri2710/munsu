package home

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openClosedLockFile swaps openLockFile for one that hands back a closed
// *os.File. Locking it fails on every GOOS (EBADF, or an invalid handle on
// windows) with an error that is not busy, which is the state every refusal
// below exists for.
func openClosedLockFile(t *testing.T) {
	t.Helper()
	orig := openLockFile
	t.Cleanup(func() { openLockFile = orig })
	openLockFile = func(p string, perm os.FileMode) (*os.File, error) {
		f, err := orig(p, perm)
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		return f, nil
	}
}

func TestLockFileBrokenIsNotBusy(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, nonblock := range []bool{true, false} {
		if err := lockFile(f, nonblock); err == nil || errors.Is(err, errLockBusy) {
			t.Errorf("lockFile(closed, nonblock=%v) = %v, want a non-busy error", nonblock, err)
		}
	}
}

func TestAcquireSessionLockRefusesBrokenLock(t *testing.T) {
	openClosedLockFile(t)
	ok, err := AcquireSessionLock(t.TempDir())
	if err == nil || ok {
		t.Fatalf("AcquireSessionLock = %v, %v; want a lock error, not busy", ok, err)
	}
}

func TestIsSessionLockHeldRefusesBrokenProbe(t *testing.T) {
	h := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(SessionLockPath(h)), 0755); err != nil {
		t.Fatal(err)
	}
	openClosedLockFile(t)
	if held, err := IsSessionLockHeld(h); err == nil || !strings.Contains(err.Error(), "probing lock") {
		t.Fatalf("IsSessionLockHeld = %v, %v; want the probe error", held, err)
	}
}

func TestIsSessionLockHeldRefusesUnopenableLock(t *testing.T) {
	h := t.TempDir()
	if err := os.MkdirAll(SessionLockPath(h), 0755); err != nil {
		t.Fatal(err)
	}
	if held, err := IsSessionLockHeld(h); err == nil {
		t.Fatalf("IsSessionLockHeld over a directory = %v, nil; want an error", held)
	}
}

func TestAcquireAFKLockRefusesBrokenLock(t *testing.T) {
	openClosedLockFile(t)
	lock, ok, err := AcquireAFKLock(t.TempDir())
	if err == nil || ok || lock != nil {
		t.Fatalf("AcquireAFKLock = %v, %v, %v; want a lock error, not busy", lock, ok, err)
	}
}

// TestWithExclusiveFileLockRefusesBrokenLock enters the lock-failure branch:
// the file opens, locking it fails, and fn must not run.
func TestWithExclusiveFileLockRefusesBrokenLock(t *testing.T) {
	openClosedLockFile(t)
	ran := false
	err := WithExclusiveFileLock(filepath.Join(t.TempDir(), "x.lock"), func() error {
		ran = true
		return nil
	})
	if err == nil || ran || !strings.Contains(err.Error(), "acquiring lock") {
		t.Fatalf("err = %v, ran = %v; want the lock error and fn not run", err, ran)
	}
}

// openReadOnlyLockFile swaps openLockFile for a read-only open: the lock is
// taken, and recording the holder's pid in it then fails.
func openReadOnlyLockFile(t *testing.T) {
	t.Helper()
	orig := openLockFile
	t.Cleanup(func() { openLockFile = orig })
	openLockFile = func(p string, perm os.FileMode) (*os.File, error) {
		f, err := orig(p, perm)
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		return os.Open(p)
	}
}

func TestAcquireSessionLockRefusesUnrecordablePID(t *testing.T) {
	h := t.TempDir()
	openReadOnlyLockFile(t)
	ok, err := AcquireSessionLock(h)
	if err == nil || ok || !strings.Contains(err.Error(), "recording holder pid") {
		t.Fatalf("AcquireSessionLock = %v, %v; want the pid write refused", ok, err)
	}
	if held, err := IsSessionLockHeld(h); err != nil || held {
		t.Fatalf("session lock after refused acquire: held=%v err=%v; want released", held, err)
	}
}

func TestAcquireAFKLockRefusesUnrecordablePID(t *testing.T) {
	h := t.TempDir()
	openReadOnlyLockFile(t)
	lock, ok, err := AcquireAFKLock(h)
	if err == nil || ok || lock != nil || !strings.Contains(err.Error(), "writing afk lock") {
		t.Fatalf("AcquireAFKLock = %v, %v, %v; want the pid write refused", lock, ok, err)
	}
}
