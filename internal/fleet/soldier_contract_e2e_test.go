//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// E2E contract: full soldier launch prompt => charter + brief + envelope + report identity
// =============================================================================

// writeLaunchManifestForTest stands in the launch script production's
// submitLaunch would have written, then calls the production writer itself for
// the manifest. It returns the manifest digest — the value production stores
// outside the worktree as the launch_manifest_sha256 anchor.
//
// The manifest is deliberately NOT rebuilt here. A second copy of the entry
// list and the migration policy would be a mirror of Runner.writeLaunchManifest
// with nothing binding the two: an entry added or a policy dropped on the
// production side would leave these tests asserting a manifest shape no soldier
// is ever launched with, and green (BEO-95, BEO-70). Only r.wtPath is read and
// only r.manifestSHA256 is written by that phase, so a bare Runner is the whole
// fixture it needs.
func writeLaunchManifestForTest(t *testing.T, worktreePath string) string {
	t.Helper()
	script := "#!/usr/bin/env bash\nexec true\n"
	if err := os.WriteFile(filepath.Join(worktreePath, LaunchScriptName), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	r := &Runner{wtPath: worktreePath}
	if err := r.writeLaunchManifest(); err != nil {
		t.Fatalf("writing launch manifest: %v", err)
	}

	// The entry set the writer declares is already guarded: ValidateManifest
	// holds the expected paths and WriteManifest refuses a manifest missing one,
	// so a dropped entry fails the line above. The migration policy has no such
	// guard -- it is optional in the schema, so a writer that stops declaring it
	// still produces a valid manifest, and retirement then treats a leftover
	// .soldier-md as unexplained. Assert it here, where every e2e caller sees it.
	manifest, err := ReadManifest(worktreePath)
	if err != nil {
		t.Fatalf("reading back the written manifest: %v", err)
	}
	if manifest.LegacyBriefMigration == nil || *manifest.LegacyBriefMigration != LegacyBriefMatchCanonicalV1 {
		t.Fatalf("production writer must declare legacy brief migration %q, got %v",
			LegacyBriefMatchCanonicalV1, manifest.LegacyBriefMigration)
	}
	return r.manifestSHA256
}

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

	prompt, env, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}

	// Verify prompt content.
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
	if !strings.Contains(prompt, `munsu report done "PR {url}" --key e2e-test`) {
		t.Error("prompt must contain exact terminal report command with --key")
	}
	if !strings.Contains(prompt, "captain-alpha") {
		t.Error("prompt must contain parent captain ID")
	}

	// Envelope fields.
	if env.TaskID != "e2e-test" {
		t.Errorf("envelope TaskID = %q, want 'e2e-test'", env.TaskID)
	}
	if env.ParentCaptainID != "captain-alpha" {
		t.Errorf("envelope ParentCaptainID = %q, want 'captain-alpha'", env.ParentCaptainID)
	}

	// Persist and verify all files.
	charter := DefaultCharter(input.TaskID, input.TaskKind, input.DeliveryMode)
	if err := PersistLaunchFiles(worktree, charter, briefContent, env, prompt); err != nil {
		t.Fatal(err)
	}

	// Verify all four durable files exist.
	for _, name := range []string{CharterName, BriefName, PromptName, EnvelopeName} {
		path := filepath.Join(worktree, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s not persisted: %v", name, err)
		}
	}

	// Anchor the durable files with the launch manifest, then verify them
	// against the digest production keeps outside the worktree.
	manifestSHA := writeLaunchManifestForTest(t, worktree)
	if err := VerifyLaunchArtifacts(worktree, manifestSHA); err != nil {
		t.Errorf("launch artifact verification failed: %v", err)
	}

	// The persisted prompt is exactly what BuildLaunchPrompt returned, and the
	// manifest is what proves it.
	promptOnDisk, err := os.ReadFile(filepath.Join(worktree, PromptName))
	if err != nil {
		t.Fatal(err)
	}
	if string(promptOnDisk) != prompt {
		t.Error("persisted prompt does not match the built prompt")
	}

	// A tampered artifact must fail against the anchor.
	if err := os.WriteFile(filepath.Join(worktree, PromptName), []byte("tampered prompt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLaunchArtifacts(worktree, manifestSHA); err == nil {
		t.Error("tampered prompt must fail launch artifact verification")
	}
}

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
		HarnessName:     "pi",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Exact report command with required --key (no brackets).
	expectedLine := `munsu report done "PR {url}" --key my-task-42`
	if !strings.Contains(prompt, expectedLine) {
		t.Errorf("prompt must contain exact terminal report command:\nwant: %s", expectedLine)
	}

	if env.TaskID != "my-task-42" {
		t.Errorf("env.TaskID = %q, want 'my-task-42'", env.TaskID)
	}
}

