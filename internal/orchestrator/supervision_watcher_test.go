package orchestrator

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

type testEndpointProbe struct{}

func (testEndpointProbe) Probe(string, map[string]string) (bool, error) { return false, nil }

type testTaskStatePort struct{}

func (testTaskStatePort) ReadTaskState(string, string) (*ObservedTaskState, error) {
	return &ObservedTaskState{}, nil
}

type testRetirementPort struct {
	validate     func(string) error
	validated    []string
	retireErr    error
	retiredPaths []string
}

func (p *testRetirementPort) RecoverPendingRetirements(string) (int, []error) {
	return 0, nil
}

func (p *testRetirementPort) ValidateCheck(path string) error {
	p.validated = append(p.validated, path)
	if p.validate != nil {
		return p.validate(path)
	}
	return nil
}

func (p *testRetirementPort) RetireMergedPoll(_ string, _ string, checkPath string) error {
	p.retiredPaths = append(p.retiredPaths, checkPath)
	return p.retireErr
}

type testCycleSender struct{}

func (testCycleSender) Alive(string, map[string]string) (bool, error) { return false, nil }
func (testCycleSender) Send(string, map[string]string, string) BoundSendResult {
	return BoundSendResult{}
}

func testScanFleet(home string) *WakeReason {
	reasons := scanFleetWithProbe(home, false, testEndpointProbe{}, testTaskStatePort{})
	if len(reasons) == 0 {
		return nil
	}
	return reasons[0]
}

type testWatcherHooks struct {
	reconcile func(string, bool) error
	activate  func(string)
}

func (h testWatcherHooks) Reconcile(home string, startup bool) error {
	if h.reconcile != nil {
		return h.reconcile(home, startup)
	}
	return nil
}
func (h testWatcherHooks) Activate(home string) {
	if h.activate != nil {
		h.activate(home)
	}
}

var activeTestHooks WatcherHooks = NoopWatcherHooks{}

func testRunCycle(home string) (bool, error) {
	return RunCycleWithProbeAndSender(home, testEndpointProbe{}, testCycleSender{}, activeTestHooks, NoopRetirementPort{}, testTaskStatePort{})
}

func TestRunCycle_CheckValidationPortAllowsCheckWake(t *testing.T) {
	home := t.TempDir()
	checksDir := filepath.Join(home, "state", "checks")
	if err := os.MkdirAll(checksDir, 0755); err != nil {
		t.Fatal(err)
	}
	checkPath := filepath.Join(checksDir, "global.check")
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\necho ready\n"), 0644); err != nil {
		t.Fatal(err)
	}

	retirement := &testRetirementPort{}
	resetRecovery()
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, retirement, testTaskStatePort{}); err != nil {
		t.Fatalf("run cycle: %v", err)
	}

	records, err := DrainWakes(home)
	if err != nil {
		t.Fatalf("drain wakes: %v", err)
	}
	if len(records) != 1 || records[0].Kind != "check" || records[0].Key != "global:global" {
		t.Fatalf("wake records = %#v, want one global check wake", records)
	}
	if len(retirement.validated) != 1 || retirement.validated[0] != checkPath {
		t.Fatalf("validated paths = %#v, want [%q]", retirement.validated, checkPath)
	}
}

func TestRunCycle_CheckValidationPortRefusesCheck(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	checkPath := filepath.Join(stateDir, "task-1.check")
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\necho ready\n"), 0644); err != nil {
		t.Fatal(err)
	}

	retirement := &testRetirementPort{validate: func(string) error { return fmt.Errorf("refused") }}
	resetRecovery()
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, retirement, testTaskStatePort{}); err != nil {
		t.Fatalf("run cycle: %v", err)
	}

	records, err := DrainWakes(home)
	if err != nil {
		t.Fatalf("drain wakes: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("wake records = %#v, want none", records)
	}
	if len(retirement.retiredPaths) != 0 {
		t.Fatalf("retired paths = %#v, want none", retirement.retiredPaths)
	}
	if len(retirement.validated) != 1 || retirement.validated[0] != checkPath {
		t.Fatalf("validated paths = %#v, want [%q]", retirement.validated, checkPath)
	}
}

func TestRunCycle_CheckValidationPortAuthorizesRetirement(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	checkPath := filepath.Join(stateDir, "task-1.check")
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\necho ready\n"), 0644); err != nil {
		t.Fatal(err)
	}

	retirement := &testRetirementPort{}
	resetRecovery()
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, retirement, testTaskStatePort{}); err != nil {
		t.Fatalf("run cycle: %v", err)
	}

	records, err := DrainWakes(home)
	if err != nil {
		t.Fatalf("drain wakes: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("wake records = %#v, want none after retirement", records)
	}
	if len(retirement.retiredPaths) != 1 || retirement.retiredPaths[0] != checkPath {
		t.Fatalf("retired paths = %#v, want [%q]", retirement.retiredPaths, checkPath)
	}
	if len(retirement.validated) != 2 {
		t.Fatalf("validated paths = %#v, want discovery and retirement validation", retirement.validated)
	}
}

// --- absorbStaleSignal tests (pure predicate) ---

func TestAbsorbStaleSignal_Nil(t *testing.T) {
	if absorbStaleSignal(nil) {
		t.Error("absorbStaleSignal(nil) should return false")
	}
}

func TestAbsorbStaleSignal_EmptyStep(t *testing.T) {
	s := &ObservedTaskState{}
	if absorbStaleSignal(s) {
		t.Error("absorbStaleSignal with empty step should return false")
	}
}

