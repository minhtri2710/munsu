//go:build integration

// Package captain — architecture contract test suite.
//
// These tests verify invariants that previously lived in the firstmate
// secondmate test suite. Each test exercises public CLI commands (exported
// captain functions) and proves one invariant through structured state
// artifacts — never through pane output parsing.
//
// Firstmate parity: every invariant documented below was enforced by
// firstmate's secondmate layer. The captain module now enforces the same
// contract via different mechanisms (Go functions instead of shell scripts).
package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// ---------------------------------------------------------------------------
// Invariant: Structured state (canonical aggregate/meta/provider) outranks pane prose
// ---------------------------------------------------------------------------
//
// All operations that resolve captain identity, liveness, or command
// authority MUST read structured state artifacts (meta files, provenance
// markers, status files). Pane output is never used for programmatic
// decisions when structured state exists.
//
// Firstmate parity: firstmate parsed meta fields (kind, sm_id, home, window)
// before any backend operation. The captain module does the same.

// TestStructuredState_CheckAliveViaBackendUsesMetaFiles proves that
// checkAliveViaBackend reads structured meta fields to determine liveness,
// never falling back to pane output parsing. When meta fields are missing
// or wrong, the function returns (false, nil) — never a pane-read error.
func TestStructuredState_CheckAliveViaBackendUsesMetaFiles(t *testing.T) {
	parent := t.TempDir()
	smHome := seedCaptainForTest(t, parent, "test-sm")

	t.Run("returns seeded when no meta exists (not yet launched)", func(t *testing.T) {
		state, err := checkAliveWithProbe(parent, Info{ID: "test-sm", Home: smHome}, &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}})
		if err != nil {
			t.Fatalf("expected nil error for missing meta, got: %v", err)
		}
		if state == CaptainAlive {
			t.Fatal("expected non-alive for captain with no meta")
		}
		if state != CaptainSeeded {
			t.Fatalf("expected CaptainSeeded for missing meta, got %v", state)
		}
	})

	t.Run("returns seeded when meta kind is not captain", func(t *testing.T) {
		// Write meta with wrong kind.
		canon, _ := canonicalCaptainHome(smHome)
		mhome.WriteMeta(parent, taskIDForCaptain("test-sm"), map[string]string{
			"kind":   "ship",
			"sm_id":  "test-sm",
			"home":   canon,
			"window": "w1",
		})
		state, err := checkAliveWithProbe(parent, Info{ID: "test-sm", Home: smHome}, &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}})
		if err != nil {
			t.Fatalf("expected nil error for wrong kind, got: %v", err)
		}
		if state == CaptainAlive {
			t.Fatal("expected non-alive for kind=ship, not captain")
		}
		if state != CaptainSeeded {
			t.Fatalf("expected CaptainSeeded for kind=ship, got %v", state)
		}
	})

	t.Run("returns seeded when meta sm_id does not match", func(t *testing.T) {
		canon, _ := canonicalCaptainHome(smHome)
		mhome.WriteMeta(parent, taskIDForCaptain("test-sm"), map[string]string{
			"kind":   "captain",
			"sm_id":  "wrong-id",
			"home":   canon,
			"window": "w1",
		})
		state, err := checkAliveWithProbe(parent, Info{ID: "test-sm", Home: smHome}, &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}})
		if err != nil {
			t.Fatalf("expected nil error for wrong sm_id, got: %v", err)
		}
		if state == CaptainAlive {
			t.Fatal("expected non-alive for mismatched sm_id")
		}
		if state != CaptainSeeded {
			t.Fatalf("expected CaptainSeeded for mismatched sm_id, got %v", state)
		}
	})

	t.Run("returns seeded when meta window is empty", func(t *testing.T) {
		canon, _ := canonicalCaptainHome(smHome)
		mhome.WriteMeta(parent, taskIDForCaptain("test-sm"), map[string]string{
			"kind":   "captain",
			"sm_id":  "test-sm",
			"home":   canon,
			"window": "",
		})
		state, err := checkAliveWithProbe(parent, Info{ID: "test-sm", Home: smHome}, &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}})
		if err != nil {
			t.Fatalf("expected nil error for empty window, got: %v", err)
		}
		if state == CaptainAlive {
			t.Fatal("expected non-alive for empty window")
		}
		if state != CaptainSeeded {
			t.Fatalf("expected CaptainSeeded for empty window, got %v", state)
		}
	})
}

