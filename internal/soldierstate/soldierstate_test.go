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
	// No window and no status — status should be "unknown"
	// (no pane-derived "idle")
	if s.Status != "unknown" {
		t.Errorf("status = %q, want unknown", s.Status)
	}
	if s.PaneAlive {
		t.Error("PaneAlive should be false when no window in meta")
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
	// Window exists in meta — PaneAlive is diagnostic true (window present).
	// Status is not derived from pane liveness.
	if !s.PaneAlive {
		t.Error("PaneAlive should be true when window exists in meta")
	}
	// No status file — status remains unknown
	if s.Status != "unknown" {
		t.Errorf("status = %q, want unknown (no status file)", s.Status)
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
	if len(s.OpenActivities) != 0 {
		t.Errorf("OpenActivities = %+v, want empty after done", s.OpenActivities)
	}
}

func TestRead_ResolvedIsNotCurrentState(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)
	if err := task.WriteMeta(tmp, "resolved-only", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}
	task.AppendStatus(tmp, "resolved-only", "working: started")
	task.AppendStatus(tmp, "resolved-only", "resolved: closed key without terminal")

	s, err := Read(tmp, "resolved-only")
	if err != nil {
		t.Fatal(err)
	}
	// Trailing resolved closes the phase; lastStateBearingLine returns ""
	// because resolved is a pure close event. No higher-precedence source exists,
	// so status falls through to unknown.
	if s.Status == "resolved" {
		t.Fatalf("status must not be resolved; got %q (%s)", s.Status, s.Description)
	}
	if s.Status != "unknown" {
		t.Errorf("status = %q, want unknown after pure close", s.Status)
	}
	if len(s.OpenActivities) != 0 {
		t.Errorf("OpenActivities should be closed, got %+v", s.OpenActivities)
	}
}

func TestRead_KeyedOpenActivitiesMultiEvent(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)
	if err := task.WriteMeta(tmp, "keyed-phases", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}
	task.AppendStatus(tmp, "keyed-phases", "working [key=phase7]: Phase 7 started")
	task.AppendStatus(tmp, "keyed-phases", "working [key=phase6]: Phase 6 started")
	task.AppendStatus(tmp, "keyed-phases", "done [key=phase6]: Phase 6 completed")
	task.AppendStatus(tmp, "keyed-phases", "resolved [key=phase7]: Phase 7 done")
	task.AppendStatus(tmp, "keyed-phases", "working [key=phase8]: Phase 8 started")

	s, err := Read(tmp, "keyed-phases")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "working" {
		t.Errorf("status = %q, want working from last state-bearing line", s.Status)
	}
	if !strings.Contains(s.Description, "Phase 8") {
		t.Errorf("description = %q, want Phase 8 note", s.Description)
	}
	if len(s.OpenActivities) != 1 || s.OpenActivities[0].Key != "phase8" {
		t.Fatalf("OpenActivities = %+v, want only phase8", s.OpenActivities)
	}
}

