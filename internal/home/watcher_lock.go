package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

func SessionLockPath(h string) string { return filepath.Join(h, "state/.lock") }
func WatchLockPath(h string) string   { return filepath.Join(h, "state/.watch.lock") }

var watcherLocks = struct {
	sync.Mutex
	files map[string]*os.File
}{files: make(map[string]*os.File)}

// openLockFile opens (creating if absent) a file this package locks. It is a
// variable so tests can hand the lock calls a closed *os.File, the one
// portable way to make lockFile fail with something other than busy.
var openLockFile = func(p string, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(p, os.O_RDWR|os.O_CREATE, perm)
}

// acquireWatcherLock reports (false, nil) only when another owner holds p.
// Any other failure is returned, so a broken lock is never read as "someone
// else has it". With recordPID the holder's pid is written into the file after
// the flock is taken, for ReadSessionLockPID.
func acquireWatcherLock(p string, recordPID bool) (bool, error) {
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return false, fmt.Errorf("creating lock directory %s: %w", filepath.Dir(p), e)
	}
	f, e := openLockFile(p, 0644)
	if e != nil {
		return false, fmt.Errorf("opening lock file %s: %w", p, e)
	}
	if e = lockFile(f, true); e != nil {
		_ = f.Close()
		if errors.Is(e, errLockBusy) {
			return false, nil
		}
		return false, fmt.Errorf("locking %s: %w", p, e)
	}
	if recordPID {
		if e = writeLockPID(f, strconv.Itoa(os.Getpid())+"\n"); e != nil {
			_ = unlockFile(f)
			_ = f.Close()
			return false, fmt.Errorf("recording holder pid in %s: %w", p, e)
		}
	}
	watcherLocks.Lock()
	watcherLocks.files[p] = f
	watcherLocks.Unlock()
	return true, nil
}
func AcquireSessionLock(h string) (bool, error) {
	return acquireWatcherLock(SessionLockPath(h), true)
}
func AcquireWatchLock(h string) (bool, error) {
	return acquireWatcherLock(WatchLockPath(h), false)
}
func releaseWatcherLock(p string) error {
	watcherLocks.Lock()
	f := watcherLocks.files[p]
	delete(watcherLocks.files, p)
	watcherLocks.Unlock()
	if f == nil {
		return nil
	}
	if err := unlockFile(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
func ReleaseWatchLock(h string) error { return releaseWatcherLock(WatchLockPath(h)) }

// ReleaseSessionLock drops a session lock this process holds.
//
// Session-start holds this lock for the process lifetime and lets exit drop it,
// so the success path never calls this. An ABORTED session-start must, because
// the session it locked for never started -- and on windows the cost of not
// calling it is larger than a stale flock: the open handle also pins the file,
// so the home directory cannot be removed while this process lives (#549
// group 10).
func ReleaseSessionLock(h string) error { return releaseWatcherLock(SessionLockPath(h)) }

// watcherLockHeld probes p with a transient non-blocking lock. It fails
// closed: an error opening or locking p is returned, never read as held or free.
func watcherLockHeld(p string) (bool, error) {
	f, e := openLockFile(p, 0644)
	if e != nil {
		return false, fmt.Errorf("opening lock file %s: %w", p, e)
	}
	defer f.Close()
	if e = lockFile(f, true); e != nil {
		if errors.Is(e, errLockBusy) {
			return true, nil
		}
		return false, fmt.Errorf("probing lock %s: %w", p, e)
	}
	return false, unlockFile(f)
}
func IsSessionLockHeld(h string) (bool, error) { return watcherLockHeld(SessionLockPath(h)) }

// ReadSessionLockPID returns the pid of the session lock holder, or 0 when no
// session holds the lock. The file outlives its holder, so a pid left by a dead
// session is reported as 0; the flock, not the content, is the authority.
func ReadSessionLockPID(h string) (int, error) { return readHeldLockPID(SessionLockPath(h)) }

// writeLockPID replaces a held lock file's content with content. Only the
// flock holder calls it, so the truncate cannot race another writer.
func writeLockPID(f *os.File, content string) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt([]byte(content), 0); err != nil {
		return err
	}
	return f.Sync()
}

// parseLockPID reads "<pid>[\t<rest>]" and returns the pid and the rest, or
// 0 when the first field is not a positive integer.
func parseLockPID(data []byte) (int, string) {
	first, rest, _ := strings.Cut(strings.TrimSpace(string(data)), "\t")
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil || pid <= 0 {
		return 0, ""
	}
	return pid, strings.TrimSpace(rest)
}