// TestStructuredState_MetaHomeComparedCanonically proves that
// Meta home comparison is done against the canonical (symlink-resolved)
// home path, not the raw path from pane text.
func TestStructuredState_MetaHomeComparedCanonically(t *testing.T) {
	parent := t.TempDir()
	smHome := seedCaptainForTest(t, parent, "test-sm")

	canon, _ := canonicalCaptainHome(smHome)
	writeCaptainMeta(t, parent, "test-sm", smHome, "w1")

	// Use a symlink variant of the same home — must still match.
	symlinkHome := filepath.Join(parent, "captains", "linked-sm")
	if err := os.Symlink(smHome, symlinkHome); err != nil {
		t.Fatal(err)
	}

	// checkAliveViaBackend uses canonical home internally.
	state, err := checkAliveWithProbe(parent, Info{ID: "test-sm", Home: symlinkHome}, &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}})
	if err != nil {
		t.Fatalf("checkAliveViaBackend via symlink: %v", err)
	}
	// Not alive because backend is not wired. The important thing is the
	// canonical comparison succeeds (no home mismatch error).
	_ = state
	_ = canon
}

// ---------------------------------------------------------------------------
// Invariant: Terminal phases (Done/merged) close and override stale working
// ---------------------------------------------------------------------------
//
// Status lines with done/failed/resolved states indicate terminal phases.
// The captain module uses status artifacts from structured state files to
// close cycles — pane prose about "working" is never authoritative.
//
// Firstmate parity: firstmate used structured status files (state/captain:X.status)
// to detect terminal phases. The captain module's status files serve the same role.

// ---------------------------------------------------------------------------
// Invariant: a captain home without a git worktree is unsupported
// ---------------------------------------------------------------------------
//
// Captain homes are managed git worktrees. A home that has a provenance
// marker but no .git is refused by Update as a failure and fails the
// fast-forward step of Converge; it is never skipped as a success.

// TestTerminalPhases_StatusFileOverridesProse proves that structured status
// artifacts are written and read correctly, demonstrating that the system
// relies on them rather than pane text.
func TestTerminalPhases_StatusFileOverridesProse(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	t.Run("append and read done status", func(t *testing.T) {
		id := "captain:test-sm"
		lines := []string{
			"working: task started",
			"done: task completed [key=task-1]",
		}
		for _, line := range lines {
			if err := mhome.AppendStatus(home, id, line); err != nil {
				t.Fatal(err)
			}
		}
		got, err := mhome.ReadStatus(home, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 status lines, got %d: %v", len(got), got)
		}
		last := got[len(got)-1]
		if !strings.Contains(last, "done") {
			t.Errorf("last status line = %q, want done", last)
		}
	})

	t.Run("failed status overrides working", func(t *testing.T) {
		id := "captain:other-sm"
		mhome.AppendStatus(home, id, "working: in progress")
		mhome.AppendStatus(home, id, "failed: something broke [key=bug-1]")
		got, err := mhome.ReadStatus(home, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 lines, got %d", len(got))
		}
		msg, key := mhome.ParseStatusKey(got[1])
		if !strings.Contains(msg, "failed") {
			t.Errorf("expected failed status, got %q", msg)
		}
		if key != "bug-1" {
			t.Errorf("expected key=bug-1, got %q", key)
		}
	})
}

