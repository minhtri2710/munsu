package home

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWithExclusiveFileLockSerializesCallers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	var mu sync.Mutex
	inside, maxInside := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := WithExclusiveFileLock(path, func() error {
				mu.Lock()
				inside++
				if inside > maxInside {
					maxInside = inside
				}
				mu.Unlock()
				time.Sleep(time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("callers inside the lock at once = %d, want 1", maxInside)
	}
}

func TestWithExclusiveFileLockRefusesWhenLockCannotBeTaken(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, nil, 0600); err != nil {
		t.Fatal(err)
	}
	ran := false
	err := WithExclusiveFileLock(filepath.Join(parent, "x.lock"), func() error {
		ran = true
		return nil
	})
	if err == nil || ran {
		t.Fatalf("err = %v, ran = %v; want an error and fn not run", err, ran)
	}
}
