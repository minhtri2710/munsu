package soldier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// E2E contract: full soldier launch prompt ⇒ charter + brief + envelope + report identity
// =============================================================================

// TestE2E_SoldierFullPrompt verifies the full launch pipeline:
//  1. BuildLaunchPrompt produces a complete charter + brief + terminal report
//  2. PersistLaunchFiles writes all durable files with correct hashes
//  3. VerifyEnvelopeIntegrity passes on the written files
//  4. The prompt contains charter content, brief content, forbidden actions, and report command
func TestE2E_SoldierFullPrompt(t *testing.T) {
	worktree := t.TempDir()
	briefContent := []byte(`# Task brief: e2e-test

## Setup
You are in a disposable git worktree.

1. First action: create your branch: git checkout -b fm/e2e-test

## Rules
1. Never push to the default branch.

## Definition of done
When delivery is complete, run munsu report done "PR {url}" and stop.
`)

	input := LaunchPromptInput{
		TaskID:          "e2e-test",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		Repository:      "munsu",
		ParentCaptainID: "captain-alpha",
		ParentHome:      "/tmp/general-home",
		WorktreePath:    worktree,
		HomeDir:         "/tmp/general-home",
		BriefContent:    briefContent,
		HarnessName:     "pi",
	}

	// 1. Build the prompt and envelope.
	prompt, env, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}

	// Verify prompt contains charter + brief + forbidden actions + report command.
	if !strings.Contains(prompt, CharterVersion) {
		t.Error("prompt must contain charter version")
	}
	if !strings.Contains(prompt, "e2e-test") {
		t.Error("prompt must contain task ID")
	}
	if !strings.Contains(prompt, "direct-PR") {
		t.Error("prompt must contain delivery mode")
	}
	if !strings.Contains(prompt, "Never push to the default branch") {
		t.Error("prompt must contain forbidden action: no push to default")
	}
	if !strings.Contains(prompt, "munsu report done") {
		t.Error("prompt must contain terminal report requirement")
	}
	if !strings.Contains(prompt, "captain-alpha") {
		t.Error("prompt must contain parent captain ID")
	}

	// All hashes are populated.
	if env.CharterSHA256 == "" {
		t.Error("envelope must have charter hash")
	}
	if env.BriefSHA256 == "" {
		t.Error("envelope must have brief hash")
	}
	if env.PromptSHA256 == "" {
		t.Error("envelope must have prompt hash")
	}
	if env.TaskID != "e2e-test" {
		t.Errorf("envelope TaskID = %q, want 'e2e-test'", env.TaskID)
	}
	if env.ParentCaptainID != "captain-alpha" {
		t.Errorf("envelope ParentCaptainID = %q, want 'captain-alpha'", env.ParentCaptainID)
	}

	// 2. Persist durable files.
	charter := DefaultCharter(input.TaskID, input.TaskKind, input.DeliveryMode)
	if err := PersistLaunchFiles(worktree, charter, briefContent, env); err != nil {
		t.Fatal(err)
	}

	// 3. Verify integrity.
	if err := VerifyEnvelopeIntegrity(worktree); err != nil {
		t.Errorf("integrity verification failed: %v", err)
	}

	// 4. Read back envelope and verify fields.
	readEnv, err := ReadEnvelope(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if readEnv.PromptSHA256 != env.PromptSHA256 {
		t.Errorf("persisted prompt hash mismatch: %q vs %q", readEnv.PromptSHA256, env.PromptSHA256)
	}
	if readEnv.CharterSHA256 != sha256Content([]byte(charter)) {
		t.Error("persisted charter hash does not match actual charter content")
	}
}

