package captain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// Charter contract tests: delivered by review as REQUIRED for PR #302
// =============================================================================

// TestExistingAGENTSMD_Preservation verifies that SeedWithParent NEVER overwrites
// an existing AGENTS.md file with a pointer — user/project-owned content is preserved.
func TestExistingAGENTSMD_Preservation(t *testing.T) {
	parent := t.TempDir()
	homePath := filepath.Join(parent, "captains", "test-captain")

	// First seed creates both .captain-charter.md and AGENTS.md.
	if err := SeedWithParent("test-captain", homePath, parent, ""); err != nil {
		t.Fatal(err)
	}

	// Write custom user content into AGENTS.md.
	customContent := "# My Custom AGENTS.md\n\nThis content must be preserved.\n"
	if err := os.WriteFile(filepath.Join(homePath, "AGENTS.md"), []byte(customContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Seed again — must NOT overwrite AGENTS.md.
	if err := SeedWithParent("test-captain", homePath, parent, ""); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(homePath, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != customContent {
		t.Fatalf("AGENTS.md was overwritten:\nwant: %q\ngot:  %q", customContent, string(body))
	}

	// .captain-charter.md should still be refreshed.
	if _, err := os.Stat(filepath.Join(homePath, CaptainCharterName)); err != nil {
		t.Errorf("%s missing after re-seed: %v", CaptainCharterName, err)
	}
}

// TestCharter_CommandRecipesValid verifies all recipe commands use correct
// authoritative syntax and no deprecated/removed commands appear.
func TestCharter_CommandRecipesValid(t *testing.T) {
	charter := DefaultCharter("recipe-test", t.TempDir())

	// Must contain the correct spawn command (not deprecated munsu launch).
	if !strings.Contains(charter, "munsu spawn") {
		t.Error("DefaultCharter must contain 'munsu spawn' command")
	}
	if strings.Contains(charter, "munsu launch") {
		t.Error("DefaultCharter must NOT contain deprecated 'munsu launch' command")
	}

	// Must contain munsu brief in the dispatch ordering.
	if !strings.Contains(charter, "munsu brief") {
		t.Error("DefaultCharter must contain 'munsu brief' command")
	}

	// Must contain tasks-axi commands for backlog management.
	if !strings.Contains(charter, "tasks-axi ready") {
		t.Error("DefaultCharter must contain 'tasks-axi ready' command")
	}
	if !strings.Contains(charter, "tasks-axi list") {
		t.Error("DefaultCharter must contain 'tasks-axi list' command")
	}
	if !strings.Contains(charter, "tasks-axi done") {
		t.Error("DefaultCharter must contain 'tasks-axi done' command")
	}

	// Must contain munsu send for soldier communication.
	if !strings.Contains(charter, "munsu send") {
		t.Error("DefaultCharter must contain 'munsu send' command")
	}

	// Must contain munsu delivery for PR merge.
	if !strings.Contains(charter, "munsu delivery pr-merge") {
		t.Error("DefaultCharter must contain 'munsu delivery pr-merge' command")
	}

	// Dispatch ordering must be correct: ready -> start -> brief -> spawn.
	if !strings.Contains(charter, "tasks-axi ready") ||
		!strings.Contains(charter, "tasks-axi start") ||
		!strings.Contains(charter, "munsu brief") ||
		!strings.Contains(charter, "munsu spawn") {
		t.Error("DefaultCharter dispatch ordering must contain ready -> start -> brief -> spawn")
	}
	// Verify the ordering appears in the correct sequence.
	readyIdx := strings.Index(charter, "tasks-axi ready")
	startIdx := strings.Index(charter, "tasks-axi start")
	briefIdx := strings.Index(charter, "munsu brief")
	spawnIdx := strings.Index(charter, "munsu spawn")
	if readyIdx < 0 || startIdx < 0 || briefIdx < 0 || spawnIdx < 0 {
		t.Error("dispatch ordering missing one or more commands")
	} else if !(readyIdx < startIdx && startIdx < briefIdx && briefIdx < spawnIdx) {
		t.Error("dispatch ordering must be: ready -> start -> brief -> spawn")
	}
}

// TestCharter_DeliveryModeNeutrality verifies the charter documents mode-specific
// behavior and does NOT mandate no-mistakes for all paths.
func TestCharter_DeliveryModeNeutrality(t *testing.T) {
	charter := DefaultCharter("mode-test", t.TempDir())

	// Must mention all three delivery modes.
	if !strings.Contains(charter, "direct-PR") {
		t.Error("DefaultCharter must mention direct-PR delivery mode")
	}
	if !strings.Contains(charter, "no-mistakes") {
		t.Error("DefaultCharter must mention no-mistakes delivery mode")
	}
	if !strings.Contains(charter, "local-only") {
		t.Error("DefaultCharter must mention local-only delivery mode")
	}

	// Must say the selected mode is authoritative.
	if !strings.Contains(charter, "selected delivery mode") {
		t.Error("DefaultCharter must state that the selected delivery mode is authoritative")
	}

	// Must NOT mandate no-mistakes for all code changes.
	if strings.Contains(charter, "All code changes go through the no-mistakes") {
		t.Error("DefaultCharter must NOT mandate no-mistakes for all code changes")
	}
}

// TestCharter_BacklogAuthority verifies the charter delegates to the selected
// backend (tasks-axi) rather than parsing backlog.md directly.
func TestCharter_BacklogAuthority(t *testing.T) {
	charter := DefaultCharter("backlog-test", t.TempDir())

	// Must say the selected backlog backend is authoritative.
	if !strings.Contains(charter, "backlog backend") && !strings.Contains(charter, "selected backlog") {
		t.Error("DefaultCharter must state the selected backlog backend is authoritative")
	}

	// Must mention tasks-axi ready as the way to list ready items.
	if !strings.Contains(charter, "tasks-axi ready") {
		t.Error("DefaultCharter must mention 'tasks-axi ready' for listing ready items")
	}

	// Must forbid direct backlog.md parsing/mutation.
	if !strings.Contains(charter, "never parse or mutate backlog.md") {
		t.Error("DefaultCharter must forbid direct backlog.md parsing/mutation")
	}
}

// TestCharter_RelaySemantics verifies the one-hop relay section matches the
// production ReconcileTerminalReceipts contract:
//   - wake/reconcile invokes production relay
//   - durable parent write must succeed BEFORE local ack
//   - teardown allowed ONLY after local exact ack / closed obligation
//   - NO wording about waiting for General munsu-send/wake acknowledgment
func TestCharter_RelaySemantics(t *testing.T) {
	charter := DefaultCharter("relay-test", t.TempDir())

	// Must mention ReconcileTerminalReceipts by name.
	if !strings.Contains(charter, "ReconcileTerminalReceipts") {
		t.Error("DefaultCharter must mention ReconcileTerminalReceipts in relay section")
	}

	// Must mention durable parent write / status write BEFORE local ack.
	if !strings.Contains(charter, "durable parent write") && !strings.Contains(charter, "parent write") {
		t.Error("DefaultCharter must mention durable parent write succeeds before local ack")
	}

	// Must mention local exact ack / turnend.WriteAck.
	if !strings.Contains(charter, "WriteAck") {
		t.Error("DefaultCharter must mention turnend.WriteAck for local ack")
	}

	// Must mention ReportRelay obligation / CompleteTaskObligation.
	if !strings.Contains(charter, "ReportRelay") || !strings.Contains(charter, "CompleteTaskObligation") {
		t.Error("DefaultCharter must mention ReportRelay/CompleteTaskObligation")
	}

	// Must require teardown only after obligation closed.
	if !strings.Contains(charter, "obligation is closed") {
		t.Error("DefaultCharter must require teardown only after obligation is closed")
	}

	// Must NOT mention waiting for General acknowledgment via munsu send.
	if strings.Contains(charter, "wait for General") {
		t.Error("DefaultCharter must NOT say 'wait for General' — production relay does not wait")
	}
}

// TestCharter_ConfigPushRefresh verifies that ConfigPush refreshes the
// .captain-charter.md with the current version.
func TestCharter_ConfigPushRefresh(t *testing.T) {
	parent := t.TempDir()
	homePath := filepath.Join(parent, "captains", "test-captain")

	// Seed a captain.
	if err := SeedWithParent("test-captain", homePath, parent, ""); err != nil {
		t.Fatal(err)
	}

	// Verify charter exists with version.
	charterBody, err := os.ReadFile(filepath.Join(homePath, CaptainCharterName))
	if err != nil {
		t.Fatalf("%s not found after seed: %v", CaptainCharterName, err)
	}
	if !strings.Contains(string(charterBody), CharterVersion) {
		t.Errorf("charter should contain version %q after seed", CharterVersion)
	}

	// Corrupt the charter with stale content.
	staleContent := "# Stale charter\n\nThis should be replaced.\n"
	if err := os.WriteFile(filepath.Join(homePath, CaptainCharterName), []byte(staleContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run ConfigPush — must refresh the charter.
	if err := ConfigPush(parent, homePath); err != nil {
		t.Fatal(err)
	}

	// Verify charter was refreshed with current version.
	refreshedBody, err := os.ReadFile(filepath.Join(homePath, CaptainCharterName))
	if err != nil {
		t.Fatal(err)
	}
	if string(refreshedBody) == staleContent {
		t.Error("ConfigPush did NOT refresh .captain-charter.md — stale content remains")
	}
	if !strings.Contains(string(refreshedBody), CharterVersion) {
		t.Errorf("refreshed charter should contain version %q", CharterVersion)
	}
	if !strings.Contains(string(refreshedBody), "Captain Charter") {
		t.Error("refreshed charter should contain 'Captain Charter' heading")
	}
}

// TestManagedWorktree_CharterUntracked is a hermetic git worktree test proving:
//   - tracked AGENTS.md is preserved byte-for-byte after seed + ConfigPush + RefreshCharter
//   - git status --porcelain is clean after refresh
//   - .captain-charter.md exists with current charter
func TestManagedWorktree_CharterUntracked(t *testing.T) {
	parent := t.TempDir()
	// Parent must be a git repo with an origin for remote validation.
	initTestRepo(t, parent, "https://github.com/test/repo.git")

	repo := t.TempDir()
	initTestRepo(t, repo, "https://github.com/test/repo.git")

	// Write a tracked AGENTS.md in the source repo BEFORE creating the worktree.
	trackedContent := "# Project AGENTS.md\n\nThis is the tracked user-owned file.\n"
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(trackedContent), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Command("git", "-C", repo, "add", "AGENTS.md").CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Command("git", "-C", repo, "commit", "-m", "add AGENTS.md").CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	// Update the tracking ref so SeedFromWorktree sees origin/main.
	if _, err := exec.Command("git", "-C", repo, "update-ref", "refs/remotes/origin/main", "HEAD").CombinedOutput(); err != nil {
		t.Fatal(err)
	}

	id := "test-wt-captain"
	homePath := filepath.Join(parent, "captains", id)

	// SeedFromWorktree creates a managed worktree at homePath.
	if err := SeedFromWorktree(id, homePath, repo, parent, "", false, ""); err != nil {
		t.Fatal(err)
	}

	// 1. Assert tracked AGENTS.md is byte-for-byte unchanged.
	agentsBody, err := os.ReadFile(filepath.Join(homePath, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(agentsBody) != trackedContent {
		t.Fatalf("tracked AGENTS.md was modified:\nwant: %q\ngot:  %q", trackedContent, string(agentsBody))
	}

	// 2. Assert git status --porcelain is clean (worktree has no tracked changes).
	statusOut, err := exec.Command("git", "-C", homePath, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(statusOut))) > 0 {
		t.Fatalf("worktree has uncommitted changes:\n%s", string(statusOut))
	}

	// 3. Assert .captain-charter.md exists with current charter content.
	charterBody, err := os.ReadFile(filepath.Join(homePath, CaptainCharterName))
	if err != nil {
		t.Fatalf("%s not found: %v", CaptainCharterName, err)
	}
	if !strings.Contains(string(charterBody), CharterVersion) {
		t.Errorf("%s should contain charter version %q", CaptainCharterName, CharterVersion)
	}

	// 4. Run ConfigPush + RefreshCharter and re-assert cleanliness.
	if err := ConfigPush(parent, homePath); err != nil {
		t.Fatal(err)
	}

	// AGENTS.md still byte-for-byte unchanged.
	agentsBody2, err := os.ReadFile(filepath.Join(homePath, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(agentsBody2) != trackedContent {
		t.Fatalf("AGENTS.md was modified after ConfigPush:\nwant: %q\ngot:  %q", trackedContent, string(agentsBody2))
	}

	// git status still clean.
	statusOut2, err := exec.Command("git", "-C", homePath, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(statusOut2))) > 0 {
		t.Fatalf("worktree dirty after ConfigPush:\n%s", string(statusOut2))
	}

	// .captain-charter.md still exists (now refreshed).
	if _, err := os.Stat(filepath.Join(homePath, CaptainCharterName)); err != nil {
		t.Errorf("%s missing after ConfigPush: %v", CaptainCharterName, err)
	}
}
