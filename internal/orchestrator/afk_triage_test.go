package orchestrator

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// A process-event wake belongs to the supervision watcher's consumer: the AFK
// digest neither classifies it nor removes it from the queue.
func TestOneCycleLeavesProcessEventWakesQueued(t *testing.T) {
	homeDir := t.TempDir()
	if err := home.EnqueueWake(homeDir, home.ProcessEventWakeKind, "merged-poll:task-1", `{"eventId":"merged-poll:task-1","generation":1,"payload":"task-1"}`); err != nil {
		t.Fatal(err)
	}
	if err := home.EnqueueWake(homeDir, "signal", "task-2", "PR merged"); err != nil {
		t.Fatal(err)
	}

	d, err := OneCycle(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || len(d.Escalated)+len(d.Routines) != 1 {
		t.Fatalf("digest = %+v, want exactly the signal wake", d)
	}
	for _, wd := range append(d.Escalated, d.Routines...) {
		if wd.Kind == home.ProcessEventWakeKind {
			t.Fatalf("process-event wake digested: %+v", wd)
		}
	}
	left, err := home.DrainWakesOfKind(homeDir, home.ProcessEventWakeKind)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Key != "merged-poll:task-1" {
		t.Fatalf("queue after digest = %#v, want the process-event wake still queued", left)
	}

	d, err = OneCycle(homeDir)
	if err != nil || d != nil {
		t.Fatalf("second cycle = %+v, %v; want nil digest on an empty queue", d, err)
	}
}
