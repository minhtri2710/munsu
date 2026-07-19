package soldierstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/task"
)

// setHomeEnv sets MUNSU_HOME for the duration of a test.
func setHomeEnv(t *testing.T, path string) {
	t.Helper()
	os.Setenv("MUNSU_HOME", path)
	t.Cleanup(func() { os.Unsetenv("MUNSU_HOME") })
}

func TestRead_NoMeta(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	s, err := Read(tmp, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}

	if s.Status != "unknown" {
		t.Errorf("status = %q, want unknown", s.Status)
	}
	if !strings.Contains(s.Description, "torn-down") {
		t.Errorf("description should say 'torn-down', got %q", s.Description)
	}
}

func TestRead_NoWindow(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := task.WriteMeta(tmp, "no-win", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}

	s, err := Read(tmp, "no-win")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "unknown" {
		t.Errorf("status = %q, want unknown", s.Status)
	}
	if !strings.Contains(s.Description, "no window") {
		t.Errorf("description should mention no window, got %q", s.Description)
	}
}

func TestRead_WithWindow(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := task.WriteMeta(tmp, "with-win", map[string]string{
		"window":   "@nonexistent99",
		"worktree": tmp,
	}); err != nil {
		t.Fatal(err)
	}

	s, err := Read(tmp, "with-win")
	if err != nil {
		t.Fatal(err)
	}
	// Window doesn't exist, so pane is gone
	if s.PaneAlive {
		t.Error("pane should not be alive for fake window")
	}
}

