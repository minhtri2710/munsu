package home

import (
	"errors"
	"fmt"
	"io"
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

	token, err := nextFence(file)
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

// nextFence reads the current fencing token from a held lock file and advances
// it by one, returning the new token. The caller holds the exclusive lock, so
// read-modify-write is safe.
func nextFence(file *os.File) (FenceToken, error) {
	// Read through the already-open, already-locked handle: os.ReadFile(file.Name())
	// opens a second handle, which Windows byte-range locks deny access through.
	if _, err := file.Seek(0, 0); err != nil {
		return 0, fmt.Errorf("home: read lock fence: %w", err)
	}
	// No os.IsNotExist tolerance: that was meaningful for os.ReadFile, which
	// could be handed a path that had since vanished. Home.Lock opened this
	// handle with O_CREATE and holds it, so a read through it cannot report a
	// missing file -- tolerating one would describe a state that cannot occur.
	data, err := io.ReadAll(file)
	if err != nil {
		return 0, fmt.Errorf("home: read lock fence: %w", err)
	}
	var cur uint64
	if len(data) > 0 {
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &cur); err != nil {
			return 0, fmt.Errorf("home: parse lock fence: %w", err)
		}
	}
	next := cur + 1
	if err := file.Truncate(0); err != nil {
		return 0, fmt.Errorf("home: truncate lock: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return 0, fmt.Errorf("home: seek lock: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", next); err != nil {
		return 0, fmt.Errorf("home: write lock fence: %w", err)
	}
	if err := file.Sync(); err != nil {
		return 0, fmt.Errorf("home: sync lock: %w", err)
	}
	return FenceToken(next), nil
}

func validateScope(scope string) error {
	if scope == "" || scope == "." || scope == ".." || filepath.Base(scope) != scope || strings.ContainsAny(scope, `/\\.`) {
		return ErrInvalidScope
	}
	return nil
}