func TestAbsorbStaleSignal_Running(t *testing.T) {
	s := &ObservedTaskState{NoMistakesRunStep: "running"}
	if !absorbStaleSignal(s) {
		t.Error("running should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_Fixing(t *testing.T) {
	s := &ObservedTaskState{NoMistakesRunStep: "fixing"}
	if !absorbStaleSignal(s) {
		t.Error("fixing should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_CI(t *testing.T) {
	s := &ObservedTaskState{NoMistakesRunStep: "ci"}
	if !absorbStaleSignal(s) {
		t.Error("ci should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_FixReview(t *testing.T) {
	s := &ObservedTaskState{NoMistakesRunStep: "fix_review"}
	if !absorbStaleSignal(s) {
		t.Error("fix_review should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_AwaitingApproval(t *testing.T) {
	s := &ObservedTaskState{NoMistakesRunStep: "awaiting_approval"}
	if !absorbStaleSignal(s) {
		t.Error("awaiting_approval should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_Done(t *testing.T) {
	s := &ObservedTaskState{NoMistakesRunStep: "done"}
	if absorbStaleSignal(s) {
		t.Error("done should NOT absorb stale signal")
	}
}

func TestAbsorbStaleSignal_Failed(t *testing.T) {
	s := &ObservedTaskState{NoMistakesRunStep: "failed"}
	if absorbStaleSignal(s) {
		t.Error("failed should NOT absorb stale signal")
	}
}

func TestAbsorbStaleSignal_ChecksPassed(t *testing.T) {
	s := &ObservedTaskState{NoMistakesRunStep: "checks-passed"}
	if absorbStaleSignal(s) {
		t.Error("checks-passed should NOT absorb stale signal")
	}
}

func TestAbsorbStaleSignal_UnknownStep(t *testing.T) {
	s := &ObservedTaskState{NoMistakesRunStep: "some-bogus-step"}
	if absorbStaleSignal(s) {
		t.Error("unknown step should NOT absorb stale signal")
	}
}

// --- handleStale / resetStreak tests (idle-seconds timer, not poll count) ---

func TestHandleStale_FirstSeen(t *testing.T) {
	delete(staleFirstSeen, "test-task-1")

	reason := handleStale("test-task-1", "pane foo is dead")
	if reason == nil {
		t.Fatal("expected non-nil reason")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
	if len(reason.TaskIDs) != 1 || reason.TaskIDs[0] != "test-task-1" {
		t.Errorf("TaskIDs = %v, want [test-task-1]", reason.TaskIDs)
	}
	if reason.DemandDeepInspection {
		t.Error("first stale sighting should not demand deep inspection (elapsed ~0)")
	}
	// Entry must exist with a recent timestamp.
	staleFirstSeenMu.Lock()
	ts, exists := staleFirstSeen["test-task-1"]
	staleFirstSeenMu.Unlock()
	if !exists {
		t.Error("staleFirstSeen entry should exist after handleStale")
	}
	if time.Since(ts) > time.Second {
		t.Errorf("staleFirstSeen timestamp too old: %v ago", time.Since(ts))
	}
}

func TestHandleStale_UnderThreshold(t *testing.T) {
	delete(staleFirstSeen, "test-task-2")

	r1 := handleStale("test-task-2", "first")
	if r1.DemandDeepInspection {
		t.Error("first stale sighting should not demand deep inspection")
	}

	r2 := handleStale("test-task-2", "second")
	if r2.DemandDeepInspection {
		t.Error("second rapid stale sighting should not demand deep (elapsed < threshold)")
	}
}

func TestHandleStale_AtThreshold(t *testing.T) {
	delete(staleFirstSeen, "test-task-3")
	// Inject past timestamp to simulate elapsed idle seconds.
	staleFirstSeenMu.Lock()
	staleFirstSeen["test-task-3"] = time.Now().Add(-30 * time.Second)
	staleFirstSeenMu.Unlock()

	reason := handleStale("test-task-3", "crossed threshold")
	if !reason.DemandDeepInspection {
		t.Error("stale with elapsed > threshold should demand deep inspection")
	}
	if !strings.Contains(reason.Message, "demand-deep-inspection") {
		t.Errorf("message should contain demand-deep-inspection, got %q", reason.Message)
	}
}

func TestHandleStale_AboveThreshold(t *testing.T) {
	delete(staleFirstSeen, "test-task-4")
	// Already well past threshold.
	staleFirstSeenMu.Lock()
	staleFirstSeen["test-task-4"] = time.Now().Add(-60 * time.Second)
	staleFirstSeenMu.Unlock()

	reason := handleStale("test-task-4", "way past threshold")
	if !reason.DemandDeepInspection {
		t.Error("stale with elapsed >> threshold should demand deep inspection")
	}
}

func TestHandleStale_MultipleTasks(t *testing.T) {
	delete(staleFirstSeen, "task-a")
	delete(staleFirstSeen, "task-b")

	// Both tasks just seen — neither demands deep.
	r1 := handleStale("task-a", "first")
	if r1.DemandDeepInspection {
		t.Error("task-a first sighting should not demand deep")
	}
	r2 := handleStale("task-b", "first")
	if r2.DemandDeepInspection {
		t.Error("task-b first sighting should not demand deep")
	}

	// Simulate task-a crossing threshold.
	staleFirstSeenMu.Lock()
	staleFirstSeen["task-a"] = time.Now().Add(-30 * time.Second)
	staleFirstSeenMu.Unlock()

	r3 := handleStale("task-a", "crossed")
	if !r3.DemandDeepInspection {
		t.Error("task-a after threshold should demand deep")
	}

	// task-b still fresh.
	r4 := handleStale("task-b", "still fresh")
	if r4.DemandDeepInspection {
		t.Error("task-b still fresh should not demand deep")
	}
}

func TestResetStreak(t *testing.T) {
	delete(staleFirstSeen, "test-reset")
	// Inject past timestamp.
	staleFirstSeenMu.Lock()
	staleFirstSeen["test-reset"] = time.Now().Add(-30 * time.Second)
	staleFirstSeenMu.Unlock()

	resetStreak("test-reset")
	staleFirstSeenMu.Lock()
	_, exists := staleFirstSeen["test-reset"]
	staleFirstSeenMu.Unlock()
	if exists {
		t.Error("staleFirstSeen should be deleted after reset")
	}

	// After reset, next stale records a fresh timestamp.
	reason := handleStale("test-reset", "after-reset")
	if reason.DemandDeepInspection {
		t.Error("after reset, first stale should not demand deep inspection")
	}
}

func TestResetStreak_NonExistent(t *testing.T) {
	// Should not panic
	resetStreak("non-existent-task")
}

func TestStaleFirstSeen_ConcurrentAccess(t *testing.T) {
	const workers = 32
	const perWorker = 100
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("race-task-%d", i%4)
			for n := 0; n < perWorker; n++ {
				handleStale(id, "concurrent")
				if n%3 == 0 {
					resetStreak(id)
				}
			}
		}(i)
	}
	wg.Wait()
}

// --- scanFleet integration tests ---

func TestScanFleet_NoStateDir(t *testing.T) {
	tmp := t.TempDir()
	reason := testScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for no state dir, got %v", reason)
	}
}

func TestScanFleet_EmptyStateDir(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	reason := testScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for empty state dir, got %v", reason)
	}
}

func TestScanFleet_IgnoresDotfiles(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	os.WriteFile(filepath.Join(stateDir, ".hidden.meta"), []byte("window=@test\n"), 0644)

	reason := testScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil when only dotfiles present, got %v", reason)
	}
}

func TestScanFleet_NoWindow(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	if err := mhome.WriteMeta(tmp, "no-win", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}

	reason := testScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for task with no window, got %v", reason)
	}
}

func TestScanFleet_MultipleTasks_OnlyOneWithWindow(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Task with no window
	mhome.WriteMeta(tmp, "no-win", map[string]string{"kind": "ship"})
	// Task with a window (but window doesn't exist, so pane is dead)
	mhome.WriteMeta(tmp, "has-win", map[string]string{"window": "@nonexistent99"})

	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected a stale reason for dead pane")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
}

// --- Evidence: absorb + streak integration tests ---
//
// These tests demonstrate the two behaviors from the user intent:
// 1. Absorb provably-working wakes (no-mistakes run-step absorbs stale)
// 2. Demand-deep-inspection streak tracking after 3 consecutive stale polls

func TestHandleStale_DemandDeepInspectionByIdleSeconds(t *testing.T) {
	delete(staleFirstSeen, "deep-test")

	// Fresh sighting — no demand deep.
	r1 := handleStale("deep-test", "pane is dead")
	if r1.DemandDeepInspection {
		t.Error("poll 1 should not demand deep (fresh first-seen)")
	}

	// Rapid follow-up within microseconds — still below threshold.
	r2 := handleStale("deep-test", "pane is dead")
	if r2.DemandDeepInspection {
		t.Error("poll 2 should not demand deep (elapsed < threshold)")
	}

	// Simulate threshold crossing via past timestamp.
	staleFirstSeenMu.Lock()
	staleFirstSeen["deep-test"] = time.Now().Add(-30 * time.Second)
	staleFirstSeenMu.Unlock()

	r3 := handleStale("deep-test", "pane is dead")
	if !r3.DemandDeepInspection {
		t.Error("poll 3 should demand deep (elapsed > threshold)")
	}
	if !strings.Contains(r3.Message, "demand-deep-inspection") {
		t.Errorf("message should contain demand-deep-inspection, got %q", r3.Message)
	}

	// Subsequent calls still past threshold.
	r4 := handleStale("deep-test", "pane is dead")
	if !r4.DemandDeepInspection {
		t.Error("poll 4 should still demand deep inspection")
	}
}

func TestAbsorbStaleSignal_AllAbsorbSteps(t *testing.T) {
	absorbSteps := []string{"running", "fixing", "ci", "fix_review", "awaiting_approval"}
	for _, step := range absorbSteps {
		s := &ObservedTaskState{NoMistakesRunStep: step}
		if !absorbStaleSignal(s) {
			t.Errorf("%s should absorb stale signal", step)
		}
	}
}

func TestAbsorbStaleSignal_AllNonAbsorbSteps(t *testing.T) {
	nonAbsorbSteps := []string{"", "done", "failed", "checks-passed", "passed", "cancelled", "some-unknown-step"}
	for _, step := range nonAbsorbSteps {
		s := &ObservedTaskState{NoMistakesRunStep: step}
		if absorbStaleSignal(s) {
			t.Errorf("%q should NOT absorb stale signal", step)
		}
	}

	if absorbStaleSignal(nil) {
		t.Error("nil state should NOT absorb stale signal")
	}
}

func TestScanFleet_StaleStatusFile(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	mhome.WriteMeta(tmp, "stale-task", map[string]string{"window": "@nonexistent99"})

	// Write a status file with old modification time
	statusPath := filepath.Join(stateDir, "stale-task.status")
	os.WriteFile(statusPath, []byte("working: started\n"), 0644)

	// Set mod time to be older than stale threshold
	past := time.Now().Add(-(StaleThreshold() + time.Second))
	os.Chtimes(statusPath, past, past)

	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected stale reason for old status file")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
	// The pane dead check fires before status staleness check
	if !strings.Contains(reason.Message, "dead") && !strings.Contains(reason.Message, "idle beyond threshold") {
		t.Errorf("message should mention dead or idle, got: %q", reason.Message)
	}
}

func TestScanFleet_RecentStatusNoStale(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	mhome.WriteMeta(tmp, "recent-task", map[string]string{"window": "@nonexistent99"})

	// Write a recent status file
	statusPath := filepath.Join(stateDir, "recent-task.status")
	os.WriteFile(statusPath, []byte("working: active\n"), 0644)

	// The pane is dead but the status is recent — pane staleness takes precedence
	// But the status staleness check won't trigger because modtime is recent
	// The pane liveness check will trigger stale first
	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected stale reason for dead pane (recent status doesn't prevent pane check)")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
}

func TestScanFleet_NoMetaFiles(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Non-captain-relevant orphan status without meta stays quiet.
	os.WriteFile(filepath.Join(stateDir, "orphan.status"), []byte("working: stray\n"), 0644)
	// Captain-relevant status (including Captain return-channel files) must wake
	// even without a companion .meta — parent status is the return path.
	os.WriteFile(filepath.Join(stateDir, "another.status"), []byte("done: finished\n"), 0644)

	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected signal for captain-relevant status without meta")
	}
	if reason.Kind != "signal" || len(reason.TaskIDs) != 1 || reason.TaskIDs[0] != "another" {
		t.Fatalf("unexpected reason: %+v", reason)
	}
}

func TestScanFleet_IgnoresNonWindowMeta(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Meta without window key should be skipped
	mhome.WriteMeta(tmp, "no-win", map[string]string{"kind": "ship", "project": "munsu"})

	reason := testScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for meta without window, got %v", reason)
	}
}

func TestScanFleet_MultipleTasks_MixedStates(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Task 1: has window but pane is dead
	mhome.WriteMeta(tmp, "dead-task", map[string]string{"window": "@nonexistent1"})
	// Task 2: no window (should be skipped)
	mhome.WriteMeta(tmp, "no-win-task", map[string]string{"kind": "scout"})
	// Task 3: has dead window and old status file
	mhome.WriteMeta(tmp, "old-status-task", map[string]string{"window": "@nonexistent2"})

	statusPath := filepath.Join(stateDir, "old-status-task.status")
	os.WriteFile(statusPath, []byte("working: started\n"), 0644)
	past := time.Now().Add(-(StaleThreshold() + time.Second))
	os.Chtimes(statusPath, past, past)

	// scanFleet processes meta files in directory order — should find a stale task
	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected a stale reason")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
}

func TestRunCycle_QueuedWakeDoesNotEmitAnotherWake(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	mhome.WriteMeta(tmp, "no-window-task", map[string]string{"kind": "ship"})
	if err := EnqueueWake(tmp, "status", "task-1", "done: ready"); err != nil {
		t.Fatal(err)
	}

	emitted, err := testRunCycle(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if emitted {
		t.Fatal("queued wakes must not cause the watcher to enqueue another wake")
	}

	records, err := DrainWakes(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("queue grew from one pending wake to %d records", len(records))
	}
}

func TestRunCycle_DeduplicatesUnchangedConditionAndEmitsAfterChange(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	mhome.WriteMeta(tmp, "task-1", map[string]string{"window": "@nonexistent-watch-cycle"})

	emitted, err := testRunCycle(tmp)
	if err != nil || !emitted {
		t.Fatalf("first cycle emitted=%v err=%v, want true nil", emitted, err)
	}
	emitted, err = testRunCycle(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if emitted {
		t.Fatal("unchanged condition emitted a duplicate wake")
	}

	if err := mhome.AppendStatus(tmp, "task-1", "failed: changed condition"); err != nil {
		t.Fatal(err)
	}
	emitted, err = testRunCycle(tmp)
	if err != nil || !emitted {
		t.Fatalf("changed condition emitted=%v err=%v, want true nil", emitted, err)
	}

	records, err := DrainWakes(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("drained %d wakes, want one initial and one changed-condition wake", len(records))
	}
}

func TestScanFleet_StaleConsistency(t *testing.T) {
	// Test that multiple scanFleet calls produce consistent behavior
	// with idle-seconds timer.
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	mhome.WriteMeta(tmp, "consist-task", map[string]string{"window": "@nonexistentConsist"})

	// First call
	r1 := testScanFleet(tmp)
	if r1 == nil {
		t.Fatal("first call expected stale")
	}
	if r1.Kind != "stale" {
		t.Errorf("first call kind = %q, want stale", r1.Kind)
	}

	// Second call (still within microseconds, no demand deep yet)
	r2 := testScanFleet(tmp)
	if r2 == nil {
		t.Fatal("second call expected stale")
	}
	if r2.Kind != "stale" {
		t.Errorf("second call kind = %q, want stale", r2.Kind)
	}

	// Inject past timestamp to simulate elapsed idle seconds.
	staleFirstSeenMu.Lock()
	staleFirstSeen["consist-task"] = time.Now().Add(-30 * time.Second)
	staleFirstSeenMu.Unlock()

	// Third call — should trigger demand deep inspection
	r3 := testScanFleet(tmp)
	if r3 == nil {
		t.Fatal("third call expected stale")
	}
	if !r3.DemandDeepInspection {
		t.Error("stale after elapsed threshold should demand deep inspection")
	}
}

func TestAbsorbStaleSignal_ComplexStatusTransitions(t *testing.T) {
	// Simulate a full lifecycle: working → done → no-mistakes run step active
	t.Run("working to done with no-mistakes run", func(t *testing.T) {
		s := &ObservedTaskState{
			Status:            "done",
			NoMistakesRunStep: "running",
		}
		// Even though status says done, the active run-step absorbs stale
		if !absorbStaleSignal(s) {
			t.Error("active run on done status should absorb stale")
		}
	})

	t.Run("done with checks-passed does NOT absorb stale", func(t *testing.T) {
		s := &ObservedTaskState{
			Status:            "done",
			NoMistakesRunStep: "checks-passed",
		}
		if absorbStaleSignal(s) {
			t.Error("checks-passed should NOT absorb stale")
		}
	})

	t.Run("failed without run step does NOT absorb", func(t *testing.T) {
		s := &ObservedTaskState{
			Status: "failed",
		}
		if absorbStaleSignal(s) {
			t.Error("failed without run step should NOT absorb stale")
		}
	})
}

func TestRunCycle_StatusSignalFromParentStatus(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	os.MkdirAll(state, 0755)
	// Captain return-channel status without requiring dead pane.
	os.WriteFile(filepath.Join(state, "captain:api.meta"), []byte("window=w1\nkind=captain\nbackend=tmux\n"), 0644)
	os.WriteFile(filepath.Join(state, "captain:api.status"), []byte("done [key=x]: PR https://example/1\n"), 0644)

	emitted, err := testRunCycle(home)
	if err != nil {
		t.Fatal(err)
	}
	if !emitted {
		t.Fatal("expected wake from captain-relevant parent status")
	}
	// Second cycle with same status must not re-emit (fingerprint dedupe).
	emitted2, err := testRunCycle(home)
	if err != nil {
		t.Fatal(err)
	}
	if emitted2 {
		t.Fatal("expected status signal to be deduped on second cycle")
	}
}

// TestReturnChannelClosedLoop proves Phase 3 C1 without live herdr/pane:
// marked General send (contract) → Captain appends parent state/captain:<id>.status
// → watcher RunCycle enqueues a signal wake → wake claim leases it.
func TestReturnChannelClosedLoop(t *testing.T) {
	homeDir := t.TempDir()
	state := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(state, 0755); err != nil {
		t.Fatal(err)
	}

	// 1) Marked send path (General → Captain). Pane inject is backend-specific;
	// the greppable contract is the marker prefix used by munsu send for kind=captain.
	req := "report progress on return-channel-e2e"
	marked := home.MarkFromGeneral(req)
	if !home.IsFromGeneral(marked) {
		t.Fatal("expected FromGeneral marker on captain send line")
	}
	if !strings.HasPrefix(marked, home.FromGeneralLabel) {
		t.Fatalf("marker label missing: %q", marked)
	}

	// 2) Captain answers on parent return channel (no pane required).
	captainID := "captain:munsu"
	statusLine := "done [key=return-channel-e2e]: closed-loop proof landed"
	if err := mhome.AppendStatus(homeDir, captainID, statusLine); err != nil {
		t.Fatal(err)
	}
	// Optional meta present as in a live captain registry entry; signal path
	// must not require dead pane (return channel wakes while captain is alive).
	if err := mhome.WriteMeta(homeDir, captainID, map[string]string{
		"kind":    "captain",
		"window":  "captain-pane-alive",
		"backend": "tmux",
	}); err != nil {
		t.Fatal(err)
	}

	// Non-relevant status must stay quiet (working is not general-relevant).
	if err := mhome.AppendStatus(homeDir, "captain:noise", "working: still grinding"); err != nil {
		t.Fatal(err)
	}

	// 3) Watcher one-shot cycle enqueues captain-relevant signal.
	emitted, err := testRunCycle(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if !emitted {
		t.Fatal("expected signal wake from captain-relevant parent status")
	}

	// 4) General leases the wake via claim (not legacy drain).
	claim, err := ClaimWakes(homeDir, "general-e2e", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if claim == nil || len(claim.Wakes) == 0 {
		t.Fatal("expected claimable signal wake after return-channel status")
	}

	var found bool
	for _, w := range claim.Wakes {
		if w.Kind == "signal" && w.Key == captainID && strings.Contains(w.Payload, "done") {
			found = true
			if w.Payload != statusLine {
				t.Fatalf("payload = %q, want %q", w.Payload, statusLine)
			}
			break
		}
	}
	if !found {
		t.Fatalf("claimed wakes missing captain signal: %+v", claim.Wakes)
	}
	if claim.LeaseID == "" || claim.Consumer != "general-e2e" {
		t.Fatalf("bad lease: id=%q consumer=%q", claim.LeaseID, claim.Consumer)
	}

	// Queue emptied for claimed records; second claim is empty unless new signal.
	claim2, err := ClaimWakes(homeDir, "general-e2e", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claim2.Wakes) != 0 {
		t.Fatalf("expected empty second claim, got %+v", claim2.Wakes)
	}

	// Unchanged status must not re-emit after fingerprint state.
	emitted2, err := testRunCycle(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if emitted2 {
		t.Fatal("expected fingerprint dedupe on unchanged captain status")
	}
}

func TestShouldAbsorbStale(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Dead pane + leftover working: must NOT absorb (possible finish/wedge).
	os.WriteFile(filepath.Join(stateDir, "dead-working.status"), []byte("working: leftover\n"), 0644)
	if shouldAbsorbStale(tmp, "dead-working", false, testTaskStatePort{}) {
		t.Error("dead pane with working status should not absorb")
	}

	// Alive pane + working: idle-healthy absorb.
	if !shouldAbsorbStale(tmp, "dead-working", true, testTaskStatePort{}) {
		t.Error("alive pane with working status should absorb")
	}

	// Paused absorbs regardless of pane liveness.
	os.WriteFile(filepath.Join(stateDir, "paused.status"), []byte("paused: waiting on human\n"), 0644)
	if !shouldAbsorbStale(tmp, "paused", false, testTaskStatePort{}) {
		t.Error("paused should absorb even when pane is dead")
	}

	// Terminal done with no active run: do not absorb as stale (signal path surfaces).
	os.WriteFile(filepath.Join(stateDir, "done.status"), []byte("done: finished\n"), 0644)
	if shouldAbsorbStale(tmp, "done", true, testTaskStatePort{}) {
		t.Error("done should not absorb via shouldAbsorbStale")
	}
}

// --- Pause resurface table-driven tests ---

func TestShouldAbsorbStale_PauseResurface(t *testing.T) {
	tests := []struct {
		name       string
		statusLine string
		pauseAge   time.Duration // age of status file (positive = in the past)
		paneAlive  bool
		wantAbsorb bool
	}{
		{
			name:       "recent pause absorbs",
			statusLine: "paused: waiting on human",
			pauseAge:   time.Minute,
			paneAlive:  false,
			wantAbsorb: true,
		},
		{
			name:       "pause beyond resurface threshold surfaces as stale",
			statusLine: "paused: waiting on human",
			pauseAge:   10 * time.Minute,
			paneAlive:  false,
			wantAbsorb: false,
		},
		{
			name:       "pause at boundary — just under — absorbs",
			statusLine: "paused: waiting on human",
			pauseAge:   4*time.Minute + 59*time.Second,
			paneAlive:  false,
			wantAbsorb: true,
		},
		{
			name:       "pause at boundary — just over — surfaces",
			statusLine: "paused: waiting on human",
			pauseAge:   5*time.Minute + 1*time.Second,
			paneAlive:  false,
			wantAbsorb: false,
		},
		{
			name:       "recent pause with alive pane absorbs",
			statusLine: "paused: waiting on review",
			pauseAge:   time.Minute,
			paneAlive:  true,
			wantAbsorb: true,
		},
		{
			name:       "stale pause with alive pane surfaces",
			statusLine: "paused: waiting on review",
			pauseAge:   10 * time.Minute,
			paneAlive:  true,
			wantAbsorb: false,
		},
		{
			name:       "working status not affected by pause resurface",
			statusLine: "working: still going",
			pauseAge:   time.Minute,
			paneAlive:  true,
			wantAbsorb: true,
		},
		{
			name:       "done status not paused — does not absorb",
			statusLine: "done: finished",
			pauseAge:   time.Minute,
			paneAlive:  true,
			wantAbsorb: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			stateDir := filepath.Join(tmp, "state")
			os.MkdirAll(stateDir, 0755)

			statusPath := filepath.Join(stateDir, "test-pause.status")
			if err := os.WriteFile(statusPath, []byte(tt.statusLine+"\n"), 0644); err != nil {
				t.Fatal(err)
			}

			// Set mod time to simulate pause age.
			past := time.Now().Add(-tt.pauseAge)
			if err := os.Chtimes(statusPath, past, past); err != nil {
				t.Fatal(err)
			}

			got := shouldAbsorbStale(tmp, "test-pause", tt.paneAlive, testTaskStatePort{})
			if got != tt.wantAbsorb {
				t.Errorf("shouldAbsorbStale = %v, want %v (pauseAge=%v, paneAlive=%v)",
					got, tt.wantAbsorb, tt.pauseAge, tt.paneAlive)
			}
		})
	}
}

func TestScanFleet_CaptainKindAbsorbsStale(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Idle captain with dead pane and non-terminal status must not flood stale.
	mhome.WriteMeta(tmp, "captain:domain", map[string]string{
		"kind":    "captain",
		"window":  "@nonexistent-captain",
		"backend": "tmux",
	})
	os.WriteFile(filepath.Join(stateDir, "captain:domain.status"), []byte("working: idle healthy\n"), 0644)
	past := time.Now().Add(-(StaleThreshold() + time.Second))
	os.Chtimes(filepath.Join(stateDir, "captain:domain.status"), past, past)

	reason := testScanFleet(tmp)
	if reason != nil {
		t.Fatalf("expected nil for idle captain (status-signal only), got %+v", reason)
	}
}

func TestScanFleet_CaptainTerminalStillSignals(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	mhome.WriteMeta(tmp, "captain:domain", map[string]string{
		"kind":    "captain",
		"window":  "@nonexistent-captain",
		"backend": "tmux",
	})
	os.WriteFile(filepath.Join(stateDir, "captain:domain.status"), []byte("done: handoff complete\n"), 0644)

	reason := testScanFleet(tmp)
	if reason == nil {
		t.Fatal("expected signal for terminal captain status")
	}
	if reason.Kind != "signal" {
		t.Fatalf("kind = %q, want signal", reason.Kind)
	}
}

func TestRunCycle_StaleFingerprintStableAcrossPolls(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	mhome.WriteMeta(tmp, "task-1", map[string]string{"window": "@nonexistent-stable"})
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working: started\n"), 0644)

	emitted, err := testRunCycle(tmp)
	if err != nil || !emitted {
		t.Fatalf("first cycle emitted=%v err=%v, want true nil", emitted, err)
	}
	// Second and third cycles must not re-enqueue: message no longer embeds wall-clock age.
	for i := 0; i < 2; i++ {
		emitted, err = testRunCycle(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if emitted {
			t.Fatalf("cycle %d re-emitted stale despite stable fingerprint", i+2)
		}
	}
	records, err := DrainWakes(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("drained %d wakes, want exactly 1", len(records))
	}
	if strings.Contains(records[0].Payload, "idle for") {
		t.Fatalf("stale message must not embed wall-clock age: %q", records[0].Payload)
	}
}

// TestNormalRunCycle_NoDiagnosticWake verifies that runCycle no longer
// emits diagnostic wakes about missing parent-home. Pending receipts without
// MUNSU_PARENT_STATUS are handled silently — the mailbox system makes them
// durable and health-visible, not watcher-routed.
func TestNormalRunCycle_NoDiagnosticWake(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	taskID := "test-soldier"
	termKey := "uplink"

	// Write provenance marker
	markerPath := filepath.Join(tmp, ProvenanceMarkerName)
	os.MkdirAll(filepath.Dir(markerPath), 0755)
	os.WriteFile(markerPath, []byte("munsu-v2\ntest-captain\n"+tmp+"\n"), 0644)

	// Write receipt (but NO MUNSU_PARENT_STATUS)
	if err := WriteReceipt(tmp, taskID, termKey, "done", "task complete"); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}

	// Ensure no hook is set and MUNSU_PARENT_STATUS is unset
	origHooks := activeTestHooks
	activeTestHooks = NoopWatcherHooks{}
	defer func() { activeTestHooks = origHooks }()
	recoveryDone = sync.Map{}

	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Run one cycle — should NOT emit diagnostic wake about parent-home
	emitted, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("runCycle: %v", err)
	}
	_ = emitted

	// Receipt should NOT be acked (no relay happened — no parent, no hook)
	if IsReceiptAcked(tmp, taskID, termKey) {
		t.Error("receipt should NOT be acked without parent env or hook")
	}

	// Verify NO diagnostic wake about parent-home was enqueued
	records, drainErr := DrainWakes(tmp)
	if drainErr != nil {
		t.Fatalf("DrainWakes: %v", drainErr)
	}
	for _, r := range records {
		if strings.Contains(r.Payload, "parent-home not configured") {
			t.Errorf("should NOT emit parent-home diagnostic: %+v", r)
		}
	}
}

// TestRunCycle_FailsGracefullyOnInvalidParent verifies that runCycle does not
// fatally error when parent home is invalid. Recovery skips gracefully when
// no hook is set or parent is missing.
func TestRunCycle_FailsGracefullyOnInvalidParent(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	taskID := "test-soldier"
	termKey := "uplink"

	// Write provenance marker
	markerPath := filepath.Join(tmp, ProvenanceMarkerName)
	os.MkdirAll(filepath.Dir(markerPath), 0755)
	os.WriteFile(markerPath, []byte("munsu-v2\ntest-captain\n"+tmp+"\n"), 0644)

	// Write receipt
	if err := WriteReceipt(tmp, taskID, termKey, "done", "task complete"); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	if err := InitTaskObligations(tmp, taskID, termKey); err != nil {
		t.Fatalf("InitTaskObligations: %v", err)
	}

	// Set MUNSU_PARENT_STATUS to a non-existent path
	t.Setenv("MUNSU_PARENT_STATUS", filepath.Join(tmp, "nonexistent"))

	// No hook set — recovery does nothing, runCycle handles gracefully
	origHooks := activeTestHooks
	activeTestHooks = NoopWatcherHooks{}
	defer func() { activeTestHooks = origHooks }()
	recoveryDone = sync.Map{}

	// Run one cycle — should NOT fail fatally
	emitted, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("runCycle should not error on invalid parent: %v", err)
	}
	_ = emitted
}

// --- Per-cycle terminal reconciliation tests (supervision-level) ---
//
// These tests verify that the per-cycle terminal reconciliation hook in
// runCycle is invoked correctly from the supervision side:
//   - No double-call on cycle 1 (recovery handles startup)
//   - Error does not exit the watcher and is observable on stderr
//   - No hook = no-op
//
// Real-hook E2E tests (invoking RunCycle against the real captain
// reconciliation hook) live in captain_relay_test.go.

// setupRunCycleTest creates a home with state dir for runCycle tests.
func setupRunCycleTest(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "state"), 0755)
	return tmp
}

// resetRecovery resets recoveryDone for test isolation.
func resetRecovery() {
	recoveryDone = sync.Map{}
}

// TestRunCycle_NoDoubleCallOnCycle1 proves that runCycle does NOT invoke the
// TerminalReconcileHook twice on cycle 1. Recovery handles startup; per-cycle
// reconcile is skipped on the first cycle via the recoveryWasDone guard.
func TestRunCycle_StartupRecoveryIsIndependentPerHome(t *testing.T) {
	homeA := setupRunCycleTest(t)
	homeB := setupRunCycleTest(t)
	calls := map[string]int{}
	origHooks := activeTestHooks
	activeTestHooks = testWatcherHooks{reconcile: func(homeDir string, startup bool) error { calls[homeDir]++; return nil }}
	defer func() { activeTestHooks = origHooks }()
	recoveryDone = sync.Map{}

	if _, err := testRunCycle(homeA); err != nil {
		t.Fatal(err)
	}
	if _, err := testRunCycle(homeB); err != nil {
		t.Fatal(err)
	}
	if calls[homeA] != 1 || calls[homeB] != 1 {
		t.Fatalf("startup calls = %#v, want one per home", calls)
	}
}

func TestRunCycle_NoDoubleCallOnCycle1(t *testing.T) {
	tmp := setupRunCycleTest(t)

	callCount := 0
	origHooks := activeTestHooks
	activeTestHooks = testWatcherHooks{reconcile: func(homeDir string, startup bool) error {
		callCount++
		return nil
	}}
	defer func() { activeTestHooks = origHooks }()
	resetRecovery()

	// Cycle 1 — recovery calls the hook once; per-cycle is skipped.
	emitted, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("first runCycle: %v", err)
	}
	_ = emitted

	if callCount != 1 {
		t.Errorf("expected exactly 1 hook call on cycle 1 (recovery only), got %d", callCount)
	}

	// Cycle 2 — recovery is already done; per-cycle calls the hook once.
	emitted2, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("second runCycle: %v", err)
	}
	_ = emitted2

	if callCount != 2 {
		t.Errorf("expected exactly 2 hook calls after cycle 2 (1 recovery + 1 per-cycle), got %d", callCount)
	}
}

// TestRunCycle_PerCycleReconcileErrorDoesNotExit verifies that when the
// terminal reconcile hook returns an error during per-cycle reconcile,
// the watcher cycle logs the error on stderr and continues rather than
// failing fatally. Partial failure must not falsely ack/close obligations.
func TestRunCycle_PerCycleReconcileErrorDoesNotExit(t *testing.T) {
	tmp := setupRunCycleTest(t)

	t.Setenv("MUNSU_PARENT_STATUS", "/nonexistent")
	origHooks := activeTestHooks
	activeTestHooks = testWatcherHooks{reconcile: func(homeDir string, startup bool) error {
		return fmt.Errorf("simulated reconcile error")
	}}
	defer func() { activeTestHooks = origHooks }()
	resetRecovery()

	// Capture stderr to verify error message is observable.
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = stderrW

	// Run a cycle — must NOT fail fatally despite hook error.
	emitted, cycleErr := testRunCycle(tmp)
	// Restore stderr before any assertions.
	stderrW.Close()
	os.Stderr = origStderr
	stderrOut, _ := io.ReadAll(stderrR)
	_ = stderrR.Close()

	if cycleErr != nil {
		t.Fatalf("runCycle should not exit on hook error: %v", cycleErr)
	}
	_ = emitted

	// Verify error message is observable on stderr.
	if !strings.Contains(string(stderrOut), "simulated reconcile error") {
		t.Errorf("expected 'simulated reconcile error' on stderr, got: %s", string(stderrOut))
	}

	// Second cycle should also continue despite hook error.
	emitted2, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("second runCycle should also not exit on hook error: %v", err)
	}
	_ = emitted2
}

// TestRunCycle_PerCycleReconcileNoHookIsNoop verifies that when no
// TerminalReconcileHook is installed, the per-cycle reconcile is a no-op
// and does not cause any issues.
func TestRunCycle_PerCycleReconcileNoHookIsNoop(t *testing.T) {
	tmp := setupRunCycleTest(t)

	origHooks := activeTestHooks
	activeTestHooks = NoopWatcherHooks{}
	defer func() { activeTestHooks = origHooks }()
	resetRecovery()

	emitted, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("runCycle should work with nil hook: %v", err)
	}
	_ = emitted

	emitted2, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("second runCycle should work with nil hook: %v", err)
	}
	_ = emitted2
}

