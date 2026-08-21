// Deterministic tests for the typed internal watcher-cycle measurements
// (issue #546): scan count, measured stale age per task, and duplicate
// suppression. These assert the observation capture at the internals level and
// verify that wall-clock age never leaks into the wake message or fingerprint.
package orchestrator

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// TestCycleObservation_ScannedCount asserts the watcher scan count equals the
// number of task .meta files examined for staleness in one cycle. Deterministic
// with a fixed set of alive tasks: none wake, all are counted.
func TestLogCycleObservation_EmitsStaleTaskAndAge(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = writer
	logCycleObservation(&CycleObservation{
		ScannedTasks: 1,
		StaleByTask:  map[string]time.Duration{"task-x": 12 * time.Second},
	})
	_ = writer.Close()
	os.Stderr = previous
	output, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "task=task-x age=12s") {
		t.Fatalf("observation output %q omits stale task identity or age", output)
	}
}

func TestCycleObservation_ScannedCount(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 4; i++ {
		mustWriteMeta(t, home, "task-"+string(rune('a'+i)), map[string]string{
			"backend": "herdr",
			"window":  "w:" + string(rune('a'+i)),
		})
	}

	// aliveTaskProbe reports every pane alive, so no task wakes.
	obs := newCycleObservation()
	reasons := scanFleetWithProbe(home, false, aliveTaskProbe{}, testTaskStatePort{}, obs)
	if len(reasons) != 0 {
		t.Fatalf("alive fleet produced %d wake reasons, want 0", len(reasons))
	}
	if obs.ScannedTasks != 4 {
		t.Fatalf("scanned = %d, want 4", obs.ScannedTasks)
	}
	if len(obs.StaleByTask) != 0 {
		t.Fatalf("stale map = %v, want empty for alive tasks", obs.StaleByTask)
	}
}

// aliveTaskProbe reports every pane alive on any task.
type aliveTaskProbe struct{}

func (aliveTaskProbe) Probe(string, map[string]string) (bool, error) { return true, nil }

// TestStaleAge_Exact is deterministic: it seeds the continuous-stale first-seen
// time directly and asserts the measured age is exactly the injected now-minus-
// first-seen, independent of the real clock. The unknown-task case measures 0.
func TestStaleAge_Exact(t *testing.T) {
	ref := time.Unix(1700000000, 0)
	setStaleFirstSeen("task-x", ref.Add(-12*time.Second))
	defer setStaleFirstSeen("task-x", time.Time{})

	if got := staleAge("task-x", ref); got != 12*time.Second {
		t.Fatalf("staleAge = %v, want exactly 12s", got)
	}
	if got := staleAge("unknown-task", ref); got != 0 {
		t.Fatalf("staleAge of unknown task = %v, want 0", got)
	}
}

// TestCycleObservation_StaleAgeCaptured exercises the real scan path: a stale
// (dead-pane) task is captured into the observation with its measured age. The
// age comes from the real clock, so it is asserted in a wide band; the durable
// invariant is that the age is reported ONLY as an observation, never in the
// wake message or fingerprint (which would break duplicate suppression).
func TestCycleObservation_StaleAgeCaptured(t *testing.T) {
	home := t.TempDir()
	mustWriteMeta(t, home, "task-z", map[string]string{
		"backend": "herdr",
		"window":  "w:z",
	})
	// No status file -> a dead pane is not general-relevant, not absorbable.
	// Seed the continuous-stale first-seen a moment ago on the real clock.
	seedAge := 5 * time.Second
	setStaleFirstSeen("task-z", time.Now().Add(-seedAge))
	defer setStaleFirstSeen("task-z", time.Time{})

	obs := newCycleObservation()
	reasons := scanFleetWithProbe(home, false, testEndpointProbe{}, testTaskStatePort{}, obs)
	if len(reasons) != 1 || reasons[0].Kind != "stale" {
		t.Fatalf("expected one stale reason, got %v", reasons)
	}
	if got, ok := obs.StaleByTask["task-z"]; !ok || got < seedAge-2*time.Second || got > seedAge+2*time.Second {
		t.Fatalf("observed stale age = %v (present=%v), want ~%v", got, ok, seedAge)
	}
	// The wall-clock age must NOT appear in the wake message (duplicate
	// suppression depends on a stable message/fingerprint).
	if msg := reasons[0].Message; msg != "pane w:z is dead" {
		t.Fatalf("wake message = %q, want stable message with no wall-clock age", msg)
	}
	if fp := wakeFingerprint(home, reasons[0]); containsSubstr(fp, "1700000000") || containsSubstr(fp, "5s") {
		t.Fatalf("fingerprint %q must not contain any wall-clock age", fp)
	}
}

