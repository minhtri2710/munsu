//go:build integration

package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

// =============================================================================
// Charter contract tests
// =============================================================================

func TestDefaultCharter_ContainsVersion(t *testing.T) {
	charter := DefaultCharter("test-id", "ship", "direct-PR")
	if !strings.Contains(charter, CharterVersion) {
		t.Errorf("DefaultCharter must contain version %q", CharterVersion)
	}
}

func TestDefaultCharter_SoldierAuthorityOnly(t *testing.T) {
	charter := DefaultCharter("auth-test", "ship", "direct-PR")
	if !strings.Contains(charter, "Soldier authority only") {
		t.Error("DefaultCharter must declare soldier-only authority")
	}
	if !strings.Contains(charter, "Forbidden") {
		t.Error("DefaultCharter must have forbidden actions section")
	}
}

func TestDefaultCharter_ForbiddenActions(t *testing.T) {
	charter := DefaultCharter("forbidden-test", "ship", "direct-PR")
	checks := []string{
		"Never push to the default branch",
		"Never merge",
		"Never modify files outside",
		"Never claim",
		"Never spawn",
		"Never invent work beyond",
	}
	for _, check := range checks {
		if !strings.Contains(charter, check) {
			t.Errorf("DefaultCharter must forbid %q", check)
		}
	}
}

func TestDefaultCharter_ReportCommand(t *testing.T) {
	charter := DefaultCharter("report-test", "ship", "direct-PR")
	if !strings.Contains(charter, "munsu report") {
		t.Error("DefaultCharter must document munsu report command")
	}
	if !strings.Contains(charter, "terminal") {
		t.Error("DefaultCharter must mention terminal reporting")
	}
}

func TestDefaultCharter_DurableFiles(t *testing.T) {
	charter := DefaultCharter("durable-test", "ship", "direct-PR")
	if !strings.Contains(charter, ".soldier-charter.md") {
		t.Error("DefaultCharter must mention .soldier-charter.md")
	}
	if !strings.Contains(charter, ".soldier-brief.md") {
		t.Error("DefaultCharter must mention .soldier-brief.md")
	}
	if !strings.Contains(charter, ".soldier-envelope.json") {
		t.Error("DefaultCharter must mention .soldier-envelope.json")
	}
}

func TestDefaultCharter_AllowedActions(t *testing.T) {
	charter := DefaultCharter("allowed-test", "ship", "direct-PR")
	if !strings.Contains(charter, "AGENTS.md") {
		t.Error("DefaultCharter must mention reading AGENTS.md before edits")
	}
}

func TestDefaultCharter_DeliveryModeAdapts(t *testing.T) {
	charter := DefaultCharter("mode-test", "ship", "direct-PR")
	if !strings.Contains(charter, "direct-PR") {
		t.Error("DefaultCharter must include the delivery mode")
	}
}

// =============================================================================
// Launch envelope tests
// =============================================================================

func TestLaunchEnvelope_WriteAndRead(t *testing.T) {
	tmp := t.TempDir()
	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "test-task",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		Repository:      "test-repo",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
	}
	if err := WriteEnvelope(tmp, env); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, EnvelopeName))
	if err != nil {
		t.Fatal(err)
	}
	var got LaunchEnvelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.TaskID != "test-task" {
		t.Errorf("TaskID = %q, want %q", got.TaskID, "test-task")
	}
	if got.EnvelopeVersion != EnvelopeVersion {
		t.Errorf("EnvelopeVersion = %q, want %q", got.EnvelopeVersion, EnvelopeVersion)
	}
	if got.ParentCaptainID != "captain-1" {
		t.Errorf("ParentCaptainID = %q, want %q", got.ParentCaptainID, "captain-1")
	}
}

// =============================================================================
// Skill manifest tests
// =============================================================================

func TestCollectSkills_RequiredSkillsSelected(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "gh-axi", Role: "soldier"},
		{Name: "qmd", Role: "soldier"},
	}
	required, _, diags := CollectSkills(catalog, []string{"gh-axi", "qmd"}, nil)
	if len(diags) > 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
	if len(required) != 2 {
		t.Fatalf("expected 2 required skills, got %d", len(required))
	}
	if !required[0].Applicable {
		t.Errorf("gh-axi should be applicable")
	}
	if !required[1].Applicable {
		t.Errorf("qmd should be applicable")
	}
}

