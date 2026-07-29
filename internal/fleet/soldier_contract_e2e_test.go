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

	// Verify envelope integrity.
	if err := VerifyEnvelopeIntegrity(worktree); err != nil {
		t.Errorf("integrity verification failed: %v", err)
	}

	// Verify prompt file hash matches envelope.
	readEnv, err := ReadEnvelope(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if readEnv.PromptSHA256 != sha256Content([]byte(prompt)) {
		t.Error("persisted prompt hash does not match actual prompt on disk")
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
		{Name: "tasks-axi", Role: "soldier"}, // denied by denylist
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

func TestE2E_VerifyAllDurableFilesOnDisk(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("disk-test", "ship", "direct-PR")
	brief := []byte("# Task\n\nDisk consistency.\n")
	prompt := "full prompt content\n"

	charterHash := sha256Content([]byte(charter))
	briefHash := sha256Content(brief)
	promptHash := sha256Content([]byte(prompt))

	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "disk-test",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		CharterSHA256:   charterHash,
		BriefSHA256:     briefHash,
		PromptSHA256:    promptHash,
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

	if err := VerifyEnvelopeIntegrity(tmp); err != nil {
		t.Fatalf("full integrity verification failed: %v", err)
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
//  1. BuildPrompt + Envelope (identity, hashes, delivery mode)
//  2. PersistLaunchFiles (charter + brief + prompt + envelope)
//  3. VerifyEnvelopeIntegrity (all hashes match, meta consistent)
//  4. BuildLaunchArgs (Pi harness with model/effort/prompt)
//  5. Prompt contains exact terminal report command with --key <task-id>
//  6. All files survive a verify round-trip
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
	if env.CharterSHA256 == "" || env.BriefSHA256 == "" || env.PromptSHA256 == "" {
		t.Error("all hashes must be non-empty")
	}

	// Step 2: Persist all files.
	charter := DefaultCharter(input.TaskID, input.TaskKind, input.DeliveryMode)
	if err := PersistLaunchFiles(worktree, charter, briefContent, env, promptText); err != nil {
		t.Fatal(err)
	}

	// Step 3: Verify integrity of all persisted files.
	if err := VerifyEnvelopeIntegrity(worktree); err != nil {
		t.Fatalf("integrity verification failed: %v", err)
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

	// Step 6: Verify round-trip integrity - read envelope back and check hashes.
	readEnv, err := ReadEnvelope(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if readEnv.PromptSHA256 != sha256Content([]byte(promptText)) {
		t.Error("envelope prompt hash does not match actual prompt on disk")
	}
	if readEnv.CharterSHA256 != sha256Content([]byte(charter)) {
		t.Error("envelope charter hash does not match actual charter on disk")
	}
	if readEnv.BriefSHA256 != sha256Content(briefContent) {
		t.Error("envelope brief hash does not match actual brief on disk")
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
	if !strings.Contains(prompt, "### gh-axi") {
		t.Error("prompt must contain ### gh-axi section")
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

	// Verify the prompt hash is non-empty.
	if env.PromptSHA256 == "" {
		t.Error("prompt SHA256 must be set")
	}
}
