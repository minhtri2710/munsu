package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// mustReadWakeQueue reads the queue without draining it.
func mustReadWakeQueue(t *testing.T, homeDir string) []WakeRecord {
	t.Helper()
	records, err := home.PeekWakes(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func runConsumerCycle(t *testing.T, homeDir string) {
	t.Helper()
	resetRecovery()
	if _, err := RunCycleWithProbeAndSender(homeDir, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, nil, testTaskStatePort{}); err != nil {
		t.Fatalf("run cycle: %v", err)
	}
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		done <- string(out)
	}()
	original := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = original
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func announcedWake(t *testing.T, eventID string, generation uint64, payload string) string {
	t.Helper()
	data, err := json.Marshal(ProcessEventWake{EventID: eventID, Generation: generation, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunCycle_ConsumerDropsUnusableWakes(t *testing.T) {
	const eventID = "no-owner:task-1"
	for _, tc := range []struct {
		name, key, payload, want string
		setup                    func(homeDir string)
	}{
		{name: "undecodable payload", key: eventID, payload: "not json", want: "undecodable payload"},
		{name: "no record", key: "no-owner:ghost", payload: announcedWake(t, "no-owner:ghost", 1, "ghost"), want: "no record"},
		{name: "superseded generation", key: eventID, payload: announcedWake(t, eventID, 1, "task-1"), want: "generation 1 superseded by 2", setup: func(homeDir string) {
			for i := 0; i < 2; i++ {
				if _, err := RegisterProcessEvent(homeDir, eventID, "task-1"); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{name: "unresolved record", key: eventID, payload: announcedWake(t, eventID, 1, "task-1"), want: "record is not resolved", setup: func(homeDir string) {
			if _, err := RegisterProcessEvent(homeDir, eventID, "task-1"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unowned prefix", key: "no-owner:gate", payload: announcedWake(t, "no-owner:gate", 1, "gate"), want: "no owner for this event prefix", setup: func(homeDir string) {
			if _, err := RegisterProcessEvent(homeDir, "no-owner:gate", "gate"); err != nil {
				t.Fatal(err)
			}
			calls := 0
			if err := EvaluateProcessEvent(context.Background(), homeDir, "no-owner:gate", resolvedWith("x", &calls)); err != nil {
				t.Fatal(err)
			}
			if _, err := home.DrainWakesOfKind(homeDir, ProcessEventWakeKind); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			if tc.setup != nil {
				tc.setup(homeDir)
			}
			if err := home.EnqueueWake(homeDir, ProcessEventWakeKind, tc.key, tc.payload); err != nil {
				t.Fatal(err)
			}
			out := captureStderr(t, func() { runConsumerCycle(t, homeDir) })
			if !strings.Contains(out, "dropped: "+tc.want) {
				t.Fatalf("stderr = %q, want dropped: %s", out, tc.want)
			}
			for _, r := range mustReadWakeQueue(t, homeDir) {
				if r.Kind == ProcessEventWakeKind {
					t.Fatalf("dropped wake re-queued: %#v", r)
				}
			}
		})
	}
}

// The consumer never leases: the wake is consumed by the watcher and is not
// claimable by agents in the meantime.
func TestProcessEventWakeIsNotClaimable(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := RegisterProcessEvent(homeDir, "no-owner:task-1", "task-1"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := EvaluateProcessEvent(context.Background(), homeDir, "no-owner:task-1", resolvedWith("x", &calls)); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimWakes(homeDir, "munsu:herdr", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claim.Wakes) != 0 {
		t.Fatalf("claimed %#v, want nothing: process-event wakes are reserved", claim.Wakes)
	}
	if HasQueuedWakes(homeDir) {
		t.Fatal("HasQueuedWakes true for a queue holding only the process-event wake")
	}
	if q := mustReadWakeQueue(t, homeDir); len(q) != 1 || q[0].Kind != ProcessEventWakeKind {
		t.Fatalf("queue = %#v, want the announcement still queued", q)
	}
}