func TestE2E_SkillSelectionWithDenylist(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "gh-axi", Role: "soldier"},
		{Name: "qmd", Role: "soldier"},
		{Name: "munsu-ops", Role: "soldier"}, // denied by denylist regardless of role
		{Name: "captain-provisioning", Role: "captain"},
		{Name: "bootstrap-diagnostics", Role: "general"},
	}

	required, optional, diags := CollectSkills(catalog,
		[]string{"gh-axi", "qmd", "captain-provisioning", "munsu-ops"},
		[]string{"bootstrap-diagnostics"})

	// gh-axi and qmd must be applicable.
	var foundGhAxi, foundQmd bool
	for _, s := range required {
		if s.Name == "gh-axi" && s.Applicable {
			foundGhAxi = true
		}
		if s.Name == "qmd" && s.Applicable {
			foundQmd = true
		}
	}
	if !foundGhAxi || !foundQmd {
		t.Error("gh-axi and qmd must be in required and applicable")
	}

	// captain-provisioning must be non-applicable (captain role).
	var foundCaptain bool
	for _, s := range required {
		if s.Name == "captain-provisioning" {
			foundCaptain = true
			if s.Applicable {
				t.Error("captain-provisioning must NOT be applicable for soldier")
			}
		}
	}
	if !foundCaptain {
		t.Error("captain-provisioning must appear in required (as non-applicable)")
	}

	// munsu-ops denied by denylist.
	for _, s := range required {
		if s.Name == "munsu-ops" {
			if s.Applicable {
				t.Error("munsu-ops must NOT be applicable (denied by denylist)")
			}
		}
	}

	// General-only optional excluded.
	if len(optional) > 0 {
		t.Error("general-only skill should not appear in optional list")
	}

	// Diagnostics must include denylist/role issues.
	var hasDiagDeny bool
	for _, d := range diags {
		if strings.Contains(d, "captain-provisioning") || strings.Contains(d, "munsu-ops") {
			hasDiagDeny = true
		}
	}
	if !hasDiagDeny {
		t.Error("should have diagnostic about denied skills")
	}
}

func TestE2E_HashRecovery(t *testing.T) {
	wt := t.TempDir()
	brief := []byte("# Task\n\nRecovery test.\n")

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

	prompt1, _, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	prompt2, _, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	if prompt1 != prompt2 {
		t.Error("prompts must be identical for identical inputs (deterministic recovery)")
	}
	if sha256Content([]byte(prompt1)) != sha256Content([]byte(prompt2)) {
		t.Error("prompt digests must be identical for identical inputs")
	}
}

func TestE2E_VerifyAllDurableFilesOnDisk(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("disk-test", "ship", "direct-PR")
	brief := []byte("# Task\n\nDisk consistency.\n")
	prompt := "full prompt content\n"

	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "disk-test",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
	}
	if err := PersistLaunchFiles(tmp, charter, brief, env, prompt); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{CharterName, BriefName, PromptName, EnvelopeName} {
		path := filepath.Join(tmp, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("file %s does not exist on disk", name)
		}
	}

	manifestSHA := writeLaunchManifestForTest(t, tmp)
	if err := VerifyLaunchArtifacts(tmp, manifestSHA); err != nil {
		t.Fatalf("full artifact verification failed: %v", err)
	}
}

