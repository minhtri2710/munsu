package orchestrator

import (
	"os"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// TestAFKReleaseKeepsSessionLock pins that the AFK daemon lock and the session
// lock are different files. When they shared one path, the AFK release
// unlinked the file under a live session flock, and the next session-start
// flocked a fresh inode while the first session still held the old one.
func TestAFKReleaseKeepsSessionLock(t *testing.T) {
	h := t.TempDir()
	acquired, err := AcquireSession(h)
	if err != nil || !acquired {
		t.Fatalf("AcquireSession = %v, %v; want held", acquired, err)
	}
	t.Cleanup(func() { _ = ReleaseSession(h) })
	before, err := os.Stat(mhome.SessionLockPath(h))
	if err != nil {
		t.Fatal(err)
	}

	lock, afkAcquired, err := mhome.AcquireAFKLock(h)
	if err != nil || !afkAcquired {
		t.Fatalf("AFK acquire = %v, %v; want acquired", afkAcquired, err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("AFK release: %v", err)
	}

	after, err := os.Stat(mhome.SessionLockPath(h))
	if err != nil {
		t.Fatalf("session lock file after AFK release: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("session lock file replaced by AFK release")
	}
	again, err := AcquireSession(h)
	if err != nil {
		t.Fatal(err)
	}
	if again {
		t.Fatal("a new session-start acquired the session lock while the first still holds it")
	}
}