// TestE2E_SoldierReportIdentity verifies that the prompt contains the exact
// terminal report command referencing the task ID, enabling the parent Captain
// to correlate the soldier's munsu report done with the deployment.
func TestE2E_SoldierReportIdentity(t *testing.T) {
	brief := []byte("# Task\n\nContent.\n")

	prompt, env, err := BuildLaunchPrompt(LaunchPromptInput{
		TaskID:          "my-task-42",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-main",
		ParentHome:      "/tmp/parent",
		WorktreePath:    t.TempDir(),
		HomeDir:         "/tmp/home",
		BriefContent:    brief,
		HarnessName:     "codex",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The exact report command embeds the task key.
	expectedKey := `[--key my-task-42]`
	if !strings.Contains(prompt, expectedKey) {
		t.Errorf("prompt must contain --key with task ID %q for terminal report correlation", "my-task-42")
	}

	// The report command must reference PR {url} for ship tasks.
	if !strings.Contains(prompt, `PR {url}`) {
		t.Error("prompt must contain 'PR {url}' in terminal report command")
	}

	// Envelope must carry the exact task ID for identity.
	if env.TaskID != "my-task-42" {
		t.Errorf("env.TaskID = %q, want 'my-task-42'", env.TaskID)
	}
}

// TestE2E_SkillSelection verifies skill manifest integration:
//   - Required soldier-applicable skills included
//   - Captain-only skills excluded
//   - Optional skills produce diagnostics only on omission
func TestE2E_SkillSelection(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "gh-axi", Role: "soldier", SourcePath: "/dev/null", Version: "1.0"},
		{Name: "tasks-axi", Role: "soldier", SourcePath: "/dev/null", Version: "2.0"},
		{Name: "captain-provisioning", Role: "captain", SourcePath: "/dev/null"},
		{Name: "fleet-ops", Role: "general", SourcePath: "/dev/null"},
	}

	required, optional, diags := CollectSkills(catalog,
		[]string{"gh-axi", "tasks-axi", "captain-provisioning"},
		[]string{"fleet-ops"})

	// Required: gh-axi + tasks-axi should be applicable.
	var foundGhAxi, foundTasksAxi bool
	for _, s := range required {
		if s.Name == "gh-axi" && s.Applicable {
			foundGhAxi = true
		}
		if s.Name == "tasks-axi" && s.Applicable {
			foundTasksAxi = true
		}
	}
	if !foundGhAxi || !foundTasksAxi {
		t.Error("gh-axi and tasks-axi must be in required and applicable")
	}

	// Captain-only skill must be marked non-applicable with diagnostic.
	var foundCaptainOnly bool
	for _, s := range required {
		if s.Name == "captain-provisioning" {
			foundCaptainOnly = true
			if s.Applicable {
				t.Error("captain-provisioning must NOT be applicable for soldier")
			}
		}
	}
	if !foundCaptainOnly {
		t.Error("captain-provisioning must appear in required (as non-applicable)")
	}

	// Captain-only diagnostic.
	var hasCaptainDiag bool
	for _, d := range diags {
		if strings.Contains(d, "captain-provisioning") {
			hasCaptainDiag = true
		}
	}
	if !hasCaptainDiag {
		t.Error("should have diagnostic about captain-only skill")
	}

	// General-only optional excluded.
	if len(optional) > 0 {
		t.Error("general-only skill should not appear in optional list")
	}
}

// TestE2E_HashRecovery verifies that the prompt hash enables deterministic
// recovery: same inputs → same hash → same prompt.
func TestE2E_HashRecovery(t *testing.T) {
	wt := t.TempDir()
	brief := []byte("# Task\n\nRecovery test.\n")

	// Build twice with same inputs.
	input := LaunchPromptInput{
		TaskID:          "recovery-test",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-recovery",
		ParentHome:      "/tmp/parent",
		WorktreePath:    wt,
		HomeDir:         "/tmp/home",
		BriefContent:    brief,
		HarnessName:     "pi",
	}

	prompt1, env1, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}

	prompt2, env2, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}

	if prompt1 != prompt2 {
		t.Error("prompts must be identical for identical inputs (deterministic recovery)")
	}
	if env1.PromptSHA256 != env2.PromptSHA256 {
		t.Error("prompt hashes must be identical for identical inputs")
	}
	if env1.CharterSHA256 != env2.CharterSHA256 {
		t.Error("charter hashes must be identical for identical inputs")
	}
}

// TestE2E_VerifyEnvelopeOnDiskConsistency verifies that the envelope file
// on disk is consistent with the charter and brief files after persist.
func TestE2E_VerifyEnvelopeOnDiskConsistency(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("disk-test", "ship", "direct-PR")
	brief := []byte("# Task\n\nDisk consistency.\n")

	charterHash := sha256Content([]byte(charter))
	briefHash := sha256Content(brief)

	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "disk-test",
		CharterSHA256:   charterHash,
		BriefSHA256:     briefHash,
	}

	if err := PersistLaunchFiles(tmp, charter, brief, env); err != nil {
		t.Fatal(err)
	}

	// Direct file checks.
	for _, name := range []string{CharterName, BriefName, EnvelopeName} {
		path := filepath.Join(tmp, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("file %s does not exist on disk", name)
		}
	}

	// Read envelope and verify hashes match actual content.
	readEnv, err := ReadEnvelope(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if readEnv.CharterSHA256 != sha256Content([]byte(charter)) {
		t.Error("envelope charter hash does not match actual charter on disk")
	}
	if readEnv.BriefSHA256 != sha256Content(brief) {
		t.Error("envelope brief hash does not match actual brief on disk")
	}
}
