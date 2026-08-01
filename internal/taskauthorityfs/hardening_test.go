package taskauthorityfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestViewSerializesUnderDispatchLock proves View reads the entire snapshot
// under state/.dispatch.lock deterministically. The test holds the dispatch
// lock and starts View; a View that ignores the lock completes in
// microseconds, so any completion before the test releases the lock is a
// torn-snapshot window. The timeout only guards against a stalled goroutine;
// correctness is decided by the completion event, not by sleeping.
func TestViewSerializesUnderDispatchLock(t *testing.T) {
	home := t.TempDir()
	buildCanonicalHome(t, home)
	s := openStore(t, home)

	holdFile, err := lockFile(dispatchLockPath(home))
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := s.View()
		done <- err
	}()

	<-started
	select {
	case err := <-done:
		t.Fatalf("View completed while the dispatch lock was held (err=%v): read did not take the lock", err)
	case <-time.After(300 * time.Millisecond):
	}

	releaseLock(holdFile)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("View after lock release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("View never completed after the dispatch lock was released")
	}
}

// TestViewRejectsSymlinks proves the read path never follows a link: any
// symlink entry, document, or current pointer inside the authority root is
// corruption, even when the target is valid state outside the root (or even
// inside it). Each case builds a home whose only defect is the link, so a
// pre-fix View that followed links would succeed.
func TestViewRejectsSymlinks(t *testing.T) {
	writeFile := func(t *testing.T, path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	linkAt := func(t *testing.T, home, rel, target string) {
		t.Helper()
		abs := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, abs); err != nil {
			t.Fatal(err)
		}
	}
	encodeHold := func(t *testing.T, id string) []byte {
		t.Helper()
		hold := fixtureHold()
		hold.ID = id
		data, err := EncodeHold(hold)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	encodeAggregate := func(t *testing.T, taskID string, generation, revision uint64) []byte {
		t.Helper()
		agg, err := taskauthority.NewAggregate(taskID, "owner", "work", "ship", "", "")
		if err != nil {
			t.Fatal(err)
		}
		agg.Generation = taskauthority.Generation(generation)
		agg.Revision = taskauthority.Revision(revision)
		agg.Current = true
		data, err := EncodeAggregate(agg)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	t.Run("hold document link to file outside the home", func(t *testing.T) {
		home := t.TempDir()
		outside := filepath.Join(t.TempDir(), "hold-1.json")
		writeFile(t, outside, encodeHold(t, "hold-1"))
		linkAt(t, home, "state/.task-authority/v2/holds/686f6c642d31.json", outside)
		assertCorrupt(t, home)
	})

	t.Run("aggregate document link to file outside the home", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		outside := filepath.Join(t.TempDir(), "t1-1.json")
		writeFile(t, outside, encodeAggregate(t, "t1", 1, 1))
		linkAt(t, home, "state/.task-authority/v2/aggregates/t1/1.json", outside)
		assertCorrupt(t, home)
	})

	t.Run("current pointer link to file outside the home", func(t *testing.T) {
		home := t.TempDir()
		writeAggregateDoc(t, home, "t1", 1, 1, true)
		outside := filepath.Join(t.TempDir(), "current")
		writeFile(t, outside, []byte("1\n"))
		linkAt(t, home, "state/.task-authority/v2/aggregates/t1/current", outside)
		assertCorrupt(t, home)
	})

	t.Run("hold document link to valid state inside the root", func(t *testing.T) {
		home := t.TempDir()
		// Real hold-1 document, then a hold-2 link that resolves to it. The
		// link must be rejected before identity checks can read through it.
		writeFile(t, filepath.Join(home, "state/.task-authority/v2/holds/686f6c642d31.json"), encodeHold(t, "hold-1"))
		linkAt(t, home, "state/.task-authority/v2/holds/686f6c642d32.json", "686f6c642d31.json")
		assertCorrupt(t, home)
	})

	t.Run("task directory link to directory outside the root", func(t *testing.T) {
		home := t.TempDir()
		outside := filepath.Join(t.TempDir(), "t1")
		writeFile(t, filepath.Join(outside, currentFileName), []byte("1\n"))
		writeFile(t, filepath.Join(outside, "1.json"), encodeAggregate(t, "t1", 1, 1))
		linkAt(t, home, "state/.task-authority/v2/aggregates/t1", outside)
		assertCorrupt(t, home)
	})

	t.Run("non-document link entry in task dir", func(t *testing.T) {
		home := t.TempDir()
		writeCurrentPointer(t, home, "t1", 1)
		writeAggregateDoc(t, home, "t1", 1, 1, true)
		outside := filepath.Join(t.TempDir(), "README")
		writeFile(t, outside, []byte("x"))
		linkAt(t, home, "state/.task-authority/v2/aggregates/t1/README", outside)
		assertCorrupt(t, home)
	})
}

// TestLockFileResecuresExisting proves lockFile re-secures a pre-existing
// lock file to 0600 instead of leaving a wider mode (for example 0644)
// that a prior owner or a careless umask left behind.
func TestLockFileResecuresExisting(t *testing.T) {
	home := t.TempDir()

	lockPath := dispatchLockPath(home)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockPath, 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := lockFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	releaseLock(f)

	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("dispatch lock mode after lockFile = %o, want 0600", got)
	}

	// The per-task lock must be re-secured too.
	taskPath, err := taskLockPath(home, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(taskPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := withLocks(home, "t1", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	taskInfo, err := os.Stat(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := taskInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("task lock mode after withLocks = %o, want 0600", got)
	}
}