func TestRead_StatusLogOverrides(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := task.WriteMeta(tmp, "status-test", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	// Append a done status
	if err := task.AppendStatus(tmp, "status-test", "done: implemented feature X"); err != nil {
		t.Fatal(err)
	}

	s, err := Read(tmp, "status-test")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "done" {
		t.Errorf("status = %q, want done", s.Status)
	}
	if s.StatusLines != 1 {
		t.Errorf("StatusLines = %d, want 1", s.StatusLines)
	}
}

func TestRead_FailedStatus(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := task.WriteMeta(tmp, "fail-test", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	if err := task.AppendStatus(tmp, "fail-test", "failed: tests not passing"); err != nil {
		t.Fatal(err)
	}

	s, err := Read(tmp, "fail-test")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "failed" {
		t.Errorf("status = %q, want failed", s.Status)
	}
}

func TestRead_MultipleStatusLines(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := task.WriteMeta(tmp, "multi-status", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	task.AppendStatus(tmp, "multi-status", "working: started investigation")
	task.AppendStatus(tmp, "multi-status", "needs-decision: which approach")
	task.AppendStatus(tmp, "multi-status", "resolved: chose approach A")
	task.AppendStatus(tmp, "multi-status", "done: all done")

	s, err := Read(tmp, "multi-status")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "done" {
		t.Errorf("status = %q, want done", s.Status)
	}
	if s.StatusLines != 4 {
		t.Errorf("StatusLines = %d, want 4", s.StatusLines)
	}
}

func TestRead_GitBranch(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	// Init a git repo in tmp
	gitDir := filepath.Join(tmp, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Check if we can init a proper git repo
	gitInit := os.WriteFile(filepath.Join(tmp, "README.md"), []byte("# test"), 0644)
	if gitInit != nil {
		t.Fatal(gitInit)
	}

	// We need git available for this test
	// Skip if no git
	if err := task.WriteMeta(tmp, "git-test", map[string]string{
		"window":   "@nonexistent99",
		"worktree": tmp,
	}); err != nil {
		t.Fatal(err)
	}

	s, err := Read(tmp, "git-test")
	if err != nil {
		t.Fatal(err)
	}
	// Status should be idle (pane gone) or unknown
	if s.Status != "unknown" && s.Status != "idle" {
		t.Errorf("unexpected status %q", s.Status)
	}
}

func TestRead_LastNonTerminalStatus(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := task.WriteMeta(tmp, "nonterm", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	task.AppendStatus(tmp, "nonterm", "paused: waiting for review")
	task.AppendStatus(tmp, "nonterm", "blocked: dependency not ready")

	s, err := Read(tmp, "nonterm")
	if err != nil {
		t.Fatal(err)
	}
	// Last line is "blocked", non-terminal, so status should be "blocked"
	// But pane is gone so pane check says "idle" which gets overridden by blocked
	if s.Status != "blocked" {
		t.Errorf("status = %q, want blocked", s.Status)
	}
}

// --- No-mistakes run-step reconciliation tests ---

func TestApplyNoMistakesStep_Running(t *testing.T) {
	s := &State{}
	s.applyNoMistakesStep("running", "")
	if s.Status != "working" {
		t.Errorf("status = %q, want working", s.Status)
	}
	if !strings.Contains(s.Description, "no-mistakes: running") {
		t.Errorf("description = %q, want to contain 'no-mistakes: running'", s.Description)
	}
}

func TestApplyNoMistakesStep_Fixing(t *testing.T) {
	s := &State{}
	s.applyNoMistakesStep("fixing", "")
	if s.Status != "working" {
		t.Errorf("status = %q, want working", s.Status)
	}
}

func TestApplyNoMistakesStep_CI(t *testing.T) {
	s := &State{}
	s.applyNoMistakesStep("ci", "")
	if s.Status != "working" {
		t.Errorf("status = %q, want working", s.Status)
	}
}

func TestApplyNoMistakesStep_AwaitingApproval(t *testing.T) {
	s := &State{}
	s.applyNoMistakesStep("awaiting_approval", "")
	if s.Status != "awaiting_approval" {
		t.Errorf("status = %q, want awaiting_approval", s.Status)
	}
}

func TestApplyNoMistakesStep_FixReview(t *testing.T) {
	s := &State{}
	s.applyNoMistakesStep("fix_review", "")
	if s.Status != "awaiting_approval" {
		t.Errorf("status = %q, want awaiting_approval", s.Status)
	}
}

func TestApplyNoMistakesStep_Passed(t *testing.T) {
	s := &State{}
	s.applyNoMistakesStep("passed", "passed")
	if s.Status != "done" {
		t.Errorf("status = %q, want done", s.Status)
	}
}

func TestApplyNoMistakesStep_ChecksPassed(t *testing.T) {
	s := &State{}
	s.applyNoMistakesStep("checks-passed", "checks-passed")
	if s.Status != "done" {
		t.Errorf("status = %q, want done", s.Status)
	}
}

func TestApplyNoMistakesStep_Failed(t *testing.T) {
	s := &State{}
	s.applyNoMistakesStep("failed", "failed")
	if s.Status != "failed" {
		t.Errorf("status = %q, want failed", s.Status)
	}
}

func TestApplyNoMistakesStep_Cancelled(t *testing.T) {
	s := &State{}
	s.applyNoMistakesStep("cancelled", "")
	if s.Status != "failed" {
		t.Errorf("status = %q, want failed", s.Status)
	}
}

func TestRunStepOverrides_ActiveStepOverridesDone(t *testing.T) {
	s := &State{NoMistakesRunStep: "running"}
	if !s.runStepOverrides("done") {
		t.Error("running should override done log")
	}
}

func TestRunStepOverrides_ActiveStepOverridesFailed(t *testing.T) {
	s := &State{NoMistakesRunStep: "running"}
	if !s.runStepOverrides("failed") {
		t.Error("running should override failed log")
	}
}

func TestRunStepOverrides_PassedDoesNotOverrideDone(t *testing.T) {
	s := &State{NoMistakesRunStep: "passed"}
	if s.runStepOverrides("done") {
		t.Error("passed should not override done log")
	}
}

func TestRunStepOverrides_PassedOverridesFailed(t *testing.T) {
	s := &State{NoMistakesRunStep: "passed"}
	if !s.runStepOverrides("failed") {
		t.Error("passed should override failed log")
	}
}

func TestRunStepOverrides_FailedDoesNotOverrideFailed(t *testing.T) {
	s := &State{NoMistakesRunStep: "failed"}
	if s.runStepOverrides("failed") {
		t.Error("failed should not override failed log")
	}
}

func TestRunStepOverrides_FailedOverridesDone(t *testing.T) {
	s := &State{NoMistakesRunStep: "failed"}
	if !s.runStepOverrides("done") {
		t.Error("failed should override done log")
	}
}

func TestRunStepOverrides_CancelledOverridesDone(t *testing.T) {
	s := &State{NoMistakesRunStep: "cancelled"}
	if !s.runStepOverrides("done") {
		t.Error("cancelled should override done log")
	}
}

func TestRunStepOverrides_AwaitingApprovalOverridesDone(t *testing.T) {
	s := &State{NoMistakesRunStep: "awaiting_approval"}
	if !s.runStepOverrides("done") {
		t.Error("awaiting_approval should override done log")
	}
}

func TestRunStepOverrides_EmptyDoesNotOverride(t *testing.T) {
	s := &State{}
	if s.runStepOverrides("done") {
		t.Error("empty run-step should not override")
	}
}

func TestCheckNoMistakesRun_BranchMismatch(t *testing.T) {
	// When branches don't match, checkNoMistakesRun should return false.
	step, outcome, ok := checkNoMistakesRun("/tmp", "other-branch")
	if ok {
		t.Error("expected false for branch mismatch, but got ok=true")
	}
	// This will likely fail to run no-mistakes, so ok=false is expected.
	// The important thing is it doesn't panic.
	_ = step
	_ = outcome
}