func TestCollectSkills_DeniedByDenylist(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "munsu-ops", Role: "soldier"}, // explicitly denied regardless of role
	}
	required, _, diags := CollectSkills(catalog, []string{"munsu-ops"}, nil)
	if len(diags) == 0 {
		t.Error("expected diagnostic about munsu-ops being denied")
	}
	if len(required) != 1 || required[0].Applicable {
		t.Error("munsu-ops should be non-applicable (denied by denylist)")
	}
}

func TestCollectSkills_DeniedByRole(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "bootstrap-diagnostics", Role: "general"},
		{Name: "harness-adapters", Role: "captain"},
	}
	required, _, diags := CollectSkills(catalog, []string{"bootstrap-diagnostics", "harness-adapters"}, nil)
	if len(diags) == 0 {
		t.Error("expected diagnostics about denied skills")
	}
	for _, s := range required {
		if s.Applicable {
			t.Errorf("skill %q should be non-applicable (role %q)", s.Name, s.Role)
		}
	}
}

func TestCollectSkills_OptionalDeniedByDenylist(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "captain-provisioning", Role: "captain"},
	}
	_, optional, diags := CollectSkills(catalog, nil, []string{"captain-provisioning"})
	if len(diags) == 0 {
		t.Error("expected diagnostic about denied optional skill")
	}
	if len(optional) != 0 {
		t.Error("denied skill should not appear in optional list")
	}
}

func TestCollectSkills_UnknownSkill(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "known-skill", Role: "soldier"},
	}
	required, _, diags := CollectSkills(catalog, []string{"unknown-skill"}, nil)
	if len(diags) == 0 {
		t.Error("expected diagnostic about unknown required skill")
	}
	if len(required) != 1 || required[0].Applicable {
		t.Error("unknown skill should be non-applicable")
	}
}

func TestCollectSkills_Dedup(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "gh-axi", Role: "soldier"},
	}
	required, _, _ := CollectSkills(catalog, []string{"gh-axi", "gh-axi"}, nil)
	if len(required) != 1 {
		t.Errorf("expected 1 required skill after dedup, got %d", len(required))
	}
}

func TestCollectSkills_RequiredAndOptional(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "gh-axi", Role: "soldier"},
		{Name: "qmd", Role: "soldier"},
	}
	required, optional, diags := CollectSkills(catalog, []string{"gh-axi"}, []string{"qmd"})
	if len(diags) > 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
	if len(required) != 1 || required[0].Name != "gh-axi" {
		t.Error("gh-axi should be in required skills")
	}
	if len(optional) != 1 || optional[0].Name != "qmd" {
		t.Error("qmd should be in optional skills")
	}
}

// =============================================================================
// SkillAuthorityClass tests
// =============================================================================

func TestSkillAuthorityClass_Soldier(t *testing.T) {
	if got := SkillAuthorityClass("gh-axi", "soldier"); got != "soldier" {
		t.Errorf("gh-axi soldier = %q, want 'soldier'", got)
	}
	if got := SkillAuthorityClass("any-skill", "any"); got != "soldier" {
		t.Errorf("any-skill any role = %q, want 'soldier'", got)
	}
}

func TestSkillAuthorityClass_DeniedByList(t *testing.T) {
	if got := SkillAuthorityClass("munsu-ops", "soldier"); got != "captain" {
		t.Errorf("munsu-ops = %q, want 'captain' (denied)", got)
	}
	if got := SkillAuthorityClass("captain-provisioning", ""); got != "captain" {
		t.Errorf("captain-provisioning = %q, want 'captain' (denied)", got)
	}
	if got := SkillAuthorityClass("no-mistakes", ""); got != "captain" {
		t.Errorf("no-mistakes = %q, want 'captain' (denied)", got)
	}
	if got := SkillAuthorityClass("harness-adapters", ""); got != "captain" {
		t.Errorf("harness-adapters = %q, want 'captain' (denied)", got)
	}
}

func TestSkillAuthorityClass_CaptainRole(t *testing.T) {
	if got := SkillAuthorityClass("captain-skill", "captain"); got != "captain" {
		t.Errorf("captain role = %q, want 'captain'", got)
	}
	if got := SkillAuthorityClass("captain-only", "captain-only"); got != "captain" {
		t.Errorf("captain-only role = %q, want 'captain'", got)
	}
}

