package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// errLockBusy is the one condition the retry loop is allowed to retry on:
// somebody else holds the scope right now. Every other failure from
// lockScopedFile -- a bad descriptor, a filesystem with no lock support, a
// Windows LockFileEx that could not be called at all -- means retrying will
// never succeed, and reporting it as ErrLockTimeout after spinning the full
// budget would name the wrong cause. Each GOOS file maps its own busy errno
// (EWOULDBLOCK, ERROR_LOCK_VIOLATION) onto this.
var errLockBusy = errors.New("home: lock is held by another owner")

// Bounded lock acquisition budget. Acquisition is advisory and per-scope; there
// is no global runtime lock that serializes independent scopes.
const (
	lockAcquireTimeout = 5 * time.Second
	lockInitialBackoff = 5 * time.Millisecond
	lockMaxBackoff     = 100 * time.Millisecond
)

// FenceToken is a monotonically increasing fencing generation for a scope.
// A holder whose token is stale cannot commit, even if it still runs.
type FenceToken uint64

// Lock is a scoped, fenced exclusive advisory lock. It owns its file handle;
// Release must be called exactly once. Lock is not safe for concurrent use.
type Lock struct {
	h        *Home
	scope    string
	path     string
	file     *os.File
	token    FenceToken
	released bool
}

// FenceToken returns the fencing generation held by this lock.
func (l *Lock) FenceToken() FenceToken { return l.token }

// Release releases the lock and closes its file handle.
func (l *Lock) Release() error {
	if l.released {
		return nil
	}
	l.released = true
	if err := unlockScopedFile(l.file); err != nil {
		_ = l.file.Close()
		return err
	}
	return l.file.Close()
}

// Lock acquires a scoped exclusive fenced lock with bounded retry. It returns
// ErrLockTimeout if the lock cannot be acquired within the budget.
func (h *Home) Lock(scope string) (*Lock, error) {
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	path := h.lockPath(scope)
	if err := privateDir(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("home: lock dir: %w", err)
	}

	var file *os.File
	var err error
	deadline := time.Now().Add(lockAcquireTimeout)
	backoff := lockInitialBackoff
	for {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, fmt.Errorf("home: open lock: %w", err)
		}
		if err := secureFile(path); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("home: secure lock: %w", err)
		}
		lockErr := lockScopedFile(file)
		if lockErr == nil {
			break
		}
		_ = file.Close()
		if !errors.Is(lockErr, errLockBusy) {
			return nil, fmt.Errorf("home: acquire lock: %w", lockErr)
		}
		if time.Now().After(deadline) {
			return nil, ErrLockTimeout
		}
		time.Sleep(backoff)
		if backoff < lockMaxBackoff {
			backoff *= 2
		}
	}

	token, err := nextFence(h.fencePath(scope))
	if err != nil {
		_ = unlockScopedFile(file)
		_ = file.Close()
		return nil, err
	}
	return &Lock{h: h, scope: scope, path: path, file: file, token: token}, nil
}

func (h *Home) lockPath(scope string) string {
	return filepath.Join(h.root, LockDirName, scope+".lock")
}

func (h *Home) fencePath(scope string) string {
	return filepath.Join(h.root, LockDirName, scope+".fence")
}

// nextFence advances the fencing generation for a scope by one and returns the
// new token. The counter lives in a sibling .fence file written atomically, so a
// crash can never truncate it to an empty or short value and reset the
// generation; the fence is monotonic across crashes. The caller holds the
// scope's exclusive flock (on the .lock file), so the read-modify-write of the
// sibling file is serialized across processes. The .lock file stays content-free.
func nextFence(fencePath string) (FenceToken, error) {
	data, err := os.ReadFile(fencePath)
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("home: read lock fence: %w", err)
	}
	var cur uint64
	if len(data) > 0 {
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &cur); err != nil {
			return 0, fmt.Errorf("home: parse lock fence: %w", err)
		}
	}
	next := cur + 1
	if err := canonicalAtomicWrite(fencePath, []byte(fmt.Sprintf("%d\n", next))); err != nil {
		return 0, fmt.Errorf("home: write lock fence: %w", err)
	}
	return FenceToken(next), nil
}

func validateScope(scope string) error {
	if scope == "" || scope == "." || scope == ".." || filepath.Base(scope) != scope || strings.ContainsAny(scope, `/\\.`) {
		return ErrInvalidScope
	}
	return nil
}