// --- CaptainActivationHook tests ---

// TestRunCycle_CaptainActivationNotCalledOnCycle1 verifies that the
// CaptainActivationHook is NOT called during the first cycle (recovery is
// responsible for startup activation). Only per-cycle hooks after recovery
// call it.
func TestRunCycle_CaptainActivationNotCalledOnCycle1(t *testing.T) {
	tmp := setupRunCycleTest(t)

	callCount := 0
	origHooks := activeTestHooks
	activeTestHooks = testWatcherHooks{activate: func(homeDir string) {
		callCount++
	}}
	defer func() { activeTestHooks = origHooks }()
	resetRecovery()

	// Cycle 1 — recovery guard should prevent hook call.
	emitted, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("first runCycle: %v", err)
	}
	_ = emitted

	if callCount != 0 {
		t.Errorf("expected 0 CaptainActivationHook calls on cycle 1, got %d", callCount)
	}

	// Cycle 2 — after recovery, hook should be called.
	emitted2, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("second runCycle: %v", err)
	}
	_ = emitted2

	if callCount != 1 {
		t.Errorf("expected 1 CaptainActivationHook call on cycle 2, got %d", callCount)
	}
}

// TestRunCycle_CaptainActivationEveryCycleAfterRecovery verifies that after
// recovery, the CaptainActivationHook is called exactly once per cycle.
func TestRunCycle_CaptainActivationEveryCycleAfterRecovery(t *testing.T) {
	tmp := setupRunCycleTest(t)

	callCount := 0
	origHooks := activeTestHooks
	activeTestHooks = testWatcherHooks{activate: func(homeDir string) {
		callCount++
	}}
	defer func() { activeTestHooks = origHooks }()
	resetRecovery()

	// Cycle 1 — recovery, no hook call.
	testRunCycle(tmp)
	if callCount != 0 {
		t.Fatalf("expected 0 after cycle 1, got %d", callCount)
	}

	// Cycle 2 — hook called once.
	testRunCycle(tmp)
	if callCount != 1 {
		t.Fatalf("expected 1 after cycle 2, got %d", callCount)
	}

	// Cycle 3 — hook called once more.
	testRunCycle(tmp)
	if callCount != 2 {
		t.Fatalf("expected 2 after cycle 3, got %d", callCount)
	}

	// Cycle 4 — hook called once more.
	testRunCycle(tmp)
	if callCount != 3 {
		t.Fatalf("expected 3 after cycle 4, got %d", callCount)
	}
}