// TestCycleObservation_DuplicateSuppression runs two real cycles over a stale
// task and asserts the second cycle suppresses the duplicate (one wake in the
// queue) and records that suppression in the observation. Deterministic because
// both cycles run far below the deep-inspection threshold and the stale
// fingerprint is stable.
func TestCycleObservation_CheckDuplicateSuppression(t *testing.T) {
	home := t.TempDir()
	checksDir := filepath.Join(home, "state", "checks")
	if err := os.MkdirAll(checksDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checksDir, "global.check"), []byte("#!/bin/sh\necho ready\n"), 0644); err != nil {
		t.Fatal(err)
	}
	resetRecovery()
	obs1 := newCycleObservation()
	if _, err := runCycleWithProbeAndSender(home, testEndpointProbe{}, testCycleSender{}, activeTestHooks, &testRetirementPort{}, testTaskStatePort{}, obs1); err != nil {
		t.Fatalf("cycle 1: %v", err)
	}
	if obs1.SuppressedDuplicates != 0 {
		t.Fatalf("cycle 1 suppressed = %d, want 0", obs1.SuppressedDuplicates)
	}
	obs2 := newCycleObservation()
	if _, err := runCycleWithProbeAndSender(home, testEndpointProbe{}, testCycleSender{}, activeTestHooks, &testRetirementPort{}, testTaskStatePort{}, obs2); err != nil {
		t.Fatalf("cycle 2: %v", err)
	}
	if obs2.SuppressedDuplicates != 1 {
		t.Fatalf("cycle 2 suppressed = %d, want 1 for duplicate check wake", obs2.SuppressedDuplicates)
	}
	records, err := mhome.DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes: %v", err)
	}
	if len(records) != 1 || records[0].Kind != "check" {
		t.Fatalf("wake records = %#v, want one check wake", records)
	}
}

func TestCycleObservation_DuplicateSuppression(t *testing.T) {
	home := t.TempDir()
	mustWriteMeta(t, home, "task-d", map[string]string{
		"backend": "herdr",
		"window":  "w:d",
	})
	if mhome.HasQueuedWakes(home) {
		t.Fatal("queue should start empty")
	}

	// Cycle 1: the stale wake is enqueued and its marker written; nothing
	// suppressed.
	obs1 := newCycleObservation()
	obs1, err1 := runCycleObs(home, obs1)
	if err1 != nil {
		t.Fatalf("cycle 1: %v", err1)
	}
	if obs1.SuppressedDuplicates != 0 {
		t.Fatalf("cycle 1 suppressed = %d, want 0", obs1.SuppressedDuplicates)
	}
	if !mhome.HasQueuedWakes(home) {
		t.Fatal("cycle 1 should have enqueued a wake")
	}

	// Cycle 2: the same stale fingerprint matches its marker, so the wake is
	// suppressed as a duplicate and no second wake is added.
	obs2 := newCycleObservation()
	obs2, err2 := runCycleObs(home, obs2)
	if err2 != nil {
		t.Fatalf("cycle 2: %v", err2)
	}
	if obs2.SuppressedDuplicates != 1 {
		t.Fatalf("cycle 2 suppressed = %d, want 1", obs2.SuppressedDuplicates)
	}
	recs, err := mhome.DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("queue held %d wakes after two cycles, want 1 (duplicate suppressed)", len(recs))
	}
}

// runCycleObs runs one full scan/enqueue cycle with the observation capture.
func runCycleObs(home string, obs *CycleObservation) (*CycleObservation, error) {
	_, err := runCycleWithProbeAndSender(home, testEndpointProbe{}, testCycleSender{}, activeTestHooks, NoopRetirementPort{}, testTaskStatePort{}, obs)
	return obs, err
}

// setStaleFirstSeen seeds the continuous-stale first-seen time for a task so a
// test can assert an exact observed age.
func setStaleFirstSeen(id string, at time.Time) {
	staleFirstSeenMu.Lock()
	defer staleFirstSeenMu.Unlock()
	if at.IsZero() {
		delete(staleFirstSeen, id)
		return
	}
	staleFirstSeen[id] = at
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
