package home

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

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