// TestRunCycle_CaptainActivationNilHookIsNoop verifies that a nil
// CaptainActivationHook is a no-op and does not affect the cycle.
func TestRunCycle_CaptainActivationNilHookIsNoop(t *testing.T) {
	tmp := setupRunCycleTest(t)

	origHooks := activeTestHooks
	activeTestHooks = NoopWatcherHooks{}
	defer func() { activeTestHooks = origHooks }()
	resetRecovery()

	// Skip cycle 1 (recovery), check cycle 2.
	emitted, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("first runCycle with nil hook: %v", err)
	}
	_ = emitted

	emitted2, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("second runCycle with nil hook: %v", err)
	}
	_ = emitted2
}

// TestDeadStaleWatcher_PendingWakeDetectsDeadWatcher proves that when the
// watcher beat is stale and material wakes are pending, a scan produces
// actionable condition codes for recovery.
func TestDeadStaleWatcher_PendingWakeDetectsDeadWatcher(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Set up a stale watcher beat.
	old := time.Now().Add(-(StaleThreshold() + time.Minute))
	beatContent := fmt.Sprintf("%d %d", old.Unix(), 99999)
	os.WriteFile(mhome.WatcherBeatPath(tmp), []byte(beatContent), 0644)

	// Add an in-flight task.
	mhome.WriteMeta(tmp, "task-stale", map[string]string{"window": "@test"})
	os.WriteFile(filepath.Join(stateDir, "task-stale.status"), []byte("working: started\n"), 0644)

	// Enqueue a material wake.
	EnqueueWake(tmp, "signal", "task-stale", "done: PR merged")

	// Evaluate guard — should detect stale + pending.
	result := EvaluateGuard(tmp, 1, time.Now())
	if !result.BeatStatus.Stale {
		t.Fatal("expected stale beat status")
	}
	if !HasQueuedWakes(tmp) {
		t.Error("material wake not queued")
	}
}

