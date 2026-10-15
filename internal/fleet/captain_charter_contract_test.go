package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func defaultCaptainCharter(t *testing.T, id, parent string) string {
	t.Helper()
	charter, err := DefaultCaptainCharter(id, parent)
	if err != nil {
		t.Fatal(err)
	}
	return charter
}

// =============================================================================
// Charter contract tests: delivered by review as REQUIRED for PR #302
// =============================================================================

// TestCharter_CommandRecipesValid verifies all recipe commands use correct
// authoritative syntax and no deprecated/removed commands appear.
func TestCharter_CommandRecipesValid(t *testing.T) {
	charter := defaultCaptainCharter(t, "recipe-test", t.TempDir())

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

	// Must contain munsu task commands for task lifecycle management.
	if !strings.Contains(charter, "munsu task list") {
		t.Error("DefaultCaptainCharter must contain 'munsu task list' command")
	}
	if !strings.Contains(charter, "munsu task start") {
		t.Error("DefaultCaptainCharter must contain 'munsu task start' command")
	}
	if !strings.Contains(charter, "munsu task done") {
		t.Error("DefaultCaptainCharter must contain 'munsu task done' command")
	}

	// Must contain munsu send for soldier communication.
	if !strings.Contains(charter, "munsu send") {
		t.Error("DefaultCaptainCharter must contain 'munsu send' command")
	}

	// Must contain munsu delivery for PR merge.
	if !strings.Contains(charter, "munsu delivery pr-merge") {
		t.Error("DefaultCaptainCharter must contain 'munsu delivery pr-merge' command")
	}

	// Dispatch order is list -> brief -> spawn. Spawn needs a queued task and
	// performs the queued-to-working transition itself, so the flow line must
	// not put start before spawn.
	flow := ""
	for _, line := range strings.Split(charter, "\n") {
		if strings.Contains(line, "munsu task list") && strings.Contains(line, "munsu spawn") {
			flow = line
			break
		}
	}
	listIdx := strings.Index(flow, "munsu task list")
	briefIdx := strings.Index(flow, "munsu brief")
	spawnIdx := strings.Index(flow, "munsu spawn")
	if flow == "" || !(listIdx < briefIdx && briefIdx < spawnIdx) || strings.Contains(flow, "munsu task start") {
		t.Errorf("dispatch flow must be list -> brief -> spawn with no task start, got %q", flow)
	}
}

// TestCharter_DeliveryModeNeutrality verifies the charter documents
// mode-specific behavior.
func TestCharter_DeliveryModeNeutrality(t *testing.T) {
	charter := defaultCaptainCharter(t, "mode-test", t.TempDir())

	// Must mention both delivery modes.
	if !strings.Contains(charter, "direct-PR") {
		t.Error("DefaultCaptainCharter must mention direct-PR delivery mode")
	}
	if !strings.Contains(charter, "local-only") {
		t.Error("DefaultCaptainCharter must mention local-only delivery mode")
	}

	// Must say the selected mode is authoritative.
	if !strings.Contains(charter, "selected delivery mode") {
		t.Error("DefaultCaptainCharter must state that the selected delivery mode is authoritative")
	}
}

// TestCharter_TaskAuthority verifies the charter delegates task lifecycle to
// the canonical Task Authority rather than parsing backlog files directly.
func TestCharter_TaskAuthority(t *testing.T) {
	charter := defaultCaptainCharter(t, "task-test", t.TempDir())

	// Must say the Task Authority is authoritative.
	if !strings.Contains(charter, "Task Authority") {
		t.Error("DefaultCaptainCharter must state the Task Authority is authoritative")
	}

	// Must mention munsu task list as the way to list ready items.
	if !strings.Contains(charter, "munsu task list") {
		t.Error("DefaultCaptainCharter must mention 'munsu task list' for listing ready items")
	}

	// Must forbid direct task state file editing.
	if !strings.Contains(charter, "never edit task state files directly") {
		t.Error("DefaultCaptainCharter must forbid direct task state file editing")
	}
}

// TestCharter_ReportSemantics verifies the Captain reports through its status
// file in the General's home and that nothing is pushed to a pane.
func TestCharter_ReportSemantics(t *testing.T) {
	parent := t.TempDir()
	charter := defaultCaptainCharter(t, "relay-test", parent)
	statusFile, err := home.StatusFilePath(parent, taskIDForCaptain("relay-test"))
	if err != nil {
		t.Fatal(err)
	}

	for _, required := range []string{
		"Uplink only through `munsu report`",
		"A report is a line in your status file in the General's home (" + shQuote(statusFile) + ")",
		"Nothing is pushed to any pane",
		"report wake the General settles with `munsu wake resolve`",
		"Teardown of a soldier is refused while one of its material reports is unsettled",
	} {
		if !strings.Contains(charter, required) {
			t.Errorf("DefaultCaptainCharter must mention %q", required)
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
	// (config resolver) resolves a non-empty session backend identity. The
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
	if err := SeedCaptain(CaptainSeedOptions{ID: "test-captain", Home: homePath, Repo: newWorktreeFixture(t), ParentHome: parent, Integration: fakeIntegrationPort{}}); err != nil {
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
	setupTypedParentHome(t, parent, "test-wt-captain")
	captainHome := filepath.Join(parent, "captains", "test-wt-captain")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-wt-captain", captainHome, "", "test-wt-captain"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(captainHome); err != nil {
		t.Fatal(err)
	}

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

func TestSeedCaptainRewritesOnlyAnUnusableMarker(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	setupTypedParentHome(t, parent, "test-captain")
	homePath := filepath.Join(parent, "captains", "test-captain")
	if err := os.MkdirAll(homePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Register(parent, "test-captain", homePath, "", "test-captain"); err != nil {
		t.Fatal(err)
	}
	opts := CaptainSeedOptions{ID: "test-captain", Home: homePath, Repo: newWorktreeFixture(t), ParentHome: parent, Integration: fakeIntegrationPort{}}
	if err := SeedCaptain(opts); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(homePath, home.CaptainProvenanceMarkerName)
	canonical, err := canonicalCaptainHome(homePath)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		content string // "" removes the marker
	}{
		{"missing", ""},
		{"blank id", "munsu-v2\n \n" + canonical + "\n"},
		{"two lines", "munsu-v2\ntest-captain\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.content == "" {
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(marker, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := SeedCaptain(opts); err != nil {
				t.Fatalf("SeedCaptain without --force: %v", err)
			}
			if id, err := home.ValidateCaptainProvenance(homePath); err != nil || id != "test-captain" {
				t.Fatalf("marker after reseed = (%q, %v), want (test-captain, nil)", id, err)
			}
		})
	}

	t.Run("marker naming another home is left alone", func(t *testing.T) {
		moved := "munsu-v2\ntest-captain\n/somewhere/else\n"
		if err := os.WriteFile(marker, []byte(moved), 0600); err != nil {
			t.Fatal(err)
		}
		if err := SeedCaptain(opts); err != nil {
			t.Fatalf("SeedCaptain without --force: %v", err)
		}
		if got, _ := os.ReadFile(marker); string(got) != moved {
			t.Fatalf("marker = %q, want the copied/moved marker untouched", got)
		}
	})
}
