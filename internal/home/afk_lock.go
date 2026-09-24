package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AFKLockPath is the AFK daemon's singleton lock. It is its own file: the
// session lock (SessionLockPath) is held for a session-start process's
// lifetime, and sharing one path let the AFK release unlink the file under a
// live session flock.
func AFKLockPath(h string) string { return filepath.Join(h, "state/.afk.lock") }

// AFKLock is the AFK daemon's flock, held for the daemon's lifetime. The file
// records "<pid>\t<RFC3339>" of the holder, written after the flock is taken.
type AFKLock struct{ f *os.File }

// AcquireAFKLock takes the AFK lock without blocking. It returns (nil, false,
// nil) when another daemon holds it. A file left by a dead daemon is taken over
// by winning the flock, never by reading its content.
func AcquireAFKLock(h string) (*AFKLock, bool, error) {
	p := AFKLockPath(h)
	if err := ensurePrivateStateDir(filepath.Dir(p)); err != nil {
		return nil, false, fmt.Errorf("creating afk lock directory: %w", err)
	}
	f, err := openLockFile(p, 0600)
	if err != nil {
		return nil, false, fmt.Errorf("opening afk lock: %w", err)
	}
	if err := lockFile(f, true); err != nil {
		_ = f.Close()
		if errors.Is(err, errLockBusy) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("locking afk lock: %w", err)
	}
	content := fmt.Sprintf("%d\t%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := writeLockPID(f, content); err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, false, fmt.Errorf("writing afk lock: %w", err)
	}
	return &AFKLock{f: f}, true, nil
}

// Release empties the lock file and unlocks it. The file is never removed: a
// removed path lets the next opener flock a fresh inode while a holder of the
// old one still runs.
func (l *AFKLock) Release() error {
	return errors.Join(l.f.Truncate(0), unlockFile(l.f), l.f.Close())
}

// ReadAFKLockPID returns the pid of the daemon holding the AFK lock, or 0 when
// no daemon holds it. A pid left in a file nobody holds is stale (the holder
// died without Release) and is reported as 0, so a reused pid is never
// mistaken for the daemon. Probing the flock is the only authority; a probe
// that cannot be answered, or a held lock with no pid, is an error.
func ReadAFKLockPID(h string) (int, error) { return readHeldLockPID(AFKLockPath(h)) }

// readHeldLockPID returns the pid recorded in lock file p while a holder has
// its flock, or 0 when the file is absent or nobody holds it.
func readHeldLockPID(p string) (int, error) {
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	held, err := watcherLockHeld(p)
	if err != nil || !held {
		return 0, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, fmt.Errorf("reading lock %s: %w", p, err)
	}
	pid, _ := parseLockPID(data)
	if pid == 0 {
		// Held, but the holder has not recorded itself yet (or wrote garbage):
		// "no holder" would be a lie, so refuse.
		return 0, fmt.Errorf("lock %s is held but records no pid", p)
	}
	return pid, nil
}
