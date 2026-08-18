package home

import (
	"errors"
	"testing"
	"time"
)

// lockTimeoutTestDeadline bounds the regression so that a Home.Lock which parks
// in the locking syscall -- or whose deadline guard never fires -- reports as a
// failing test rather than as a test binary hanging until its own timeout kills
// it. It must stay well under `go test -timeout` (10m by default), because a
// hang there is read as flake and costs the whole lane.
const lockTimeoutTestDeadline = 60 * time.Second

// TestLockHeldScopeTimesOut pins the contract Home.Lock documents: a scope held
// by somebody else refuses with ErrLockTimeout once the budget is spent,
// instead of blocking forever on a holder that may never release.
func TestLockHeldScopeTimesOut(t *testing.T) {
	h := newTestHome(t)
	held, err := h.Lock("scope")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	type attempt struct {
		lock    *Lock
		err     error
		elapsed time.Duration
	}
	done := make(chan attempt, 1)
	go func() {
		start := time.Now()
		lk, err := h.Lock("scope")
		done <- attempt{lock: lk, err: err, elapsed: time.Since(start)}
	}()

	select {
	case got := <-done:
		if got.lock != nil {
			_ = got.lock.Release()
			t.Fatal("second Lock acquired a scope already held")
		}
		if !errors.Is(got.err, ErrLockTimeout) {
			t.Fatalf("second Lock on a held scope: got %v, want ErrLockTimeout", got.err)
		}
		if got.elapsed < lockAcquireTimeout {
			t.Errorf("second Lock refused after %s, before the %s budget was spent", got.elapsed, lockAcquireTimeout)
		}
	case <-time.After(lockTimeoutTestDeadline):
		t.Fatalf("second Lock on a held scope did not return within %s: the %s budget is not enforced", lockTimeoutTestDeadline, lockAcquireTimeout)
	}
}
