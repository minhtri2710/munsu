package taskauthorityfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLockPrimitivePaths(t *testing.T) {
	home := t.TempDir()
	if got := dispatchLockPath(home); got != filepath.Join(home, "state", ".dispatch.lock") {
		t.Errorf("dispatchLockPath = %q, want %q", got, filepath.Join(home, "state", ".dispatch.lock"))
	}
	taskPath, err := taskLockPath(home, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if taskPath != filepath.Join(home, "state", "t1.meta.lock") {
		t.Errorf("taskLockPath = %q, want %q", taskPath, filepath.Join(home, "state", "t1.meta.lock"))
	}
	if _, err := taskLockPath(home, "../escape"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("taskLockPath(unsafe task id) error = %v, want ErrInvalidPath", err)
	}
}

// TestLockOrderDispatchBeforeTask proves the canonical acquisition order
// deterministically: the combined operation records the dispatch lock first
// and can never touch the per-task lock while the test holds it. The recorder
// signals only after a successful flock, so the assertion windows cannot race
// the goroutine's scheduling.
func TestLockOrderDispatchBeforeTask(t *testing.T) {
	home := t.TempDir()
	taskPath, err := taskLockPath(home, "t1")
	if err != nil {
		t.Fatal(err)
	}
	// Hold the per-task lock so the combined operation's second acquisition
	// provably blocks after the dispatch lock is already recorded.
	taskFile, err := lockFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(taskFile)

	var mu sync.Mutex
	var acquired []string
	acquiredCh := make(chan string, 2)
	record := func(path string) (*os.File, error) {
		f, err := lockFile(path)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		acquired = append(acquired, path)
		mu.Unlock()
		acquiredCh <- path
		return f, nil
	}

	ran := make(chan struct{})
	go func() {
		_ = withLocksOrdered(home, "t1", record, func() error {
			close(ran)
			return nil
		})
	}()

	// The first recorded acquisition must be the dispatch lock.
	select {
	case path := <-acquiredCh:
		if path != dispatchLockPath(home) {
			t.Fatalf("first acquired lock = %q, want dispatch lock %q", path, dispatchLockPath(home))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch lock was never acquired")
	}
	// While the test holds the task lock, no second acquisition can be
	// recorded: the operation must wait on the task lock after dispatch.
	select {
	case path := <-acquiredCh:
		t.Fatalf("lock %q acquired while the test held the task lock (wrong order)", path)
	case <-time.After(200 * time.Millisecond):
	}

	releaseLock(taskFile)

	select {
	case path := <-acquiredCh:
		if path != taskPath {
			t.Fatalf("second acquired lock = %q, want task lock %q", path, taskPath)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("task lock was never acquired after release")
	}
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("fn never ran after both locks were acquired")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(acquired) != 2 || acquired[0] != dispatchLockPath(home) || acquired[1] != taskPath {
		t.Fatalf("acquisition order = %v, want [dispatch, task]", acquired)
	}
}

func TestLockPrimitivesSerializeSameTask(t *testing.T) {
	home := t.TempDir()
	var (
		mu        sync.Mutex
		active    int
		maxActive int
	)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := withLocks(home, "t1", func() error {
				mu.Lock()
				active++
				if active > maxActive {
					maxActive = active
				}
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				active--
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Errorf("withLocks: %v", err)
			}
		}()
	}
	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("critical section ran %d times concurrently, want 1", maxActive)
	}
}

// TestLockPrimitivesSerializeAcrossTasks documents the conservative design:
// the shared dispatch lock serializes operations on different tasks too,
// until measured contention justifies a separate decision.
func TestLockPrimitivesSerializeAcrossTasks(t *testing.T) {
	home := t.TempDir()
	var (
		mu        sync.Mutex
		active    int
		maxActive int
	)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			taskID := fmt.Sprintf("t%d", i)
			err := withLocks(home, taskID, func() error {
				mu.Lock()
				active++
				if active > maxActive {
					maxActive = active
				}
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				active--
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Errorf("withLocks(%s): %v", taskID, err)
			}
		}(i)
	}
	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("critical section ran %d times concurrently across tasks, want 1", maxActive)
	}
}

func TestLockFilesCreatedPrivate(t *testing.T) {
	home := t.TempDir()
	if err := withLocks(home, "t1", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	dispatchInfo, err := os.Stat(dispatchLockPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatchInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("dispatch lock mode = %o, want 0600", got)
	}
	taskPath, err := taskLockPath(home, "t1")
	if err != nil {
		t.Fatal(err)
	}
	taskInfo, err := os.Stat(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := taskInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("task lock mode = %o, want 0600", got)
	}
	stateInfo, err := os.Stat(filepath.Join(home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if got := stateInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("state dir mode = %o, want 0700", got)
	}
}

func TestWithLocksRejectsInvalidTaskID(t *testing.T) {
	home := t.TempDir()
	calls := 0
	record := func(path string) (*os.File, error) {
		calls++
		return lockFile(path)
	}
	err := withLocksOrdered(home, "../escape", record, func() error { return nil })
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("withLocksOrdered(unsafe task id) error = %v, want ErrInvalidPath", err)
	}
	if calls != 0 {
		t.Fatalf("lock acquisition attempted %d times, want 0 (task id validated before dispatch lock)", calls)
	}
}