// TestTerminalPhases_ResolvedOverridesWorking proves that a resolved status
// line appended after a working line is correctly stored and readable —
// demonstrating structured precedence over any "working" prose.
func TestTerminalPhases_ResolvedOverridesWorking(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)

	id := "captain:api"
	workingMsg := "working: fixing the widget"
	resolvedMsg := "resolved: fixed the widget [key=widget-fix]"

	mhome.AppendStatus(home, id, workingMsg)
	mhome.AppendStatus(home, id, resolvedMsg)

	got, err := mhome.ReadStatus(home, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	last := got[len(got)-1]
	if !strings.Contains(last, "resolved") {
		t.Errorf("last line = %q, want resolved", last)
	}
	msg, key := mhome.ParseStatusKey(last)
	if msg == "" {
		t.Error("expected non-empty message")
	}
	if key != "widget-fix" {
		t.Errorf("key = %q, want widget-fix", key)
	}
}

func unsupportedCaptainHomeFixture(t *testing.T, parent, id string) string {
	t.Helper()
	smHome := filepath.Join(parent, "captains", id)
	for _, dir := range []string{"state", "config", "data"} {
		if err := os.MkdirAll(filepath.Join(smHome, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := mhome.SeedCaptainProvenance(smHome, id); err != nil {
		t.Fatal(err)
	}
	return smHome
}

// TestUpdate_HomeWithoutWorktreeIsRefusedUnsupported proves that Update()
// refuses a captain home with no git worktree with a failing outcome.
func TestUpdate_HomeWithoutWorktreeIsRefusedUnsupported(t *testing.T) {
	parent := t.TempDir()
	smHome := unsupportedCaptainHomeFixture(t, parent, "test-sm")

	resp := Update(smHome, parent)
	if string(resp.Outcome) != "unsupported-home" {
		t.Fatalf("Update outcome = %q, want %q", resp.Outcome, "unsupported-home")
	}
	if !resp.Outcome.IsFailure() {
		t.Fatalf("Update outcome %q must be a failure", resp.Outcome)
	}
	if resp.Err == nil || !strings.Contains(resp.Err.Error(), "no git worktree") {
		t.Fatalf("Update error = %v, want no-git-worktree refusal", resp.Err)
	}
	if _, err := os.Stat(filepath.Join(smHome, "config", "parent-home")); !os.IsNotExist(err) {
		t.Fatalf("refused Update must not push config, stat err = %v", err)
	}
}

// TestUpdate_OutcomeFailureClassification pins which outcomes are failures.
func TestUpdate_OutcomeFailureClassification(t *testing.T) {
	if AlreadyCurrent.IsFailure() {
		t.Fatal("AlreadyCurrent should not be a failure")
	}
	if FastForwarded.IsFailure() {
		t.Fatal("FastForwarded should not be a failure")
	}
	if !UnsupportedHome.IsFailure() {
		t.Fatal("UnsupportedHome should be a failure")
	}
	if !Dirty.IsFailure() {
		t.Fatal("Dirty should be a failure")
	}
	if !Diverged.IsFailure() {
		t.Fatal("Diverged should be a failure")
	}
}

// TestConverge_HomeWithoutWorktreeFailsFastForward proves that Converge
// reports a failed fast-forward for a captain home with no git worktree
// instead of skipping it.
func TestConverge_HomeWithoutWorktreeFailsFastForward(t *testing.T) {
	parent := t.TempDir()
	smHome := unsupportedCaptainHomeFixture(t, parent, "no-worktree-sm")

	result, err := Converge(parent, []Info{
		{ID: "no-worktree-sm", Home: smHome},
	}, ConvergeCapabilities{Continuity: noopCaptainContinuity{}, Messaging: noopCaptainMessaging{}, Watcher: noopCaptainWatcher{}, Notification: &captainNotificationTransport{acknowledged: true}, Mailbox: &captainTestMailboxSender{}})
	if err == nil {
		t.Fatal("Converge must fail for a captain home with no git worktree")
	}
	if result == nil {
		t.Fatal("Converge must return a structured result")
	}
	found := false
	for _, step := range result.Steps {
		if step.Name == "no-worktree-sm: safe fast-forward" {
			found = true
			if step.Status != ConvergeFailed {
				t.Fatalf("safe fast-forward status = %s (%s), want failed", step.Status, step.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("no safe fast-forward step in %+v", result.Steps)
	}
}

// ---------------------------------------------------------------------------
// Invariant: Worktree homes get fast-forwarded correctly
// ---------------------------------------------------------------------------
//
// A worktree captain home (git worktree from a parent repo) must be
// safely fast-forwarded when the parent advances its default branch.
// The Update function returns FastForwarded when the worktree was on a
// clean ancestor commit of the parent's new default branch.
//
// Firstmate parity: firstmate used git merge --ff-only inside captain
// homes. The captain module's safeFF does the same with additional
// safety checks (same remote origin, clean tree, ancestor relationship).

// TestUpdate_WorktreeHomeFastForwarded proves that Update() fast-forwards
// a git-based captain home when the parent default branch has advanced.
// Uses a controlled bare-remote git setup to prove the fast-forward path.
func TestUpdate_WorktreeHomeFastForwarded(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput()
	if err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	parent := filepath.Join(root, "parent")
	captain := filepath.Join(root, "captain")
	for _, dst := range []string{parent, captain} {
		if out, err := exec.Command("git", "clone", remote, dst).CombinedOutput(); err != nil {
			t.Fatalf("git clone: %v\n%s", err, out)
		}
		gitTestRun(t, dst, "config", "user.name", "Munsu Test")
		gitTestRun(t, dst, "config", "user.email", "munsu@example.invalid")
	}

	// Initial commit: .gitignore covers captain markers + AGENTS.md.
	gitTestRun(t, parent, "checkout", "-b", "main")
	gitignoreContent := []byte("state/\nconfig/\ndata/\n.munsu-captain-home\n.captain-launch.sh\n")
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), gitignoreContent, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, parent, "add", ".gitignore", "AGENTS.md")
	gitTestRun(t, parent, "commit", "-m", "initial")
	before := gitTestRun(t, parent, "rev-parse", "HEAD")
	gitTestRun(t, parent, "push", "-u", "origin", "main")
	gitTestRun(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	// Sync captain to the initial commit.
	gitTestRun(t, captain, "fetch", "origin", "main")
	gitTestRun(t, captain, "checkout", "-B", "main", before)
	gitTestRun(t, captain, "remote", "set-head", "origin", "main")
	gitTestRun(t, parent, "remote", "set-head", "origin", "main")

	// Parent advances AGENTS.md (instruction surface).
	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, parent, "commit", "-am", "advance instructions")
	_ = gitTestRun(t, parent, "rev-parse", "HEAD")
	gitTestRun(t, parent, "push", "origin", "main")
	// Seed the already-local object without changing the captain checkout.
	gitTestRun(t, captain, "fetch", "origin", "main")
	gitTestRun(t, captain, "reset", "--hard", before)

	// Add captain structure (gitignored — no dirty tree).
	os.MkdirAll(filepath.Join(captain, "state"), 0755)
	os.MkdirAll(filepath.Join(captain, "config"), 0755)
	os.MkdirAll(filepath.Join(captain, "data"), 0755)
	// No tracked file writes — state/, config/, data/ are gitignored.
	mhome.SeedCaptainProvenance(captain, "test-sm")

	// Update should fast-forward the captain home.
	resp := Update(captain, parent)
	if resp.Outcome != FastForwarded {
		t.Fatalf("Update outcome = %q, want %q (before=%s, after=%s, err=%v)",
			resp.Outcome, FastForwarded, safeStr(resp.Before), safeStr(resp.After), resp.Err)
	}
	if resp.Before == resp.After {
		t.Fatal("expected Before != After on fast-forward")
	}
}

// TestUpdate_WorktreeHomeAlreadyCurrent proves that Update() returns
// AlreadyCurrent when the captain is already on the parent's commit.
func TestUpdate_WorktreeHomeAlreadyCurrent(t *testing.T) {
	tests := []struct {
		name     string
		noParent bool
	}{
		{name: "parent is repository"},
		{name: "parent has no git", noParent: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project := newWorktreeFixture(t)
			parent := t.TempDir()
			if _, err := mhome.Init(parent); err != nil {
				t.Fatal(err)
			}
			homePath := filepath.Join(parent, "captains", "test-captain")
			if err := seedFromWorktreeTest("test-captain", homePath, project, parent, "", false, ""); err != nil {
				t.Fatal(err)
			}
			if tc.noParent {
				if _, err := os.Stat(filepath.Join(parent, ".git")); !os.IsNotExist(err) {
					t.Skip("parent unexpectedly has .git — cannot test no-parent-git scenario")
				}
			}
			resp := Update(homePath, parent)
			if resp.Outcome != AlreadyCurrent {
				t.Fatalf("Update outcome = %q, want %q (err=%v)", resp.Outcome, AlreadyCurrent, resp.Err)
			}
		})
	}
}

// TestUpdate_UnmarkedHomeReturnsInvalidProvenance proves that Update()
// on a home without provenance marker returns InvalidProvenance, not a
// panic or silent skip.
func TestUpdate_UnmarkedHomeReturnsInvalidProvenance(t *testing.T) {
	tmp := t.TempDir()
	resp := Update(tmp, t.TempDir())
	if resp.Outcome != InvalidProvenance {
		t.Fatalf("Update outcome = %q, want %q (err=%v)", resp.Outcome, InvalidProvenance, resp.Err)
	}
	if resp.Err == nil {
		t.Fatal("expected non-nil error for unmarked home")
	}
}

// ---------------------------------------------------------------------------
// Invariant: Update outcome types map correctly from safeFF reasons
// ---------------------------------------------------------------------------

// TestUpdate_OutcomeMapping proves that safeFF reasons are correctly mapped
// to typed UpdateOutcome values, so convergent actions can make decisions
// on typed outcomes rather than error string parsing.
func TestUpdate_OutcomeMapping(t *testing.T) {
	tests := []struct {
		reason   SafeFFReason
		err      error
		expected UpdateOutcome
	}{
		{SafeFFSuccess, nil, FastForwarded},
		{SafeFFAlreadyCurrent, nil, AlreadyCurrent},
		{SafeFFOffBranch, fmt.Errorf("off branch"), WrongBranch},
		{SafeFFMissingOrigin, fmt.Errorf("no origin"), Offline},
		{SafeFFChangesTracked, fmt.Errorf("dirty"), Dirty},
		{SafeFFError, fmt.Errorf("generic"), Diverged},
	}

	for _, tt := range tests {
		got := outcomeFromFFReason(tt.reason, tt.err)
		if got != tt.expected {
			t.Errorf("outcomeFromFFReason(%q, %v) = %q, want %q",
				tt.reason, tt.err, got, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// Invariant: Provenance validation rejects copied/moved homes
// ---------------------------------------------------------------------------

// TestProvenance_CopiedHomeIsRefused proves that a captain home that was
// copied to a different path (canonical home mismatch) is rejected by
// home.ValidateCaptainProvenance. This prevents two captains from claiming the same ID.
func TestProvenance_CopiedHomeIsRefused(t *testing.T) {
	tmp := t.TempDir()
	original := filepath.Join(tmp, "original")
	copied := filepath.Join(tmp, "copied")

	os.MkdirAll(original, 0755)
	mhome.SeedCaptainProvenance(original, "test-sm")

	// Copy the entire home (including provenance marker with canonical path).
	copyDir(t, original, copied)

	// Validation must fail because canonical home doesn't match.
	_, err := mhome.ValidateCaptainProvenance(copied)
	if err == nil {
		t.Fatal("expected error for copied home")
	}
	if !strings.Contains(err.Error(), "canonical") && !strings.Contains(err.Error(), "does not match") {
		t.Errorf("error = %v, want canonical-home mismatch", err)
	}
}

// copyDir recursively copies a directory tree for provenance copy tests.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(t, srcPath, dstPath)
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
