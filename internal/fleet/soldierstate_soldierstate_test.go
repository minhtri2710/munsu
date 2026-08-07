//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// setHomeEnv sets MUNSU_HOME for the duration of a test.
func setHomeEnv(t *testing.T, path string) {
	t.Helper()
	os.Setenv("MUNSU_HOME", path)
	t.Cleanup(func() { os.Unsetenv("MUNSU_HOME") })
}

// seedCanonicalPhase creates one canonical task at the given phase through
// the concrete Task Authority (ADR-0008), never legacy backlog state.
func seedCanonicalPhase(t *testing.T, homeDir, taskID string, phase taskauthority.Phase) {
	t.Helper()
	auth := canonicalAtHome(t, homeDir)
	tid := mustTaskID(t, taskID)
	createReq := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: tid, Owner: "general", Description: "work", Kind: "ship", Reason: "test",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-create-"+taskID), createReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, createReq); err != nil {
		t.Fatal(err)
	}
	switch phase {
	case taskauthority.PhaseBlocked:
		blockReq := taskauthority.CanonicalBlockRequest{
			HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), Detail: "dep", Reason: "test",
		}
		op, err := domain.NewOperation(mustOpID(t, "op-block-"+taskID), blockReq)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Block(op, blockReq); err != nil {
			t.Fatal(err)
		}
	case taskauthority.PhaseWorking:
		startReq := taskauthority.CanonicalStartRequest{
			HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), Reason: "test",
		}
		op, err := domain.NewOperation(mustOpID(t, "op-start-"+taskID), startReq)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Start(op, startReq); err != nil {
			t.Fatal(err)
		}
	case taskauthority.PhaseDone:
		completeReq := taskauthority.CanonicalCompleteRequest{
			HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), To: taskauthority.PhaseDone, Reason: "test",
		}
		op, err := domain.NewOperation(mustOpID(t, "op-done-"+taskID), completeReq)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Complete(op, completeReq); err != nil {
			t.Fatal(err)
		}
	default:
		// queued stays as created
	}
}

// aliveProbe is a test endpoint probe that always reports the pane alive.
type aliveProbe struct{}

func (aliveProbe) Probe(string, map[string]string) (bool, error) { return true, nil }