func TestE2E_TerminalReportHasExactKey(t *testing.T) {
	// Terminal report must produce exact `munsu report done "PR {url}" --key <id>`.
	taskID := "e2e-exec-test"
	brief := []byte("# Task\n\nDo work and report.\n")

	prompt, _, err := BuildLaunchPrompt(LaunchPromptInput{
		TaskID:          taskID,
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-e2e",
		ParentHome:      "/tmp/parent",
		WorktreePath:    t.TempDir(),
		HomeDir:         "/tmp/home",
		BriefContent:    brief,
		HarnessName:     "pi",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The --key value must be the task ID exactly.
	expected := `munsu report done "PR {url}" --key ` + taskID
	if !strings.Contains(prompt, expected) {
		t.Errorf("prompt must contain exact terminal report:\nwant: %s", expected)
	}

	// Must NOT have brackets around --key.
	if strings.Contains(prompt, "[--key") {
		t.Error("prompt must NOT contain literal brackets around --key")
	}
}

// TestE2E_FullProductionFlow simulates the entire production launch sequence:
//  1. BuildPrompt + Envelope (identity, delivery mode)
//  2. PersistLaunchFiles (charter + brief + prompt + envelope)
//  3. Launch manifest written, artifacts verified against its digest
//  4. BuildLaunchArgs (Pi harness with model/effort/prompt)
//  5. Prompt contains exact terminal report command with --key <task-id>
//  6. Tampering is rejected by the anchor even when the worktree is
//     made self-consistent again
func TestE2E_FullProductionFlow(t *testing.T) {
	worktree := t.TempDir()
	taskID := "prod-flow-99"

	briefContent := []byte(`# Task brief: ` + taskID + `

## Setup
You are in a disposable git worktree.

1. git checkout -b fm/` + taskID + `

## Rules
1. Never push to the default branch.
2. Stay inside this worktree.

## Definition of done
Task complete when committed. Run munsu report done "PR {url}" and stop.
`)

	// Step 1: Build prompt and envelope.
	input := LaunchPromptInput{
		TaskID:          taskID,
		TaskKind:        "ship",
		DeliveryMode:    "no-mistakes",
		Repository:      "munsu",
		ParentCaptainID: "captain-prod",
		ParentHome:      "/tmp/general-home",
		WorktreePath:    worktree,
		HomeDir:         "/tmp/general-home",
		BriefContent:    briefContent,
		HarnessName:     "pi",
	}

	promptText, env, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}

	// Verify envelope identity is complete.
	if env.TaskID != taskID {
		t.Errorf("env.TaskID = %q, want %q", env.TaskID, taskID)
	}
	if env.DeliveryMode != "no-mistakes" {
		t.Errorf("env.DeliveryMode = %q, want 'no-mistakes'", env.DeliveryMode)
	}
	if env.ParentCaptainID != "captain-prod" {
		t.Errorf("env.ParentCaptainID = %q, want 'captain-prod'", env.ParentCaptainID)
	}

	// Step 2: Persist all files.
	charter := DefaultCharter(input.TaskID, input.TaskKind, input.DeliveryMode)
	if err := PersistLaunchFiles(worktree, charter, briefContent, env, promptText); err != nil {
		t.Fatal(err)
	}

	// Step 3: Anchor the persisted files and verify them against the anchor.
	manifestSHA := writeLaunchManifestForTest(t, worktree)
	if err := VerifyLaunchArtifacts(worktree, manifestSHA); err != nil {
		t.Fatalf("launch artifact verification failed: %v", err)
	}

	// Step 4: Build launch args (Pi harness).
	bin, args, err := BuildLaunchArgs(worktree, "pi", "gpt-5", "high", promptText)
	if err != nil {
		t.Fatal(err)
	}
	if bin != "pi" {
		t.Errorf("bin = %q, want 'pi'", bin)
	}
	// Verify model/effort flags present.
	hasModel, hasEffort := false, false
	for i, arg := range args {
		if arg == "--model" && i+1 < len(args) && args[i+1] == "gpt-5" {
			hasModel = true
		}
		if arg == "--thinking" && i+1 < len(args) && args[i+1] == "high" {
			hasEffort = true
		}
	}
	if !hasModel {
		t.Error("launch args must include --model gpt-5")
	}
	if !hasEffort {
		t.Error("launch args must include --thinking high")
	}
	// Verify prompt is last arg.
	lastArg := args[len(args)-1]
	if lastArg != promptText {
		t.Error("last launch arg must be the complete prompt")
	}

	// Step 5: Verify terminal report command with exact --key.
	expectedReport := `munsu report done "PR {url}" --key ` + taskID
	if !strings.Contains(promptText, expectedReport) {
		t.Errorf("prompt must contain exact report command:\nwant: %s", expectedReport)
	}

	// Step 6: A self-consistent worktree is not evidence. Tamper with the
	// charter and refresh the manifest to match — whoever can write the
	// charter can write the manifest beside it. Only the anchor held outside
	// the worktree rejects this.
	if err := os.WriteFile(filepath.Join(worktree, CharterName), []byte(charter+"\ntampered\n"), 0644); err != nil {
		t.Fatal(err)
	}
	refreshedSHA := writeLaunchManifestForTest(t, worktree)
	if refreshedSHA == manifestSHA {
		t.Fatal("refreshed manifest digest must differ after tampering")
	}
	if err := VerifyLaunchArtifacts(worktree, refreshedSHA); err != nil {
		t.Errorf("refreshed manifest is self-consistent, so it must pass on its own terms: %v", err)
	}
	if err := VerifyLaunchArtifacts(worktree, manifestSHA); err == nil {
		t.Error("tampered charter with a refreshed manifest must fail against the original anchor")
	}
}

// TestRegression_SkillSelectionWithoutSrcwalk proves that the soldier contract
// skill selection works correctly without srcwalk in the catalog. Focused
// regression guard for the remove-srcwalk-integration task.
func TestRegression_SkillSelectionWithoutSrcwalk(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "gh-axi", Role: "soldier"},
		{Name: "qmd", Role: "soldier"},
		{Name: "chrome-devtools-axi", Role: "soldier"},
	}

	required, optional, diags := CollectSkills(catalog,
		[]string{"gh-axi"},
		[]string{"qmd", "chrome-devtools-axi"})

	if len(diags) > 0 {
		t.Errorf("unexpected diagnostics without srcwalk: %v", diags)
	}

	// Verify gh-axi is applicable and required.
	var foundGhAxi bool
	for _, s := range required {
		if s.Name == "gh-axi" {
			foundGhAxi = true
			if !s.Applicable {
				t.Error("gh-axi must be applicable")
			}
		}
	}
	if !foundGhAxi {
		t.Error("gh-axi must be in required skills")
	}

	// Verify optional skills are present.
	var foundQmd, foundChrome bool
	for _, s := range optional {
		if s.Name == "qmd" {
			foundQmd = true
			if !s.Applicable {
				t.Error("qmd must be applicable")
			}
		}
		if s.Name == "chrome-devtools-axi" {
			foundChrome = true
			if !s.Applicable {
				t.Error("chrome-devtools-axi must be applicable")
			}
		}
		// Verify srcwalk never appears in optional.
		if s.Name == "srcwalk" {
			t.Error("srcwalk must NOT be in optional skills")
		}
	}
	if !foundQmd {
		t.Error("qmd must be in optional skills")
	}
	if !foundChrome {
		t.Error("chrome-devtools-axi must be in optional skills")
	}
}

