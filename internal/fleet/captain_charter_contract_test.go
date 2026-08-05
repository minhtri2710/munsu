package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// =============================================================================
// Charter contract tests: delivered by review as REQUIRED for PR #302
// =============================================================================

// TestExistingAGENTSMD_Preservation verifies that SeedWithParent NEVER overwrites
// an existing AGENTS.md file with a pointer — user/project-owned content is preserved.
func TestExistingAGENTSMD_Preservation(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	// Typed parent base with explicit Backend so SeedCaptain's config inherit
	// (ResolveProject) resolves a non-empty session backend identity. The
	// registration mirrors ensureParentTypedConfig's default-project binding
	// (which is skipped once the typed base exists).
	setupTypedParentHome(t, parent, "test-captain")
	homePath := filepath.Join(parent, "captains", "test-captain")
	if err := os.MkdirAll(homePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-captain", homePath, "", "test-captain"); err != nil {
		t.Fatal(err)
	}

	// First seed creates both .captain-charter.md and AGENTS.md.
	if err := SeedCaptain(CaptainSeedOptions{ID: "test-captain", Home: homePath, ParentHome: parent, Integration: fakeIntegrationPort{}}); err != nil {
		t.Fatal(err)
	}

	// Write custom user content into AGENTS.md.
	customContent := "# My Custom AGENTS.md\n\nThis content must be preserved.\n"
	if err := os.WriteFile(filepath.Join(homePath, "AGENTS.md"), []byte(customContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Seed again — must NOT overwrite AGENTS.md.
	if err := SeedCaptain(CaptainSeedOptions{ID: "test-captain", Home: homePath, ParentHome: parent, Integration: fakeIntegrationPort{}}); err != nil {
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
	charter := DefaultCaptainCharter("recipe-test", t.TempDir())

	// Must contain the correct spawn command (not deprecated munsu launch).
	if !strings.Contains(charter, "munsu spawn") {
		t.Error("DefaultCaptainCharter must contain 'munsu spawn' command")
	}
	if strings.Contains(charter, "munsu launch") {
		t.Error("DefaultCaptainCharter must NOT contain deprecated 'munsu launch' command")
	}

	// Must contain munsu brief in the dispatch ordering.
	if !strings.Contains(charter, "munsu brief") {
		t.Error("DefaultCaptainCharter must contain 'munsu brief' command")
	}

	// Must contain tasks-axi commands for backlog management.
	if !strings.Contains(charter, "tasks-axi ready") {
		t.Error("DefaultCaptainCharter must contain 'tasks-axi ready' command")
	}
	if !strings.Contains(charter, "tasks-axi list") {
		t.Error("DefaultCaptainCharter must contain 'tasks-axi list' command")
	}
	if !strings.Contains(charter, "tasks-axi done") {
		t.Error("DefaultCaptainCharter must contain 'tasks-axi done' command")
	}

	// Must contain munsu send for soldier communication.
	if !strings.Contains(charter, "munsu send") {
		t.Error("DefaultCaptainCharter must contain 'munsu send' command")
	}

	// Must contain munsu delivery for PR merge.
	if !strings.Contains(charter, "munsu delivery pr-merge") {
		t.Error("DefaultCaptainCharter must contain 'munsu delivery pr-merge' command")
	}

	// Dispatch ordering must be correct: ready -> start -> brief -> spawn.
	if !strings.Contains(charter, "tasks-axi ready") ||
		!strings.Contains(charter, "tasks-axi start") ||
		!strings.Contains(charter, "munsu brief") ||
		!strings.Contains(charter, "munsu spawn") {
		t.Error("DefaultCaptainCharter dispatch ordering must contain ready -> start -> brief -> spawn")
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
	charter := DefaultCaptainCharter("mode-test", t.TempDir())

	// Must mention all three delivery modes.
	if !strings.Contains(charter, "direct-PR") {
		t.Error("DefaultCaptainCharter must mention direct-PR delivery mode")
	}
	if !strings.Contains(charter, "no-mistakes") {
		t.Error("DefaultCaptainCharter must mention no-mistakes delivery mode")
	}
	if !strings.Contains(charter, "local-only") {
		t.Error("DefaultCaptainCharter must mention local-only delivery mode")
	}

	// Must say the selected mode is authoritative.
	if !strings.Contains(charter, "selected delivery mode") {
		t.Error("DefaultCaptainCharter must state that the selected delivery mode is authoritative")
	}

	// Must NOT mandate no-mistakes for all code changes.
	if strings.Contains(charter, "All code changes go through the no-mistakes") {
		t.Error("DefaultCaptainCharter must NOT mandate no-mistakes for all code changes")
	}
}

// TestCharter_BacklogAuthority verifies the charter delegates to the selected
// backend (tasks-axi) rather than parsing backlog.md directly.
func TestCharter_BacklogAuthority(t *testing.T) {
	charter := DefaultCaptainCharter("backlog-test", t.TempDir())

	// Must say the selected backlog backend is authoritative.
	if !strings.Contains(charter, "backlog backend") && !strings.Contains(charter, "selected backlog") {
		t.Error("DefaultCaptainCharter must state the selected backlog backend is authoritative")
	}

	// Must mention tasks-axi ready as the way to list ready items.
	if !strings.Contains(charter, "tasks-axi ready") {
		t.Error("DefaultCaptainCharter must mention 'tasks-axi ready' for listing ready items")
	}

	// Must forbid direct backlog.md parsing/mutation.
	if !strings.Contains(charter, "never parse or mutate backlog.md") {
		t.Error("DefaultCaptainCharter must forbid direct backlog.md parsing/mutation")
	}
}

// TestCharter_RelaySemantics verifies the mailbox-only one-hop Uplink Report contract.
func TestCharter_RelaySemantics(t *testing.T) {
	charter := DefaultCaptainCharter("relay-test", t.TempDir())

	for _, required := range []string{
		"One-Hop Uplink Report",
		"immutable envelope",
		"NotificationRef",
		"inbox receive",
		"inbox ack",
		"Processing Ack",
		"after 60 seconds",
		"latest material report supersedes",
		"Teardown is allowed only after",
	} {
		if !strings.Contains(charter, required) {
			t.Errorf("DefaultCaptainCharter must mention %q", required)
		}
	}
	for _, legacy := range []string{"ReconcileTerminalReceipts", "turnend.WriteAck", "CompleteTaskObligation"} {
		if strings.Contains(charter, legacy) {
			t.Errorf("DefaultCaptainCharter must not prescribe legacy relay %q", legacy)
		}
	}
}

// TestCharter_ConfigPushRefresh verifies that ConfigPush refreshes the
// .captain-charter.md with the current version.
func TestCharter_ConfigPushRefresh(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	// Typed parent base with explicit Backend so SeedCaptain's config inherit
	// (ResolveProject) resolves a non-empty session backend identity. The
	// registration mirrors ensureParentTypedConfig's default-project binding
	// (which is skipped once the typed base exists).
	setupTypedParentHome(t, parent, "test-captain")
	homePath := filepath.Join(parent, "captains", "test-captain")
	if err := os.MkdirAll(homePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-captain", homePath, "", "test-captain"); err != nil {
		t.Fatal(err)
	}

	// Seed a captain.
	if err := SeedCaptain(CaptainSeedOptions{ID: "test-captain", Home: homePath, ParentHome: parent, Integration: fakeIntegrationPort{}}); err != nil {
		t.Fatal(err)
	}

	// Verify charter exists with version.
	charterBody, err := os.ReadFile(filepath.Join(homePath, CaptainCharterName))
	if err != nil {
		t.Fatalf("%s not found after seed: %v", CaptainCharterName, err)
	}
	if !strings.Contains(string(charterBody), CaptainCharterVersion) {
		t.Errorf("charter should contain version %q after seed", CaptainCharterVersion)
	}

	// Corrupt the charter with stale content.
	staleContent := "# Stale charter\n\nThis should be replaced.\n"
	if err := os.WriteFile(filepath.Join(homePath, CaptainCharterName), []byte(staleContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run ConfigPush — must refresh the charter.
	if err := configPush(parent, homePath); err != nil {
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
	if !strings.Contains(string(refreshedBody), CaptainCharterVersion) {
		t.Errorf("refreshed charter should contain version %q", CaptainCharterVersion)
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
	// The parent is both a munsu home (canonical) and a git repo with an
	// origin for remote validation. Initialize the home first so the Fleet
	// Registry can operate on it.
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
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
	if err := seedFromWorktreeTest(id, homePath, repo, parent, "", false, ""); err != nil {
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
	if !strings.Contains(string(charterBody), CaptainCharterVersion) {
		t.Errorf("%s should contain charter version %q", CaptainCharterName, CaptainCharterVersion)
	}

	// 4. Run configPush + RefreshCharter and re-assert cleanliness.
	if err := configPush(parent, homePath); err != nil {
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
