package home

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestAppendStatusOnceConcurrentPublishesOnce pins that the duplicate check and
// the append share the meta lock: concurrent pollers publishing one line
// append it exactly once.
func TestAppendStatusOnceConcurrentPublishesOnce(t *testing.T) {
	h := t.TempDir()
	const pollers = 16
	var (
		wg       sync.WaitGroup
		appended atomic.Int32
		start    = make(chan struct{})
	)
	for i := 0; i < pollers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := AppendStatusOnce(h, "task-1", "done: merged")
			if err != nil {
				t.Error(err)
			}
			if ok {
				appended.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	lines, err := ReadStatus(h, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if appended.Load() != 1 || len(lines) != 1 {
		t.Fatalf("appended = %d, lines = %v; want one append and one line", appended.Load(), lines)
	}
}
