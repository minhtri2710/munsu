package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWatcherLockPaths(t *testing.T) {
	h := t.TempDir()
	if got, want := SessionLockPath(h), filepath.Join(h, "state/.lock"); got != want {
		t.Fatalf("session path = %q, want %q", got, want)
	}
	if got, want := WatchLockPath(h), filepath.Join(h, "state/.watch.lock"); got != want {
		t.Fatalf("watch path = %q, want %q", got, want)
	}
}

func TestWatcherLockAcquireRelease(t *testing.T) {
	h := t.TempDir()
	ok, err := AcquireSessionLock(h)
	if err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	if held, err := IsSessionLockHeld(h); err != nil || !held {
		t.Fatalf("held = %v, %v; want held", held, err)
	}
	if err := releaseWatcherLock(SessionLockPath(h)); err != nil {
		t.Fatal(err)
	}
	if held, err := IsSessionLockHeld(h); err != nil || held {
		t.Fatalf("held = %v, %v after release; want free", held, err)
	}
}

func TestWatcherLockDoesNotUnlinkHeldInode(t *testing.T) {
	h := t.TempDir()
	p := SessionLockPath(h)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	holder, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := lockFile(holder, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := AcquireSessionLock(h)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("second acquirer took a lock held through the original inode")
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("second acquirer replaced the held lock inode")
	}
}

func TestSessionLockRecordsHolderPID(t *testing.T) {
	h := t.TempDir()
	if pid, err := ReadSessionLockPID(h); err != nil || pid != 0 {
		t.Fatalf("ReadSessionLockPID with no lock = %d, %v; want 0", pid, err)
	}
	ok, err := AcquireSessionLock(h)
	if err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	defer ReleaseSessionLock(h)
	if pid, err := ReadSessionLockPID(h); err != nil || pid != os.Getpid() {
		t.Fatalf("ReadSessionLockPID = %d, %v; want %d", pid, err, os.Getpid())
	}
}

// TestReadSessionLockPIDIgnoresStalePID pins that a pid left behind by a dead
// session is not reported: with nobody holding the flock the reader answers 0.
func TestReadSessionLockPIDIgnoresStalePID(t *testing.T) {
	h := t.TempDir()
	p := SessionLockPath(h)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("17022\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if pid, err := ReadSessionLockPID(h); pid != 0 || err != nil {
		t.Fatalf("ReadSessionLockPID over an unheld lock = %d, %v; want 0, nil", pid, err)
	}
}
