package home

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentEnqueueAndClaimPreservesEveryWake(t *testing.T) {
	home := t.TempDir()
	const producers = 32
	const claimers = 32

	claimed := make(chan string, claimers)
	errs := make(chan error, producers+claimers)
	var wg sync.WaitGroup
	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := EnqueueWake(home, "signal", fmt.Sprintf("task-%02d", i), "payload"); err != nil {
				errs <- err
			}
		}(i)
	}
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := ClaimWakes(home, "consumer", 60, 1)
			if err != nil {
				errs <- err
				return
			}
			for _, wake := range result.Wakes {
				claimed <- wake.Key
			}
		}()
	}
	wg.Wait()
	close(claimed)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	queued, err := DrainWakes(home)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]int, producers)
	for key := range claimed {
		seen[key]++
	}
	for _, wake := range queued {
		seen[wake.Key]++
	}
	if len(seen) != producers {
		t.Fatalf("saw %d unique wakes, want %d: %v", len(seen), producers, seen)
	}
	for key, count := range seen {
		if count != 1 {
			t.Fatalf("wake %q appeared %d times, want once", key, count)
		}
	}
}

func TestClaimWakesConcurrentConsumersClaimRecordOnce(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(WakeQueuePath(home), []byte("100\t1\tsignal\tkey\tpayload\n"), 0644); err != nil {
		t.Fatal(err)
	}

	const consumers = 8
	results := make(chan int, consumers)
	errs := make(chan error, consumers)
	var wg sync.WaitGroup
	for i := 0; i < consumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := ClaimWakes(home, "consumer", 60, 1)
			if err != nil {
				errs <- err
				return
			}
			results <- len(result.Wakes)
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	total := 0
	for n := range results {
		total += n
	}
	if total != 1 {
		t.Fatalf("concurrent claims returned %d copies, want 1", total)
	}
}
