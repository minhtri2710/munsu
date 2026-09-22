package home

import (
	"fmt"
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
	ok, err := AcquireSessionLock(h, WatcherLockPolicy{})
	if err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	if !IsSessionLockHeld(h) {
		t.Fatal("lock not held")
	}
	if err := releaseWatcherLock(SessionLockPath(h)); err != nil {
		t.Fatal(err)
	}
	if IsSessionLockHeld(h) {
		t.Fatal("lock remained held after release")
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
	if err := lockWatcherFile(holder, true); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(holder, "%d\n", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := AcquireSessionLock(h, WatcherLockPolicy{ProcessAlive: func(int) bool { return false }, IsWatcher: func(int) bool { return true }})
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

func TestWatcherPIDPolicyIsIgnoredByFlockAcquisition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    func(string) string
		acquire func(string, WatcherLockPolicy) (bool, error)
		release func(string) error
	}{
		{"session", SessionLockPath, AcquireSessionLock, ReleaseSessionLock},
		{"watch", WatchLockPath, AcquireWatchLock, ReleaseWatchLock},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := t.TempDir()
			p := tc.path(h)
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("123\n"), 0644); err != nil {
				t.Fatal(err)
			}
			ok, err := tc.acquire(h, WatcherLockPolicy{ProcessAlive: func(int) bool { return false }, IsWatcher: func(int) bool { return true }})
			if err != nil || !ok {
				t.Fatalf("acquire=%v,%v", ok, err)
			}
			defer tc.release(h)
		})
	}
}