// TestRegression_BuildLaunchPromptWithoutSrcwalk proves that prompt generation
// does not reference srcwalk and produces a valid soldier charter. Focused
// regression guard for the remove-srcwalk-integration task.
func TestRegression_BuildLaunchPromptWithoutSrcwalk(t *testing.T) {
	wt := t.TempDir()
	brief := []byte("# Regression\n\nClean skill environment.\n")

	input := LaunchPromptInput{
		TaskID:          "regression-clean-env",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-test",
		ParentHome:      "/tmp/test",
		WorktreePath:    wt,
		HomeDir:         "/tmp/test-home",
		BriefContent:    brief,
		HarnessName:     "pi",
		RequiredSkills: []SkillEntry{
			{Name: "gh-axi", Role: "soldier", Applicable: true},
		},
		OptionalSkills: []SkillEntry{
			{Name: "qmd", Role: "soldier", Applicable: true},
		},
	}

	prompt, env, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatalf("BuildLaunchPrompt failed without srcwalk: %v", err)
	}

	// Verify prompt does NOT reference srcwalk.
	if strings.Contains(prompt, "srcwalk") {
		t.Error("prompt must NOT contain srcwalk references")
	}

	// Verify exact generated sections from applicable skills.
	if !strings.Contains(prompt, "## Required Skills") {
		t.Error("prompt must contain ## Required Skills section")
	}
	if !strings.Contains(prompt, "- gh-axi") {
		t.Error("prompt must list gh-axi under required skills")
	}
	if !strings.Contains(prompt, "## Optional Skills") {
		t.Error("prompt must contain ## Optional Skills section")
	}
	if !strings.Contains(prompt, "- qmd") {
		t.Error("prompt must contain - qmd in optional skills")
	}

	// Verify env does not reference srcwalk.
	for _, s := range env.RequiredSkills {
		if s.Name == "srcwalk" {
			t.Error("srcwalk must NOT be in env.RequiredSkills")
		}
	}
	for _, s := range env.OptionalSkills {
		if s.Name == "srcwalk" {
			t.Error("srcwalk must NOT be in env.OptionalSkills")
		}
	}

	// Verify prompt contains essential soldier charter elements.
	if !strings.Contains(prompt, "Soldier Charter") {
		t.Error("prompt must contain soldier charter header")
	}
}
