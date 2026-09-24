package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAFKLockConcurrentAcquireHasOneWinner pins the singleton: the flock, not
// a read-then-write of the file's content, decides who holds the AFK lock.
func TestAFKLockConcurrentAcquireHasOneWinner(t *testing.T) {
	h := t.TempDir()
	const contenders = 16
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		locks []*AFKLock
		start = make(chan struct{})
	)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lock, acquired, err := AcquireAFKLock(h)
			if err != nil {
				t.Error(err)
				return
			}
			if acquired {
				mu.Lock()
				locks = append(locks, lock)
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	for _, l := range locks {
		defer l.Release()
	}
	if len(locks) != 1 {
		t.Fatalf("concurrent AcquireAFKLock winners = %d, want 1", len(locks))
	}
}

func TestAFKLockRecordsHolderAndReleases(t *testing.T) {
	h := t.TempDir()
	lock, acquired, err := AcquireAFKLock(h)
	if err != nil || !acquired {
		t.Fatalf("AcquireAFKLock = %v, %v", acquired, err)
	}
	data, err := os.ReadFile(AFKLockPath(h))
	if err != nil {
		t.Fatal(err)
	}
	pid, started := parseLockPID(data)
	if pid != os.Getpid() {
		t.Errorf("lock pid = %d, want %d", pid, os.Getpid())
	}
	if _, err := time.Parse(time.RFC3339, started); err != nil {
		t.Errorf("lock start %q is not RFC3339: %v", started, err)
	}
	if got, err := ReadAFKLockPID(h); err != nil || got != os.Getpid() {
		t.Errorf("ReadAFKLockPID = %d, %v; want %d", got, err, os.Getpid())
	}
	before, err := os.Stat(AFKLockPath(h))
	if err != nil {
		t.Fatal(err)
	}

	if _, again, err := AcquireAFKLock(h); err != nil || again {
		t.Fatalf("second AcquireAFKLock = %v, %v; want busy", again, err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}

	// Release keeps the inode: a removed path would let a later opener flock
	// a fresh file while an old holder still ran.
	after, err := os.Stat(AFKLockPath(h))
	if err != nil {
		t.Fatalf("lock file after Release: %v", err)
	}
	if !os.SameFile(before, after) || after.Size() != 0 {
		t.Fatalf("lock file after Release: same=%v size=%d, want same inode, empty", os.SameFile(before, after), after.Size())
	}
	if got, err := ReadAFKLockPID(h); err != nil || got != 0 {
		t.Errorf("ReadAFKLockPID after Release = %d, %v; want 0", got, err)
	}
}

// TestAFKLockStalePIDIsTakenOverByFlock pins that a file left by a dead daemon
// is ignored by readers and taken over by the next acquirer, both decided by
// the flock rather than by the pid in the file. The pid here is this live
// process, so any liveness check of the content would read it as held.
func TestAFKLockStalePIDIsTakenOverByFlock(t *testing.T) {
	h := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(AFKLockPath(h)), 0700); err != nil {
		t.Fatal(err)
	}
	stale := fmt.Sprintf("%d\t2024-01-01T00:00:00Z\n", os.Getpid())
	if err := os.WriteFile(AFKLockPath(h), []byte(stale), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadAFKLockPID(h); err != nil || got != 0 {
		t.Fatalf("ReadAFKLockPID over an unheld file = %d, %v; want 0", got, err)
	}
	lock, acquired, err := AcquireAFKLock(h)
	if err != nil || !acquired {
		t.Fatalf("AcquireAFKLock over a stale file = %v, %v; want acquired", acquired, err)
	}
	defer lock.Release()
}

func TestReadAFKLockPIDNoFile(t *testing.T) {
	if got, err := ReadAFKLockPID(t.TempDir()); err != nil || got != 0 {
		t.Fatalf("ReadAFKLockPID on a fresh home = %d, %v; want 0", got, err)
	}
}

// TestReadAFKLockPIDRefusesHeldLockWithoutPID builds a daemon between its flock
// and its pid write. Reporting 0 would tell Return no daemon runs.
func TestReadAFKLockPIDRefusesHeldLockWithoutPID(t *testing.T) {
	h := t.TempDir()
	lock, acquired, err := AcquireAFKLock(h)
	if err != nil || !acquired {
		t.Fatalf("AcquireAFKLock = %v, %v", acquired, err)
	}
	defer lock.Release()
	if err := os.Truncate(AFKLockPath(h), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAFKLockPID(h); err == nil || !strings.Contains(err.Error(), "records no pid") {
		t.Fatalf("ReadAFKLockPID = %v, want a refusal", err)
	}
}
