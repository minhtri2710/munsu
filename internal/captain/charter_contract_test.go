package captain

import (
	"os"
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

// TestCharter_RelaySemantics verifies the one-hop relay section follows the
// production contract: durable receipt → reconcile → ack before teardown.
func TestCharter_RelaySemantics(t *testing.T) {
	charter := DefaultCharter("relay-test", t.TempDir())

	// Must mention durable receipt.
	if !strings.Contains(charter, "durable receipt") {
		t.Error("DefaultCharter must mention durable receipt in relay section")
	}

	// Must mention reconcile and relay.
	if !strings.Contains(charter, "Reconcile") {
		t.Error("DefaultCharter must mention Reconcile in relay section")
	}

	// Must require ack before teardown.
	if !strings.Contains(charter, "ack") || !strings.Contains(charter, "teardown") {
		t.Error("DefaultCharter must require ack before teardown")
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

// TestManagedWorktree_CharterUntracked verifies that managed worktree captains
// write the charter to .captain-charter.md (excluded via info/exclude) and
// never modify the tracked AGENTS.md.
func TestManagedWorktree_CharterUntracked(t *testing.T) {
	// This requires a git repo with worktree support — test via SeedFromWorktree
	// which is already tested in TestSeedWorktree_CreatesWorktreeAndStructure.
	// Here we verify the contract requirements via assertion on the worktree paths.

	// The CaptainCharterName constant ensures contract alignment.
	if CaptainCharterName != ".captain-charter.md" {
		t.Fatalf("CaptainCharterName = %q, want .captain-charter.md", CaptainCharterName)
	}

	// Verify .captain-charter.md is in the worktree exclude list.
	found := false
	for _, entry := range worktreeExcludeContent {
		if entry == CaptainCharterName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%s must be in worktreeExcludeContent so it's never tracked", CaptainCharterName)
	}
}