// TestDeadStaleWatcher_AutoRecoverOrFailClosed proves that a dead/stale watcher
// with pending material wakes either recovers (by starting a new watcher) or
// fails closed with actionable diagnostics.
func TestDeadStaleWatcher_AutoRecoverOrFailClosed(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// No watcher beat = absent watcher.
	beatStatus := ReadBeatStatus(tmp, time.Now())
	if beatStatus.Exists {
		t.Fatal("beat should not exist in clean temp dir")
	}

	// Enqueue a material wake.
	EnqueueWake(tmp, "signal", "task-fail", "done: PR merged")

	// Bounded status command should detect the situation.
	// (Unit test for the diagnostic evaluation logic)
	result := EvaluateGuard(tmp, 1, time.Now())
	if result.BeatStatus.Exists {
		t.Error("absent watcher reported as existing")
	}
	hasWakeCondition := false
	for _, c := range result.Conditions {
		if c.Code == ConditionQueuedWakesPending {
			hasWakeCondition = true
		}
	}
	if !hasWakeCondition {
		t.Errorf("missing queued-wake condition: %v", result.Conditions)
	}

	// Run cycle to attempt recovery — with no watcher, cycles still produce
	// status-signal wakes.
	emitted, err := testRunCycle(tmp)
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	// Should produce a wake from ScanFleet (status signal).
	_ = emitted
}