func TestSkillAuthorityClass_GeneralRole(t *testing.T) {
	if got := SkillAuthorityClass("general-skill", "general"); got != "general" {
		t.Errorf("general role = %q, want 'general'", got)
	}
	if got := SkillAuthorityClass("general-only", "general-only"); got != "general" {
		t.Errorf("general-only role = %q, want 'general'", got)
	}
}

// =============================================================================
// BuildLaunchPrompt tests
// =============================================================================

func TestBuildLaunchPrompt_BasicContent(t *testing.T) {
	brief := []byte("# Task brief: test-task\n\nDo the work.\n")
	input := LaunchPromptInput{
		TaskID:          "test-task",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		Repository:      "test-repo",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		WorktreePath:    t.TempDir(),
		HomeDir:         "/tmp/home",
		BriefContent:    brief,
		HarnessName:     "pi",
	}
	prompt, env, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, CharterVersion) {
		t.Error("prompt must contain charter version")
	}
	if !strings.Contains(prompt, "Do the work.") {
		t.Error("prompt must contain brief content, not just a path reference")
	}
	if !strings.Contains(prompt, "munsu report done") {
		t.Error("prompt must contain terminal report command")
	}
	if env.TaskID != "test-task" {
		t.Errorf("envelope TaskID = %q, want %q", env.TaskID, "test-task")
	}
}

