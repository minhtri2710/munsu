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

func TestWatcherLockReclaimsStalePID(t *testing.T) {
	h := t.TempDir()
	p := WatchLockPath(h)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("99999999\n"), 0644); err != nil {
		t.Fatal(err)
	}
	called := false
	ok, err := AcquireWatchLock(h, WatcherLockPolicy{ProcessAlive: func(pid int) bool { called = true; return false }})
	if err != nil || !ok || !called {
		t.Fatalf("acquire = %v, %v; callback=%v", ok, err, called)
	}
	defer ReleaseWatchLock(h)
}

func TestWatcherPIDPolicyOnlyAppliesToSessionLock(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    func(string) string
		acquire func(string, WatcherLockPolicy) (bool, error)
		release func(string) error
		calls   int
	}{
		{"session", SessionLockPath, AcquireSessionLock, ReleaseSessionLock, 1},
		{"watch", WatchLockPath, AcquireWatchLock, ReleaseWatchLock, 0},
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
			calls := 0
			policy := WatcherLockPolicy{ProcessAlive: func(int) bool { return true }, IsWatcher: func(int) bool { calls++; return true }}
			ok, err := tc.acquire(h, policy)
			if err != nil || !ok {
				t.Fatalf("acquire=%v,%v", ok, err)
			}
			defer tc.release(h)
			if calls != tc.calls {
				t.Fatalf("watcher policy calls=%d, want %d", calls, tc.calls)
			}
		})
	}
}
