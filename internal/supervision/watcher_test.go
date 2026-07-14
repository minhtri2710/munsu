package supervision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/crewstate"
	"github.com/minhtri2710/munsu/internal/task"
)

// --- absorbStaleSignal tests (pure predicate) ---

func TestAbsorbStaleSignal_Nil(t *testing.T) {
	if absorbStaleSignal(nil) {
		t.Error("absorbStaleSignal(nil) should return false")
	}
}

func TestAbsorbStaleSignal_EmptyStep(t *testing.T) {
	s := &crewstate.State{}
	if absorbStaleSignal(s) {
		t.Error("absorbStaleSignal with empty step should return false")
	}
}

func TestAbsorbStaleSignal_Running(t *testing.T) {
	s := &crewstate.State{NoMistakesRunStep: "running"}
	if !absorbStaleSignal(s) {
		t.Error("running should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_Fixing(t *testing.T) {
	s := &crewstate.State{NoMistakesRunStep: "fixing"}
	if !absorbStaleSignal(s) {
		t.Error("fixing should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_CI(t *testing.T) {
	s := &crewstate.State{NoMistakesRunStep: "ci"}
	if !absorbStaleSignal(s) {
		t.Error("ci should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_FixReview(t *testing.T) {
	s := &crewstate.State{NoMistakesRunStep: "fix_review"}
	if !absorbStaleSignal(s) {
		t.Error("fix_review should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_AwaitingApproval(t *testing.T) {
	s := &crewstate.State{NoMistakesRunStep: "awaiting_approval"}
	if !absorbStaleSignal(s) {
		t.Error("awaiting_approval should absorb stale signal")
	}
}

func TestAbsorbStaleSignal_Done(t *testing.T) {
	s := &crewstate.State{NoMistakesRunStep: "done"}
	if absorbStaleSignal(s) {
		t.Error("done should NOT absorb stale signal")
	}
}

func TestAbsorbStaleSignal_Failed(t *testing.T) {
	s := &crewstate.State{NoMistakesRunStep: "failed"}
	if absorbStaleSignal(s) {
		t.Error("failed should NOT absorb stale signal")
	}
}

func TestAbsorbStaleSignal_ChecksPassed(t *testing.T) {
	s := &crewstate.State{NoMistakesRunStep: "checks-passed"}
	if absorbStaleSignal(s) {
		t.Error("checks-passed should NOT absorb stale signal")
	}
}

func TestAbsorbStaleSignal_UnknownStep(t *testing.T) {
	s := &crewstate.State{NoMistakesRunStep: "some-bogus-step"}
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
	reason := handleStale("test-task-2", "second")
	if reason.DemandDeepInspection {
		t.Error("second stale poll should not demand deep inspection (threshold=3)")
	}
	if staleStreaks["test-task-2"] != 2 {
		t.Errorf("streak = %d, want 2", staleStreaks["test-task-2"])
	}
}

func TestHandleStale_AtThreshold(t *testing.T) {
	delete(staleStreaks, "test-task-3")
	consecutiveStaleThreshold = 3

	handleStale("test-task-3", "first")
	handleStale("test-task-3", "second")
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
	handleStale("test-task-4", "second")
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
	handleStale("task-a", "second")

	// task-a at 2 -> 3, should trigger
	reasonA := handleStale("task-a", "third")
	if !reasonA.DemandDeepInspection {
		t.Error("task-a third poll should demand deep inspection")
	}

	// task-b at 1, should NOT trigger
	reasonB := handleStale("task-b", "second")
	if reasonB.DemandDeepInspection {
		t.Error("task-b second poll should not demand deep inspection (streak=2, thresh=3)")
	}
}

func TestResetStreak(t *testing.T) {
	delete(staleStreaks, "test-reset")
	consecutiveStaleThreshold = 3

	handleStale("test-reset", "first")
	handleStale("test-reset", "second")
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

// --- scanFleet integration tests ---

func TestScanFleet_NoStateDir(t *testing.T) {
	tmp := t.TempDir()
	reason := scanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for no state dir, got %v", reason)
	}
}

func TestScanFleet_EmptyStateDir(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	reason := scanFleet(tmp)
	if reason != nil {
		t.Errorf("expected nil for empty state dir, got %v", reason)
	}
}

func TestScanFleet_IgnoresDotfiles(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	os.WriteFile(filepath.Join(stateDir, ".hidden.meta"), []byte("window=@test\n"), 0644)

	reason := scanFleet(tmp)
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

	reason := scanFleet(tmp)
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

	reason := scanFleet(tmp)
	if reason == nil {
		t.Fatal("expected a stale reason for dead pane")
	}
	if reason.Kind != "stale" {
		t.Errorf("kind = %q, want stale", reason.Kind)
	}
}
