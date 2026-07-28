package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WatcherLockPolicy struct {
	ProcessAlive func(int) bool
	IsWatcher    func(int) bool
}

func SessionLockPath(h string) string { return filepath.Join(h, "state/.lock") }
func WatchLockPath(h string) string   { return filepath.Join(h, "state/.watch.lock") }
func readWatcherLockPID(p string) int {
	b, e := os.ReadFile(p)
	if e != nil {
		return 0
	}
	var pid int
	if _, e = fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid); e != nil {
		return 0
	}
	return pid
}
func acquireWatcherLock(p string, policy WatcherLockPolicy, session bool) (bool, error) {
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return false, fmt.Errorf("creating lock directory %s: %w", filepath.Dir(p), e)
	}
	if pid := readWatcherLockPID(p); pid > 0 && policy.ProcessAlive != nil {
		if !policy.ProcessAlive(pid) || (session && policy.IsWatcher != nil && policy.IsWatcher(pid)) {
			_ = os.Remove(p)
		}
	}
	f, e := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0644)
	if e != nil {
		return false, fmt.Errorf("opening lock file %s: %w", p, e)
	}
	if e = lockWatcherFile(f, true); e != nil {
		_ = f.Close()
		return false, nil
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return true, nil
}
func AcquireSessionLock(h string, p WatcherLockPolicy) (bool, error) {
	return acquireWatcherLock(SessionLockPath(h), p, true)
}
func AcquireWatchLock(h string, p WatcherLockPolicy) (bool, error) {
	return acquireWatcherLock(WatchLockPath(h), p, false)
}
func releaseWatcherLock(p string) error {
	f, e := os.OpenFile(p, os.O_RDWR, 0644)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	defer f.Close()
	return unlockWatcherFile(f)
}
func ReleaseSessionLock(h string) error { return releaseWatcherLock(SessionLockPath(h)) }
func ReleaseWatchLock(h string) error   { return releaseWatcherLock(WatchLockPath(h)) }
func watcherLockHeld(p string) bool {
	f, e := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0644)
	if e != nil {
		return false
	}
	defer f.Close()
	if e = lockWatcherFile(f, true); e != nil {
		return true
	}
	_ = unlockWatcherFile(f)
	return false
}
func IsSessionLockHeld(h string) bool { return watcherLockHeld(SessionLockPath(h)) }
func IsWatchLockHeld(h string) bool   { return watcherLockHeld(WatchLockPath(h)) }