func TestRead_NoMeta(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	s, err := ReadSoldierState(tmp, "nonexistent")
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

	if err := home.WriteMeta(tmp, "no-win", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSoldierState(tmp, "no-win")
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

	if err := home.WriteMeta(tmp, "with-win", map[string]string{
		"window":   "@nonexistent99",
		"worktree": tmp,
	}); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSoldierState(tmp, "with-win")
	if err != nil {
		t.Fatal(err)
	}
	// Nonexistent window ID probed on backend → PaneAlive should be false.
	if s.PaneAlive {
		t.Error("PaneAlive should be false when window is nonexistent on backend")
	}
	// No status file — status remains unknown
	if s.Status != "unknown" {
		t.Errorf("status = %q, want unknown (no status file)", s.Status)
	}
}

func TestRead_HerdrPaneNotFound_ReturnsPaneAliveFalse(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := home.WriteMeta(tmp, "incident-soldier", map[string]string{
		"backend":       "herdr",
		"herdr_session": "default",
		"window":        "default:w6E:p3",
		"worktree":      tmp,
	}); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(tmp, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`echo '{"error":{"code":"pane_not_found","message":"pane w6E:p3 not found"}}'` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp+":"+os.Getenv("PATH"))

	s, err := ReadSoldierState(tmp, "incident-soldier")
	if err != nil {
		t.Fatal(err)
	}
	if s.PaneAlive {
		t.Errorf("s.PaneAlive = true, want false when Herdr returns pane_not_found")
	}
}

func TestRead_StatusLogOverrides(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := home.WriteMeta(tmp, "status-test", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	// Append a done status
	if err := home.AppendStatus(tmp, "status-test", "done: implemented feature X"); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSoldierState(tmp, "status-test")
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

	if err := home.WriteMeta(tmp, "fail-test", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	if err := home.AppendStatus(tmp, "fail-test", "failed: tests not passing"); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSoldierState(tmp, "fail-test")
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

	if err := home.WriteMeta(tmp, "multi-status", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	home.AppendStatus(tmp, "multi-status", "working: started investigation")
	home.AppendStatus(tmp, "multi-status", "needs-decision: which approach")
	home.AppendStatus(tmp, "multi-status", "resolved: chose approach A")
	home.AppendStatus(tmp, "multi-status", "done: all done")

	s, err := ReadSoldierState(tmp, "multi-status")
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
	if err := home.WriteMeta(tmp, "resolved-only", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}
	home.AppendStatus(tmp, "resolved-only", "working: started")
	home.AppendStatus(tmp, "resolved-only", "resolved: closed key without terminal")

	s, err := ReadSoldierState(tmp, "resolved-only")
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
	if err := home.WriteMeta(tmp, "keyed-phases", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}
	home.AppendStatus(tmp, "keyed-phases", "working [key=phase7]: Phase 7 started")
	home.AppendStatus(tmp, "keyed-phases", "working [key=phase6]: Phase 6 started")
	home.AppendStatus(tmp, "keyed-phases", "done [key=phase6]: Phase 6 completed")
	home.AppendStatus(tmp, "keyed-phases", "resolved [key=phase7]: Phase 7 done")
	home.AppendStatus(tmp, "keyed-phases", "working [key=phase8]: Phase 8 started")

	s, err := ReadSoldierState(tmp, "keyed-phases")
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
	if err := home.WriteMeta(tmp, "keyed-verb", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}
	home.AppendStatus(tmp, "keyed-verb", "done [key=ship]: PR https://example/1")

	s, err := ReadSoldierState(tmp, "keyed-verb")
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
	if err := home.WriteMeta(tmp, "git-test", map[string]string{
		"window":   "@nonexistent99",
		"worktree": tmp,
	}); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSoldierState(tmp, "git-test")
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

	if err := home.WriteMeta(tmp, "nonterm", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	home.AppendStatus(tmp, "nonterm", "paused: waiting for review")
	home.AppendStatus(tmp, "nonterm", "blocked: dependency not ready")

	s, err := ReadSoldierState(tmp, "nonterm")
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

// TestRead_CanonicalDoneOverridesStaleStatus proves a canonical done phase
// (tier 1) overrides a stale "working" status line (Task 7.8): the status
// log is superseded display, never state truth.
func TestRead_CanonicalDoneOverridesStaleStatus(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)
	seedCanonicalPhase(t, tmp, "canonical-done", taskauthority.PhaseDone)

	// Create meta with window
	if err := home.WriteMeta(tmp, "canonical-done", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	// Add a stale "working" status line
	if err := home.AppendStatus(tmp, "canonical-done", "working: stale work"); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSoldierState(tmp, "canonical-done")
	if err != nil {
		t.Fatal(err)
	}
	// Canonical "done" overrides stale "working"
	if s.Status != "done" {
		t.Errorf("status = %q, want done (canonical overrides stale working)", s.Status)
	}
	if !s.StatusLogSuperseded {
		t.Errorf("StatusLogSuperseded should be true when canonical overrides")
	}
}

// TestRead_CanonicalBlockedOverridesStaleStatus proves a canonical blocked
// phase overrides a stale status line.
func TestRead_CanonicalBlockedOverridesStaleStatus(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)
	seedCanonicalPhase(t, tmp, "canonical-blocked", taskauthority.PhaseBlocked)

	if err := home.WriteMeta(tmp, "canonical-blocked", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	// Add a stale "working" status line
	if err := home.AppendStatus(tmp, "canonical-blocked", "working: active work"); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSoldierState(tmp, "canonical-blocked")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "blocked" {
		t.Errorf("status = %q, want blocked (canonical overrides status)", s.Status)
	}
}

// TestRead_CanonicalWorkingWinsWithAlivePane proves the canonical working
// phase is state truth when the pane is verifiably alive.
func TestRead_CanonicalWorkingWinsWithAlivePane(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)
	seedCanonicalPhase(t, tmp, "canonical-working", taskauthority.PhaseWorking)

	// Create meta with window
	if err := home.WriteMeta(tmp, "canonical-working", map[string]string{
		"window": "@nonexistent99",
	}); err != nil {
		t.Fatal(err)
	}

	// Add a stale "blocked" status line
	if err := home.AppendStatus(tmp, "canonical-working", "blocked: waiting for dep"); err != nil {
		t.Fatal(err)
	}

	s, err := ReadWithProbe(tmp, "canonical-working", aliveProbe{})
	if err != nil {
		t.Fatal(err)
	}
	// Canonical working wins over the stale blocked status line.
	if s.Status != "working" {
		t.Errorf("status = %q, want working (canonical phase is state truth)", s.Status)
	}
	if !s.StatusLogSuperseded {
		t.Errorf("StatusLogSuperseded should be true when canonical overrides")
	}
}

// TestRead_CanonicalQueuedWhenUnknown proves a canonical queued phase is
// surfaced when no higher work state exists.
func TestRead_CanonicalQueuedWhenUnknown(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)
	seedCanonicalPhase(t, tmp, "canonical-queued", taskauthority.PhaseQueued)

	// Create meta only (no status file, no window)
	if err := home.WriteMeta(tmp, "canonical-queued", map[string]string{
		"kind": "ship",
	}); err != nil {
		t.Fatal(err)
	}

	s, err := ReadSoldierState(tmp, "canonical-queued")
	if err != nil {
		t.Fatal(err)
	}
	// Queued phase appears as current state.
	if s.Status != "queued" {
		t.Errorf("status = %q, want queued", s.Status)
	}
}

func TestRead_NoCanonicalRecordFallsThrough(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	setHomeEnv(t, tmp)

	// Create meta with a "done" status
	if err := home.WriteMeta(tmp, "no-canonical-record", map[string]string{
		"kind": "ship",
	}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(tmp, "no-canonical-record", "done: completed without canonical record"); err != nil {
		t.Fatal(err)
	}

	// No canonical record — falls through to the status file tier.
	s, err := ReadSoldierState(tmp, "no-canonical-record")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "done" {
		t.Errorf("status = %q, want done from status file", s.Status)
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
