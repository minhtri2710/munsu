package supervision

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/marker"
	"github.com/minhtri2710/munsu/internal/soldierstate"
	"github.com/minhtri2710/munsu/internal/task"
)

// --- absorbStaleSignal tests (pure predicate) ---

func TestAbsorbStaleSignal_Nil(t *testing.T) {
	if absorbStaleSignal(nil) {
		t.Error("absorbStaleSignal(nil) should return false")
	}
}

func TestAbsorbStaleSignal_EmptyStep(t *testing.T) {
	s := &soldierstate.State{}
	if absorbStaleSignal(s) {
		t.Error("absorbStaleSignal with empty step should return false")
	}
}

func TestAbsorbStaleSignal_Running(t *testing.T) {
	s := &soldierstate.State{NoMistakesRunStep: "running"}
	if !absorbStaleSignal(s) {
		t.Error("running should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_Fixing(t *testing.T) {
	s := &soldierstate.State{NoMistakesRunStep: "fixing"}
	if !absorbStaleSignal(s) {
		t.Error("fixing should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_CI(t *testing.T) {
	s := &soldierstate.State{NoMistakesRunStep: "ci"}
	if !absorbStaleSignal(s) {
		t.Error("ci should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_FixReview(t *testing.T) {
	s := &soldierstate.State{NoMistakesRunStep: "fix_review"}
	if !absorbStaleSignal(s) {
		t.Error("fix_review should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_AwaitingApproval(t *testing.T) {
	s := &soldierstate.State{NoMistakesRunStep: "awaiting_approval"}
	if !absorbStaleSignal(s) {
		t.Error("awaiting_approval should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_Done(t *testing.T) {
	s := &soldierstate.State{NoMistakesRunStep: "done"}
	if absorbStaleSignal(s) {
		t.Error("done should NOT absorb stale signal")
	}
}

func TestAbsorbStaleSignal_Failed(t *testing.T) {
	s := &soldierstate.State{NoMistakesRunStep: "failed"}
	if absorbStaleSignal(s) {
		t.Error("failed should NOT absorb stale signal")
	}
}

func TestAbsorbStaleSignal_ChecksPassed(t *testing.T) {
	s := &soldierstate.State{NoMistakesRunStep: "checks-passed"}
	if absorbStaleSignal(s) {
		t.Error("checks-passed should NOT absorb stale signal")
	}
}

func TestAbsorbStaleSignal_UnknownStep(t *testing.T) {
	s := &soldierstate.State{NoMistakesRunStep: "some-bogus-step"}
	if absorbStaleSignal(s) {
		t.Error("unknown step should NOT absorb stale signal")
	}
}

// --- handleStale / resetStreak tests (pure map operations) ---

func TestHandleStale_FirstPoll(t *testing.T) {
	delete(staleStreaks, "test-task-1")
	consecutiveStaleThreshold = 3

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
		t.Error("first stale poll should not demand deep inspection")
	}
	if staleStreaks["test-task-1"] != 1 {
		t.Errorf("streak = %d, want 1", staleStreaks["test-task-1"])
	}
}

func TestHandleStale_UnderThreshold(t *testing.T) {
	delete(staleStreaks, "test-task-2")
	consecutiveStaleThreshold = 3

	handleStale("test-task-2", "first")
	reason := handleStale("test-task-2", "general")
	if reason.DemandDeepInspection {
		t.Error("captain stale poll should not demand deep inspection (threshold=3)")
	}
	if staleStreaks["test-task-2"] != 2 {
		t.Errorf("streak = %d, want 2", staleStreaks["test-task-2"])
	}
}

func TestHandleStale_AtThreshold(t *testing.T) {
	delete(staleStreaks, "test-task-3")
	consecutiveStaleThreshold = 3

	handleStale("test-task-3", "first")
	handleStale("test-task-3", "general")
	reason := handleStale("test-task-3", "third")
	if !reason.DemandDeepInspection {
		t.Error("third consecutive stale poll should demand deep inspection")
	}
	if !strings.Contains(reason.Message, "demand-deep-inspection") {
		t.Errorf("message should contain demand-deep-inspection, got %q", reason.Message)
	}
}

func TestHandleStale_AboveThreshold(t *testing.T) {
	delete(staleStreaks, "test-task-4")
	consecutiveStaleThreshold = 3

	handleStale("test-task-4", "first")
	handleStale("test-task-4", "general")
	handleStale("test-task-4", "third")
	reason := handleStale("test-task-4", "fourth")
	if !reason.DemandDeepInspection {
		t.Error("fourth consecutive stale poll should demand deep inspection")
	}
}

func TestHandleStale_MultipleTasks(t *testing.T) {
	delete(staleStreaks, "task-a")
	delete(staleStreaks, "task-b")
	consecutiveStaleThreshold = 3

	handleStale("task-a", "first")
	handleStale("task-b", "first")
	handleStale("task-a", "general")

	// task-a at 2 -> 3, should trigger
	reasonA := handleStale("task-a", "third")
	if !reasonA.DemandDeepInspection {
		t.Error("task-a third poll should demand deep inspection")
	}

	// task-b at 1, should NOT trigger
	reasonB := handleStale("task-b", "general")
	if reasonB.DemandDeepInspection {
		t.Error("task-b captain poll should not demand deep inspection (streak=2, thresh=3)")
	}
}

func TestResetStreak(t *testing.T) {
	delete(staleStreaks, "test-reset")
	consecutiveStaleThreshold = 3

	handleStale("test-reset", "first")
	handleStale("test-reset", "general")
	resetStreak("test-reset")

	if _, exists := staleStreaks["test-reset"]; exists {
		t.Error("streak should be deleted after reset")
	}

	// After reset, next stale should be count 1
	reason := handleStale("test-reset", "after-reset")
	if reason.DemandDeepInspection {
		t.Error("after reset, first stale should not demand deep inspection")
	}
	if staleStreaks["test-reset"] != 1 {
		t.Errorf("streak after reset = %d, want 1", staleStreaks["test-reset"])
	}
}

func TestResetStreak_NonExistent(t *testing.T) {
	// Should not panic
	resetStreak("non-existent-task")
}

func TestStaleStreaks_ConcurrentAccess(t *testing.T) {
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
	reason := ScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for no state dir, got %v", reason)
	}
}

func TestScanFleet_EmptyStateDir(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	reason := ScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for empty state dir, got %v", reason)
	}
}

func TestScanFleet_IgnoresDotfiles(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	os.WriteFile(filepath.Join(stateDir, ".hidden.meta"), []byte("window=@test\n"), 0644)

	reason := ScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil when only dotfiles present, got %v", reason)
	}
}

func TestScanFleet_NoWindow(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	if err := task.WriteMeta(tmp, "no-win", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}

	reason := ScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for task with no window, got %v", reason)
	}
}

func TestScanFleet_MultipleTasks_OnlyOneWithWindow(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Task with no window
	task.WriteMeta(tmp, "no-win", map[string]string{"kind": "ship"})
	// Task with a window (but window doesn't exist, so pane is dead)
	task.WriteMeta(tmp, "has-win", map[string]string{"window": "@nonexistent99"})

	reason := ScanFleet(tmp)
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

func TestHandleStale_DemandDeepInspectionStreak(t *testing.T) {
	delete(staleStreaks, "deep-test")
	consecutiveStaleThreshold = 3

	r1 := handleStale("deep-test", "pane is dead")
	if r1.DemandDeepInspection {
		t.Error("poll 1 should not demand deep inspection")
	}

	r2 := handleStale("deep-test", "pane is dead")
	if r2.DemandDeepInspection {
		t.Error("poll 2 should not demand deep inspection")
	}

	r3 := handleStale("deep-test", "pane is dead")
	if !r3.DemandDeepInspection {
		t.Error("poll 3 should demand deep inspection")
	}
	if !strings.Contains(r3.Message, "demand-deep-inspection") {
		t.Errorf("message should contain demand-deep-inspection, got %q", r3.Message)
	}

	r4 := handleStale("deep-test", "pane is dead")
	if !r4.DemandDeepInspection {
		t.Error("poll 4 should still demand deep inspection")
	}
}

func TestAbsorbStaleSignal_AllAbsorbSteps(t *testing.T) {
	absorbSteps := []string{"running", "fixing", "ci", "fix_review", "awaiting_approval"}
	for _, step := range absorbSteps {
		s := &soldierstate.State{NoMistakesRunStep: step}
		if !absorbStaleSignal(s) {
			t.Errorf("%s should absorb stale signal", step)
		}
	}
}

func TestAbsorbStaleSignal_AllNonAbsorbSteps(t *testing.T) {
	nonAbsorbSteps := []string{"", "done", "failed", "checks-passed", "passed", "cancelled", "some-unknown-step"}
	for _, step := range nonAbsorbSteps {
		s := &soldierstate.State{NoMistakesRunStep: step}
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

	task.WriteMeta(tmp, "stale-task", map[string]string{"window": "@nonexistent99"})

	// Write a status file with old modification time
	statusPath := filepath.Join(stateDir, "stale-task.status")
	os.WriteFile(statusPath, []byte("working: started\n"), 0644)

	// Set mod time to be older than stale threshold
	past := time.Now().Add(-(lifecycle.StaleThreshold() + time.Second))
	os.Chtimes(statusPath, past, past)

	reason := ScanFleet(tmp)
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

	task.WriteMeta(tmp, "recent-task", map[string]string{"window": "@nonexistent99"})

	// Write a recent status file
	statusPath := filepath.Join(stateDir, "recent-task.status")
	os.WriteFile(statusPath, []byte("working: active\n"), 0644)

	// The pane is dead but the status is recent — pane staleness takes precedence
	// But the status staleness check won't trigger because modtime is recent
	// The pane liveness check will trigger stale first
	reason := ScanFleet(tmp)
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

	reason := ScanFleet(tmp)
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
	task.WriteMeta(tmp, "no-win", map[string]string{"kind": "ship", "project": "munsu"})

	reason := ScanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for meta without window, got %v", reason)
	}
}

func TestScanFleet_MultipleTasks_MixedStates(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Task 1: has window but pane is dead
	task.WriteMeta(tmp, "dead-task", map[string]string{"window": "@nonexistent1"})
	// Task 2: no window (should be skipped)
	task.WriteMeta(tmp, "no-win-task", map[string]string{"kind": "scout"})
	// Task 3: has dead window and old status file
	task.WriteMeta(tmp, "old-status-task", map[string]string{"window": "@nonexistent2"})

	statusPath := filepath.Join(stateDir, "old-status-task.status")
	os.WriteFile(statusPath, []byte("working: started\n"), 0644)
	past := time.Now().Add(-(lifecycle.StaleThreshold() + time.Second))
	os.Chtimes(statusPath, past, past)

	// scanFleet processes meta files in directory order — should find a stale task
	reason := ScanFleet(tmp)
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

	task.WriteMeta(tmp, "no-window-task", map[string]string{"kind": "ship"})
	if err := lifecycle.EnqueueWake(tmp, "status", "task-1", "done: ready"); err != nil {
		t.Fatal(err)
	}

	emitted, err := runCycle(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if emitted {
		t.Fatal("queued wakes must not cause the watcher to enqueue another wake")
	}

	records, err := lifecycle.DrainWakes(tmp)
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
	task.WriteMeta(tmp, "task-1", map[string]string{"window": "@nonexistent-watch-cycle"})

	emitted, err := runCycle(tmp)
	if err != nil || !emitted {
		t.Fatalf("first cycle emitted=%v err=%v, want true nil", emitted, err)
	}
	emitted, err = runCycle(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if emitted {
		t.Fatal("unchanged condition emitted a duplicate wake")
	}

	if err := task.AppendStatus(tmp, "task-1", "failed: changed condition"); err != nil {
		t.Fatal(err)
	}
	emitted, err = runCycle(tmp)
	if err != nil || !emitted {
		t.Fatalf("changed condition emitted=%v err=%v, want true nil", emitted, err)
	}

	records, err := lifecycle.DrainWakes(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("drained %d wakes, want one initial and one changed-condition wake", len(records))
	}
}

func TestScanFleet_StaleConsistency(t *testing.T) {
	// Test that multiple scanFleet calls produce consistent behavior
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	task.WriteMeta(tmp, "consist-task", map[string]string{"window": "@nonexistentConsist"})

	// First call
	r1 := ScanFleet(tmp)
	if r1 == nil {
		t.Fatal("first call expected stale")
	}
	if r1.Kind != "stale" {
		t.Errorf("first call kind = %q, want stale", r1.Kind)
	}

	// Captain call (streak should increment)
	r2 := ScanFleet(tmp)
	if r2 == nil {
		t.Fatal("captain call expected stale")
	}
	if r2.Kind != "stale" {
		t.Errorf("captain call kind = %q, want stale", r2.Kind)
	}

	// Third call - should trigger demand deep inspection
	r3 := ScanFleet(tmp)
	if r3 == nil {
		t.Fatal("third call expected stale")
	}
	if !r3.DemandDeepInspection {
		t.Error("third consecutive stale should demand deep inspection")
	}
}

func TestAbsorbStaleSignal_ComplexStatusTransitions(t *testing.T) {
	// Simulate a full lifecycle: working → done → no-mistakes run step active
	t.Run("working to done with no-mistakes run", func(t *testing.T) {
		s := &soldierstate.State{
			Status:            "done",
			NoMistakesRunStep: "running",
		}
		// Even though status says done, the active run-step absorbs stale
		if !absorbStaleSignal(s) {
			t.Error("active run on done status should absorb stale")
		}
	})

	t.Run("done with checks-passed does NOT absorb stale", func(t *testing.T) {
		s := &soldierstate.State{
			Status:            "done",
			NoMistakesRunStep: "checks-passed",
		}
		if absorbStaleSignal(s) {
			t.Error("checks-passed should NOT absorb stale")
		}
	})

	t.Run("failed without run step does NOT absorb", func(t *testing.T) {
		s := &soldierstate.State{
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

	emitted, err := RunCycle(home)
	if err != nil {
		t.Fatal(err)
	}
	if !emitted {
		t.Fatal("expected wake from captain-relevant parent status")
	}
	// Second cycle with same status must not re-emit (fingerprint dedupe).
	emitted2, err := RunCycle(home)
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
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.MkdirAll(state, 0755); err != nil {
		t.Fatal(err)
	}

	// 1) Marked send path (General → Captain). Pane inject is backend-specific;
	// the greppable contract is the marker prefix used by munsu send for kind=captain.
	req := "report progress on return-channel-e2e"
	marked := marker.MarkFromGeneral(req)
	if !marker.IsFromGeneral(marked) {
		t.Fatal("expected FromGeneral marker on captain send line")
	}
	if !strings.HasPrefix(marked, marker.FromGeneralLabel) {
		t.Fatalf("marker label missing: %q", marked)
	}

	// 2) Captain answers on parent return channel (no pane required).
	captainID := "captain:munsu"
	statusLine := "done [key=return-channel-e2e]: closed-loop proof landed"
	if err := task.AppendStatus(home, captainID, statusLine); err != nil {
		t.Fatal(err)
	}
	// Optional meta present as in a live captain registry entry; signal path
	// must not require dead pane (return channel wakes while captain is alive).
	if err := task.WriteMeta(home, captainID, map[string]string{
		"kind":    "captain",
		"window":  "captain-pane-alive",
		"backend": "tmux",
	}); err != nil {
		t.Fatal(err)
	}

	// Non-relevant status must stay quiet (working is not general-relevant).
	if err := task.AppendStatus(home, "captain:noise", "working: still grinding"); err != nil {
		t.Fatal(err)
	}

	// 3) Watcher one-shot cycle enqueues captain-relevant signal.
	emitted, err := RunCycle(home)
	if err != nil {
		t.Fatal(err)
	}
	if !emitted {
		t.Fatal("expected signal wake from captain-relevant parent status")
	}

	// 4) General leases the wake via claim (not legacy drain).
	claim, err := lifecycle.ClaimWakes(home, "general-e2e", 60, 10)
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
	claim2, err := lifecycle.ClaimWakes(home, "general-e2e", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claim2.Wakes) != 0 {
		t.Fatalf("expected empty second claim, got %+v", claim2.Wakes)
	}

	// Unchanged status must not re-emit after fingerprint marker.
	emitted2, err := RunCycle(home)
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
	if shouldAbsorbStale(tmp, "dead-working", false) {
		t.Error("dead pane with working status should not absorb")
	}

	// Alive pane + working: idle-healthy absorb.
	if !shouldAbsorbStale(tmp, "dead-working", true) {
		t.Error("alive pane with working status should absorb")
	}

	// Paused absorbs regardless of pane liveness.
	os.WriteFile(filepath.Join(stateDir, "paused.status"), []byte("paused: waiting on human\n"), 0644)
	if !shouldAbsorbStale(tmp, "paused", false) {
		t.Error("paused should absorb even when pane is dead")
	}

	// Terminal done with no active run: do not absorb as stale (signal path surfaces).
	os.WriteFile(filepath.Join(stateDir, "done.status"), []byte("done: finished\n"), 0644)
	if shouldAbsorbStale(tmp, "done", true) {
		t.Error("done should not absorb via shouldAbsorbStale")
	}
}

func TestScanFleet_CaptainKindAbsorbsStale(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Idle captain with dead pane and non-terminal status must not flood stale.
	task.WriteMeta(tmp, "captain:domain", map[string]string{
		"kind":    "captain",
		"window":  "@nonexistent-captain",
		"backend": "tmux",
	})
	os.WriteFile(filepath.Join(stateDir, "captain:domain.status"), []byte("working: idle healthy\n"), 0644)
	past := time.Now().Add(-(lifecycle.StaleThreshold() + time.Second))
	os.Chtimes(filepath.Join(stateDir, "captain:domain.status"), past, past)

	reason := ScanFleet(tmp)
	if reason != nil {
		t.Fatalf("expected nil for idle captain (status-signal only), got %+v", reason)
	}
}

func TestScanFleet_CaptainTerminalStillSignals(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	task.WriteMeta(tmp, "captain:domain", map[string]string{
		"kind":    "captain",
		"window":  "@nonexistent-captain",
		"backend": "tmux",
	})
	os.WriteFile(filepath.Join(stateDir, "captain:domain.status"), []byte("done: handoff complete\n"), 0644)

	reason := ScanFleet(tmp)
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
	task.WriteMeta(tmp, "task-1", map[string]string{"window": "@nonexistent-stable"})
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working: started\n"), 0644)

	emitted, err := runCycle(tmp)
	if err != nil || !emitted {
		t.Fatalf("first cycle emitted=%v err=%v, want true nil", emitted, err)
	}
	// Second and third cycles must not re-enqueue: message no longer embeds wall-clock age.
	for i := 0; i < 2; i++ {
		emitted, err = runCycle(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if emitted {
			t.Fatalf("cycle %d re-emitted stale despite stable fingerprint", i+2)
		}
	}
	records, err := lifecycle.DrainWakes(tmp)
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