func TestRead_KeyedVerbBeforeColon(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)
	if err := task.WriteMeta(tmp, "keyed-verb", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}
	task.AppendStatus(tmp, "keyed-verb", "done [key=ship]: PR https://example/1")

	s, err := Read(tmp, "keyed-verb")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "done" {
		t.Errorf("status = %q, want done (key must not pollute verb)", s.Status)
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
	// No status file and pane-derived 'idle' is removed. Status should be unknown.
	if s.Status != "unknown" {
		t.Errorf("unexpected status %q, want unknown (pane prose is not truth)", s.Status)
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
	// Last line is "blocked", non-terminal — status resolves to "blocked" at tier 2.
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

func TestRead_BacklogDoneOverridesStaleStatus(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	// Create meta with window
	if err := task.WriteMeta(tmp, "backlog-test", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	// Add a stale "working" status line
	if err := task.AppendStatus(tmp, "backlog-test", "working: stale work"); err != nil {
		t.Fatal(err)
	}

	// Write a backlog file with "done" for this task
	backlogDir := filepath.Join(tmp, "data")
	if err := os.MkdirAll(backlogDir, 0755); err != nil {
		t.Fatal(err)
	}
	backlogContent := `# Backlog

	## 2025-01-01
	- [x] backlog-test: completed task
`
	if err := os.WriteFile(filepath.Join(backlogDir, "backlog.md"), []byte(backlogContent), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := Read(tmp, "backlog-test")
	if err != nil {
		t.Fatal(err)
	}
	// Backlog "done" (tier 1) overrides stale "working" (tier 5)
	if s.Status != "done" {
		t.Errorf("status = %q, want done (backlog overrides stale working)", s.Status)
	}
	if s.BacklogState != "done" {
		t.Errorf("BacklogState = %q, want done", s.BacklogState)
	}
	if !s.StatusLogSuperseded {
		t.Errorf("StatusLogSuperseded should be true when backlog overrides")
	}
}

func TestRead_BacklogBlockedOverridesStaleStatus(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := task.WriteMeta(tmp, "blocked-test", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	// Add a stale "working" status line
	if err := task.AppendStatus(tmp, "blocked-test", "working: active work"); err != nil {
		t.Fatal(err)
	}

	// Write a backlog file with "blocked"
	backlogDir := filepath.Join(tmp, "data")
	if err := os.MkdirAll(backlogDir, 0755); err != nil {
		t.Fatal(err)
	}
	backlogContent := `# Backlog

	## 2025-01-01
	- [!] blocked-test: blocked task
`
	if err := os.WriteFile(filepath.Join(backlogDir, "backlog.md"), []byte(backlogContent), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := Read(tmp, "blocked-test")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "blocked" {
		t.Errorf("status = %q, want blocked (backlog overrides status)", s.Status)
	}
	if s.BacklogState != "blocked" {
		t.Errorf("BacklogState = %q, want blocked", s.BacklogState)
	}
}

func TestRead_BacklogInFlightFallsThrough(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	// Create meta with window
	if err := task.WriteMeta(tmp, "in-flight-test", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	// Add a "blocked" status line
	if err := task.AppendStatus(tmp, "in-flight-test", "blocked: waiting for dep"); err != nil {
		t.Fatal(err)
	}

	// Backlog says in-flight (falls through to tier 2)
	backlogDir := filepath.Join(tmp, "data")
	if err := os.MkdirAll(backlogDir, 0755); err != nil {
		t.Fatal(err)
	}
	backlogContent := `# Backlog

	## 2025-01-01
	- [-] in-flight-test: in progress
`
	if err := os.WriteFile(filepath.Join(backlogDir, "backlog.md"), []byte(backlogContent), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := Read(tmp, "in-flight-test")
	if err != nil {
		t.Fatal(err)
	}
	// Backlog in-flight doesn't override — status from tier 2 (blocked) wins
	if s.Status != "blocked" {
		t.Errorf("status = %q, want blocked (in-flight falls through)", s.Status)
	}
	if s.BacklogState != "in-flight" {
		t.Errorf("BacklogState = %q, want in-flight", s.BacklogState)
	}
}

func TestRead_BacklogQueuedWhenUnknown(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	// Create meta only (no status file, no window)
	if err := task.WriteMeta(tmp, "queued-test", map[string]string{
		"kind": "ship",
	}); err != nil {
		t.Fatal(err)
	}

	// Backlog says queued
	backlogDir := filepath.Join(tmp, "data")
	if err := os.MkdirAll(backlogDir, 0755); err != nil {
		t.Fatal(err)
	}
	backlogContent := `# Backlog

	## 2025-01-01
	- [ ] queued-test: not started
`
	if err := os.WriteFile(filepath.Join(backlogDir, "backlog.md"), []byte(backlogContent), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := Read(tmp, "queued-test")
	if err != nil {
		t.Fatal(err)
	}
	// Queued state appears when status is unknown
	if s.Status != "queued" {
		t.Errorf("status = %q, want queued", s.Status)
	}
	if s.BacklogState != "queued" {
		t.Errorf("BacklogState = %q, want queued", s.BacklogState)
	}
}

func TestRead_NoBacklogItemFallsThrough(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	// Create meta with a "done" status
	if err := task.WriteMeta(tmp, "no-backlog-item", map[string]string{
		"kind": "ship",
	}); err != nil {
		t.Fatal(err)
	}
	if err := task.AppendStatus(tmp, "no-backlog-item", "done: completed without backlog"); err != nil {
		t.Fatal(err)
	}

	// No backlog file at all — should fall through to tier 2
	s, err := Read(tmp, "no-backlog-item")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "done" {
		t.Errorf("status = %q, want done from status file", s.Status)
	}
	if s.BacklogState != "" {
		t.Errorf("BacklogState = %q, want empty (no backlog item)", s.BacklogState)
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