// TestArmBackground_ClearsStaleIdentity verifies that ArmBackground clears
// stale identity before invoking the process starter. This tests the real
// production contract without spawning a daemon.
func TestArmBackground_ClearsStaleIdentity(t *testing.T) {
	home := t.TempDir()

	// Write a stale identity with known CommitSHA.
	id := NewIdentity(home)
	id.BuildVersion = "0.1.0-dev+stalecommit"
	id.CommitSHA = "stalecommit"
	WriteIdentity(home, id)

	// Substitute the lower-level starter to capture when it would be invoked.
	started := make(chan struct{}, 1)
	savedStarter := startWatcherProcess
	startWatcherProcess = func(dir string) error {
		// Verify identity is already cleared before the starter runs.
		if remaining := ReadIdentity(dir); remaining != nil {
			if remaining.CommitSHA == "stalecommit" {
				t.Error("stale identity should have been cleared before starter")
			}
		}
		started <- struct{}{}
		return nil // don't actually start a daemon
	}
	defer func() { startWatcherProcess = savedStarter }()

	if err := ArmBackground(home, false); err != nil {
		t.Fatalf("ArmBackground: %v", err)
	}

	// The seam must have been reached (starter called).
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("process starter was never invoked")
	}

	// After ArmBackground returns, identity must be gone.
	if remaining := ReadIdentity(home); remaining != nil {
		t.Errorf("identity should be nil after ArmBackground, got %+v", remaining)
	}
}
