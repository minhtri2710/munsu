//go:build !windows

package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// TestSessionLockIsNotStolenWhenLivenessIsUnprobable builds the state #580
// reported end to end: a live holder's PID in state/.lock, and a PATH under
// which the shell-out probe answered "dead". acquireWatcherLock then removed
// the file, and the next O_CREATE handed the second acquirer a fresh inode to
// flock while the holder's flock stayed on the orphan. The lock file must
// survive as the same inode and the second acquire must be refused.
func TestSessionLockIsNotStolenWhenLivenessIsUnprobable(t *testing.T) {
	h := t.TempDir()
	p := home.SessionLockPath(h)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatalf("creating state dir: %v", err)
	}
	holder, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("opening lock file: %v", err)
	}
	defer holder.Close()
	// flock is held per open file description, so this conflicts with the
	// descriptor AcquireSession opens even though both live in this process.
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("locking lock file: %v", err)
	}
	if _, err := fmt.Fprintf(holder, "%d\n", os.Getpid()); err != nil {
		t.Fatalf("writing holder PID: %v", err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}

	t.Setenv("PATH", t.TempDir())

	ok, err := AcquireSession(h)
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	if ok {
		t.Error("AcquireSession took the session lock while a live holder held it")
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatalf("a live holder's lock file was removed: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Error("a live holder's lock file was replaced by a fresh inode")
	}
}