func TestBuildLaunchPrompt_EmptyInputFails(t *testing.T) {
	_, _, err := BuildLaunchPrompt(LaunchPromptInput{})
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

func TestBuildLaunchPrompt_MissingBriefFails(t *testing.T) {
	_, _, err := BuildLaunchPrompt(LaunchPromptInput{
		TaskID:       "test",
		WorktreePath: t.TempDir(),
		HomeDir:      t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for missing brief, got nil")
	}
}

func TestBuildLaunchPrompt_DefaultKindAndMode(t *testing.T) {
	brief := []byte("# Task\n\nContent.\n")
	_, env, err := BuildLaunchPrompt(LaunchPromptInput{
		TaskID:          "test",
		WorktreePath:    t.TempDir(),
		HomeDir:         t.TempDir(),
		BriefContent:    brief,
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		DeliveryMode:    "direct-PR",
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.TaskKind != "ship" {
		t.Errorf("default TaskKind = %q, want 'ship'", env.TaskKind)
	}
	if env.DeliveryMode != "direct-PR" {
		t.Errorf("DeliveryMode = %q, want 'direct-PR'", env.DeliveryMode)
	}
}

// =============================================================================
// FailClosedDuringLaunch tests
// =============================================================================

func TestFailClosedDuringLaunch_Passes(t *testing.T) {
	tmp := t.TempDir()
	err := FailClosedDuringLaunch(LaunchPromptInput{
		TaskID:          "test",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		WorktreePath:    tmp,
		BriefContent:    []byte("brief"),
		DeliveryMode:    "direct-PR",
	})
	if err != nil {
		t.Errorf("expected no error for valid input, got: %v", err)
	}
}

func TestFailClosedDuringLaunch_EmptyTaskID(t *testing.T) {
	err := FailClosedDuringLaunch(LaunchPromptInput{
		TaskID:          "",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		WorktreePath:    t.TempDir(),
		BriefContent:    []byte("brief"),
		DeliveryMode:    "direct-PR",
	})
	if err == nil {
		t.Error("expected error for empty task ID")
	}
}

func TestFailClosedDuringLaunch_EmptyParentHome(t *testing.T) {
	err := FailClosedDuringLaunch(LaunchPromptInput{
		TaskID:          "test",
		ParentCaptainID: "captain-1",
		ParentHome:      "",
		WorktreePath:    t.TempDir(),
		BriefContent:    []byte("brief"),
		DeliveryMode:    "direct-PR",
	})
	if err == nil {
		t.Error("expected error for empty parent home")
	}
}

func TestFailClosedDuringLaunch_MissingWorktree(t *testing.T) {
	err := FailClosedDuringLaunch(LaunchPromptInput{
		TaskID:          "test",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		WorktreePath:    "/nonexistent/path",
		BriefContent:    []byte("brief"),
		DeliveryMode:    "direct-PR",
	})
	if err == nil {
		t.Error("expected error for missing worktree path")
	}
}

func TestFailClosedDuringLaunch_EmptyBrief(t *testing.T) {
	err := FailClosedDuringLaunch(LaunchPromptInput{
		TaskID:          "test",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		WorktreePath:    t.TempDir(),
		BriefContent:    []byte(""),
		DeliveryMode:    "direct-PR",
	})
	if err == nil {
		t.Error("expected error for empty brief content")
	}
}

func TestFailClosedDuringLaunch_RequiredSkillMissing(t *testing.T) {
	tmp := t.TempDir()
	err := FailClosedDuringLaunch(LaunchPromptInput{
		TaskID:          "test",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		WorktreePath:    tmp,
		BriefContent:    []byte("brief"),
		DeliveryMode:    "direct-PR",
		RequiredSkills: []SkillEntry{
			{Name: "missing-skill", Applicable: true, SourcePath: "/nonexistent/skill.md"},
		},
	})
	if err == nil {
		t.Error("expected error for missing required skill source")
	}
}

// =============================================================================
// PersistLaunchFiles tests
// =============================================================================

func TestPersistLaunchFiles_WritesAllFiles(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("persist-test", "ship", "direct-PR")
	brief := []byte("# Task brief\n\nContent.\n")
	prompt := "complete prompt text"
	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "persist-test",
	}
	if err := PersistLaunchFiles(tmp, charter, brief, env, prompt); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{CharterName, BriefName, PromptName, EnvelopeName} {
		path := filepath.Join(tmp, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
}

// =============================================================================
// BuildLaunchArgs contract tests
// =============================================================================

func TestBuildLaunchArgs_Pi(t *testing.T) {
	bin, args, err := BuildLaunchArgs("/tmp/home", "pi", "gpt-5", "high", "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	if bin != "pi" {
		t.Errorf("bin = %q, want 'pi'", bin)
	}
	if len(args) == 0 {
		t.Fatal("expected at least 1 arg")
	}
	// Pi uses --model and --thinking flags with values.
	foundModel, foundEffort := false, false
	for i, arg := range args {
		if arg == "--model" && i+1 < len(args) && args[i+1] == "gpt-5" {
			foundModel = true
		}
		if arg == "--thinking" && i+1 < len(args) && args[i+1] == "high" {
			foundEffort = true
		}
	}
	if !foundModel {
		t.Error("pi args must include --model gpt-5")
	}
	if !foundEffort {
		t.Error("pi args must include --thinking high")
	}
	lastArg := args[len(args)-1]
	if lastArg != "test prompt" {
		t.Errorf("last arg = %q, want 'test prompt'", lastArg)
	}
}

func TestBuildLaunchArgs_Codex_FailsWithoutPromptContract(t *testing.T) {
	_, _, err := BuildLaunchArgs("/tmp/home", "codex", "gpt-5", "80", "test prompt")
	if err == nil {
		t.Error("expected error for codex (no prompt-arg contract)")
	}
}

func TestBuildLaunchArgs_Claude_FailsWithoutPromptContract(t *testing.T) {
	_, _, err := BuildLaunchArgs("/tmp/home", "claude", "sonnet-4", "", "test prompt")
	if err == nil {
		t.Error("expected error for claude (no prompt-arg contract)")
	}
}

func TestBuildLaunchArgs_Agy_FailsWithoutPromptContract(t *testing.T) {
	_, _, err := BuildLaunchArgs("/tmp/home", "agy", "", "", "test prompt")
	if err == nil {
		t.Error("expected error for agy (no prompt-arg contract)")
	}
}

func TestBuildLaunchArgs_UnknownHarness(t *testing.T) {
	_, _, err := BuildLaunchArgs("/tmp/home", "unknown-harness", "", "", "test prompt")
	if err == nil {
		t.Error("expected error for unknown harness")
	}
}

func TestBuildLaunchArgs_PiUsesTemplateDefaults(t *testing.T) {
	// When model/effort are empty, BuildLaunchArgs should use Template defaults.
	bin, args, err := BuildLaunchArgs("/tmp/home", "pi", "", "", "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	if bin != "pi" {
		t.Errorf("bin = %q, want 'pi'", bin)
	}
	// Last arg must be the prompt.
	lastArg := args[len(args)-1]
	if lastArg != "test prompt" {
		t.Errorf("last arg = %q, want 'test prompt'", lastArg)
	}
}

// =============================================================================
// Terminal report contract tests
// =============================================================================

func TestTerminalReport_ExactCommand(t *testing.T) {
	reminder := terminalReportReminder("test-task-42", "ship", "captain-alpha")
	// Must contain exact command without literal brackets.
	if !strings.Contains(reminder, `munsu report done "PR {url}" --key test-task-42`) {
		t.Errorf("terminal report must contain exact command, got:\n%s", reminder)
	}
	// Must NOT contain [--key ...] with literal brackets.
	if strings.Contains(reminder, "[--key") {
		t.Error("terminal report must NOT contain literal brackets around --key")
	}
	// Must mention the --key is required.
	if !strings.Contains(reminder, "REQUIRED") {
		t.Error("terminal report must state --key is REQUIRED")
	}
	// Must reference parent captain.
	if !strings.Contains(reminder, "captain-alpha") {
		t.Error("terminal report must contain parent captain ID")
	}
}

func TestTerminalReport_ExactBytes(t *testing.T) {
	// Verify exact byte output for the terminal report command.
	note := terminalReportReminder("x-task", "ship", "x-captain")
	expected := `munsu report done "PR {url}" --key x-task`
	if !strings.Contains(note, expected) {
		t.Errorf("terminal report must contain exact bytes:\nwant: %s\ngot:  %s", expected, note)
	}
}

func TestTerminalReport_ScoutUsesFindingsSummary(t *testing.T) {
	note := terminalReportReminder("scout-task", "scout", "general")
	if !strings.Contains(note, `munsu report done "summary of findings location" --key scout-task`) {
		t.Errorf("scout terminal report must use findings summary, got:\n%s", note)
	}
	if strings.Contains(note, "scout-taskgeneral") || strings.Contains(note, "PR {url}") {
		t.Errorf("scout terminal report contains ship-only or concatenated content:\n%s", note)
	}
}

func TestBuildLaunchPrompt_ScoutContainsFindingsReportCommand(t *testing.T) {
	prompt, _, err := BuildLaunchPrompt(LaunchPromptInput{
		TaskID:                 "scout-report-test",
		TaskKind:               "scout",
		ParentCaptainID:        "captain-alpha",
		ParentHome:             "/tmp/parent",
		WorktreePath:           t.TempDir(),
		HomeDir:                "/tmp/home",
		BriefContent:           []byte("# Scout brief\n\nInvestigate.\n"),
		HarnessName:            "pi",
		ScoutScope:             "investigate scope",
		ScoutRuntimeBudgetSecs: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, `munsu report done "summary of findings location" --key scout-report-test`) {
		t.Errorf("scout prompt must contain findings report command, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "PR {url}") || strings.Contains(prompt, "scout-report-testcaptain-alpha") {
		t.Errorf("scout prompt contains ship-only or concatenated report content:\n%s", prompt)
	}
}

func TestBuildLaunchPrompt_ContainsExactReportCommand(t *testing.T) {
	brief := []byte("# Task brief: report-test\n\nDo work.\n")
	prompt, env, err := BuildLaunchPrompt(LaunchPromptInput{
		TaskID:          "report-test",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-alpha",
		ParentHome:      "/tmp/parent",
		WorktreePath:    t.TempDir(),
		HomeDir:         "/tmp/home",
		BriefContent:    brief,
		HarnessName:     "pi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, `munsu report done "PR {url}" --key report-test`) {
		t.Error("prompt must contain exact 'munsu report done \"PR {url}\" --key report-test'")
	}
	if strings.Contains(prompt, "report-testcaptain-alpha") {
		t.Error("prompt must not concatenate the parent captain ID into --key")
	}
	if env.ParentCaptainID != "captain-alpha" {
		t.Errorf("env ParentCaptainID = %q, want 'captain-alpha'", env.ParentCaptainID)
	}
}

// =============================================================================
// Recovery determinism test
// =============================================================================

func TestBuildLaunchPrompt_RecoveryDeterminism(t *testing.T) {
	brief := []byte("# Task\n\nDeterministic content.\n")
	wt := t.TempDir()
	input := LaunchPromptInput{
		TaskID:          "recovery-test",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-1",
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
		t.Error("repeated BuildLaunchPrompt with same input should produce identical prompt")
	}
}

// =============================================================================
// buildSkillInstructions with strict read/hash verification
// =============================================================================

func TestBuildSkillInstructions_RequiredSkillReadError(t *testing.T) {
	tmp := t.TempDir()
	skillPath := filepath.Join(tmp, "nonexistent.md")
	_, err := buildSkillInstructions(
		[]SkillEntry{{Name: "missing-skill", Applicable: true, SourcePath: skillPath}},
		nil, tmp,
	)
	if err == nil {
		t.Error("expected error for missing required skill source at build time")
	}
	if err != nil && !strings.Contains(err.Error(), "missing-skill") {
		t.Errorf("error should mention skill name, got: %v", err)
	}
}

func TestBuildSkillInstructions_RequiredSkillHashFail(t *testing.T) {
	tmp := t.TempDir()
	skillPath := filepath.Join(tmp, "skill.md")
	os.WriteFile(skillPath, []byte("content"), 0644)
	_, err := buildSkillInstructions(
		[]SkillEntry{{Name: "my-skill", Applicable: true, SourcePath: skillPath, SourceSHA256: "badhash"}},
		nil, tmp,
	)
	if err == nil {
		t.Error("expected error for SHA-256 mismatch at build time")
	}
}

func TestBuildSkillInstructions_RequiredSkillSuccess(t *testing.T) {
	tmp := t.TempDir()
	skillPath := filepath.Join(tmp, "good-skill.md")
	content := []byte("# Good Skill\n\nInstructions.\n")
	os.WriteFile(skillPath, content, 0644)
	result, err := buildSkillInstructions(
		[]SkillEntry{{
			Name: "good-skill", Applicable: true, SourcePath: skillPath,
			SourceSHA256: sha256Content(content),
		}},
		nil, tmp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Good Skill") {
		t.Error("result should contain skill name")
	}
	if !strings.Contains(result, "Instructions.") {
		t.Error("result should contain inlined skill content")
	}
}

// =============================================================================
// Default argument capture tests (argv verification)
// =============================================================================

func TestBuildLaunchArgs_PiArgvCapture(t *testing.T) {
	// Verify exact argv for Pi harness with model and effort.
	_, args, err := BuildLaunchArgs("/tmp/home", "pi", "my-model", "max", "do the work")
	if err != nil {
		t.Fatal(err)
	}
	// Expected argv: --model my-model --thinking max do the work
	expectedParts := []string{"--model", "my-model", "--thinking", "max", "do the work"}
	if len(args) != len(expectedParts) {
		t.Fatalf("expected %d args, got %d: %v", len(expectedParts), len(args), args)
	}
	for i, expected := range expectedParts {
		if args[i] != expected {
			t.Errorf("arg[%d] = %q, want %q", i, args[i], expected)
		}
	}
}

func TestBuildLaunchArgs_PiDefaultModel(t *testing.T) {
	// When model is empty, Pi template defines no DefaultModel, so only thinking+prompt.
	_, args, err := BuildLaunchArgs("/tmp/home", "pi", "", "medium", "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	foundModel := false
	for i, arg := range args {
		if arg == "--model" {
			foundModel = true
			// Should have a model value after it.
			if i+1 >= len(args) || args[i+1] == "" {
				t.Error("--model must be followed by a model value")
			}
		}
	}
	// Pi doesn't have DefaultModel in Template, but flag should not appear without value.
	if foundModel {
		// Pi Template has ModelFlag="--model" but no DefaultModel.
		// With empty model, it should not emit --model flag.
		t.Error("with empty model, pi should not emit --model flag (no DefaultModel)")
	}
}

// =============================================================================
// harness.GetAdapter introspection for prompt-arg support
// =============================================================================

func TestHarnessPromptArgSupported(t *testing.T) {
	for _, name := range harness.KnownHarnesses {
		a, ok := harness.GetAdapter(name)
		if !ok {
			continue
		}
		// Only Pi currently has verified prompt-arg support.
		if name == "pi" && (!a.CaptainLaunch.Supported || !a.CaptainLaunch.PromptArg) {
			t.Error("pi must have verified prompt-arg contract for soldier launch")
		}
	}
}
