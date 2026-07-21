package soldier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// Charter contract tests
// =============================================================================

// TestDefaultCharter_ContainsVersion verifies the charter has its canonical version.
func TestDefaultCharter_ContainsVersion(t *testing.T) {
	charter := DefaultCharter("test-id", "ship", "direct-PR")
	if !strings.Contains(charter, CharterVersion) {
		t.Errorf("DefaultCharter must contain version %q", CharterVersion)
	}
}

// TestDefaultCharter_SoldierAuthorityOnly verifies the charter declares soldier-only
// authority and never captain or general authority.
func TestDefaultCharter_SoldierAuthorityOnly(t *testing.T) {
	charter := DefaultCharter("auth-test", "ship", "direct-PR")

	// Must declare soldier authority.
	if !strings.Contains(charter, "Soldier authority only") {
		t.Error("DefaultCharter must declare soldier-only authority")
	}

	// Must forbid claiming Captain or General authority.
	if !strings.Contains(charter, "Never claim") && !strings.Contains(charter, "Forbidden") {
		t.Error("DefaultCharter must have forbidden actions section")
	}

	// Must NOT contain captain-level authority like "spawn soldiers" or "merge PRs".
	if strings.Contains(charter, "Merge PR") || strings.Contains(charter, "Spawning Soldiers") {
		t.Error("DefaultCharter must NOT contain captain-level authority language")
	}
}

// TestDefaultCharter_ForbiddenActions verifies the charter contains the explicit
// no-merge rule and other soldier-specific forbidden actions.
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

// TestDefaultCharter_ReportCommand verifies the charter documents the munsu report
// command as the primary terminal reporting mechanism.
func TestDefaultCharter_ReportCommand(t *testing.T) {
	charter := DefaultCharter("report-test", "ship", "direct-PR")

	if !strings.Contains(charter, "munsu report") {
		t.Error("DefaultCharter must document munsu report command")
	}
	if !strings.Contains(charter, "terminal") {
		t.Error("DefaultCharter must mention terminal reporting")
	}
}

// TestDefaultCharter_DurableFiles verifies the charter documents the durable files.
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

// TestDefaultCharter_AllowedActions verifies the charter includes reading AGENTS.md.
func TestDefaultCharter_AllowedActions(t *testing.T) {
	charter := DefaultCharter("allowed-test", "ship", "direct-PR")
	if !strings.Contains(charter, "AGENTS.md") {
		t.Error("DefaultCharter must mention reading AGENTS.md before edits")
	}
}

// TestDefaultCharter_DeliveryModeAdapts checks mode-specific content (direct-PR).
func TestDefaultCharter_DeliveryModeAdapts(t *testing.T) {
	charter := DefaultCharter("mode-test", "ship", "direct-PR")
	if !strings.Contains(charter, "direct-PR") {
		t.Error("DefaultCharter must include the delivery mode")
	}
}

// =============================================================================
// Launch envelope tests
// =============================================================================

// TestLaunchEnvelope_WriteAndRead verifies a round-trip write/read of the envelope.
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
		CharterSHA256:   "abc123",
		BriefSHA256:     "def456",
		PromptSHA256:    "ghi789",
	}

	if err := WriteEnvelope(tmp, env); err != nil {
		t.Fatal(err)
	}

	got, err := ReadEnvelope(tmp)
	if err != nil {
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

// TestLaunchEnvelope_IntegrityVerify checks integrity verification passes.
func TestLaunchEnvelope_IntegrityVerify(t *testing.T) {
	tmp := t.TempDir()

	charter := DefaultCharter("verify-test", "ship", "direct-PR")
	brief := "# Task brief: verify-test\n\nSome content.\n"

	// Write files.
	if err := os.WriteFile(filepath.Join(tmp, CharterName), []byte(charter), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, BriefName), []byte(brief), 0644); err != nil {
		t.Fatal(err)
	}

	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "verify-test",
		TaskKind:        "ship",
		CharterSHA256:   sha256Content([]byte(charter)),
		BriefSHA256:     sha256Content([]byte(brief)),
	}
	if err := WriteEnvelope(tmp, env); err != nil {
		t.Fatal(err)
	}

	if err := VerifyEnvelopeIntegrity(tmp); err != nil {
		t.Errorf("integrity verification failed: %v", err)
	}
}

// TestLaunchEnvelope_IntegrityFail checks integrity fails on mismatch.
func TestLaunchEnvelope_IntegrityFail(t *testing.T) {
	tmp := t.TempDir()

	charter := DefaultCharter("fail-test", "ship", "direct-PR")
	if err := os.WriteFile(filepath.Join(tmp, CharterName), []byte(charter), 0644); err != nil {
		t.Fatal(err)
	}

	// Write envelope with WRONG hash.
	env := &LaunchEnvelope{
		CharterSHA256: "deadbeef",
	}
	if err := WriteEnvelope(tmp, env); err != nil {
		t.Fatal(err)
	}

	if err := VerifyEnvelopeIntegrity(tmp); err == nil {
		t.Error("expected integrity verification to fail, got nil")
	}
}

// =============================================================================
// Skill manifest tests
// =============================================================================

func TestCollectSkills_RequiredSkillsSelected(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "munsu-ops", Role: "soldier", SourcePath: "/tmp/munsu-ops", Version: "1.0"},
		{Name: "gh-axi", Role: "", SourcePath: "/tmp/gh-axi"},
		{Name: "captain-provisioning", Role: "captain", SourcePath: "/tmp/captain"},
	}

	required, _, diags := CollectSkills(catalog, []string{"munsu-ops", "gh-axi"}, nil)

	if len(diags) > 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
	if len(required) != 2 {
		t.Fatalf("expected 2 required skills, got %d", len(required))
	}
	if !required[0].Applicable {
		t.Errorf("munsu-ops should be applicable")
	}
	if !required[1].Applicable {
		t.Errorf("gh-axi should be applicable")
	}
}

func TestCollectSkills_CaptainOnlySkillExcluded(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "captain-provisioning", Role: "captain", SourcePath: "/tmp/captain"},
	}

	required, optional, diags := CollectSkills(catalog, []string{"captain-provisioning"}, nil)

	if len(diags) == 0 {
		t.Error("expected diagnostic about captain-only skill being non-applicable")
	}
	if len(required) != 1 || required[0].Applicable {
		t.Error("captain-only skill should be marked non-applicable in required list")
	}
	if len(optional) != 0 {
		t.Error("captain-only skill should not appear in optional list")
	}
}

func TestCollectSkills_GeneralOnlySkillExcluded(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "fleet-ops", Role: "general", SourcePath: "/tmp/fleet"},
	}

	required, optional, diags := CollectSkills(catalog, nil, []string{"fleet-ops"})

	if len(diags) == 0 {
		t.Error("expected diagnostic about general-only skill being omitted")
	}
	if len(required) != 0 {
		t.Error("general-only skill should not appear in required list")
	}
	if len(optional) != 0 {
		t.Error("general-only skill should not appear in optional list")
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
	if len(required) != 1 {
		t.Fatalf("expected 1 required skill (unknown), got %d", len(required))
	}
	if required[0].Applicable {
		t.Error("unknown skill should be non-applicable")
	}
}

func TestCollectSkills_OptionalSkillOmittedDiagnostic(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "known-skill", Role: "soldier"},
	}

	_, optional, diags := CollectSkills(catalog, nil, []string{"unknown-optional"})

	if len(diags) == 0 {
		t.Error("expected diagnostic about unknown optional skill")
	}
	if len(optional) != 0 {
		t.Error("unknown optional skill should not appear in optional list")
	}
}

func TestCollectSkills_Dedup(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "munsu-ops", Role: "soldier"},
	}

	required, _, _ := CollectSkills(catalog, []string{"munsu-ops", "munsu-ops"}, nil)

	if len(required) != 1 {
		t.Errorf("expected 1 required skill after dedup, got %d", len(required))
	}
}

func TestCollectSkills_OptionalSkillsCollected(t *testing.T) {
	catalog := []SkillEntry{
		{Name: "tasks-axi", Role: "soldier", SourcePath: "/tmp/tasks-axi", Version: "2.0"},
		{Name: "gh-axi", Role: "soldier", SourcePath: "/tmp/gh-axi"},
	}

	required, optional, diags := CollectSkills(catalog, []string{"tasks-axi"}, []string{"gh-axi"})

	if len(diags) > 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
	if len(required) != 1 || required[0].Name != "tasks-axi" {
		t.Error("tasks-axi should be in required skills")
	}
	if len(optional) != 1 || optional[0].Name != "gh-axi" {
		t.Error("gh-axi should be in optional skills")
	}
}

// =============================================================================
// BuildLaunchPrompt tests
// =============================================================================

func TestBuildLaunchPrompt_BasicContent(t *testing.T) {
	brief := "# Task brief: test-task\n\nDo the work.\n"
	input := LaunchPromptInput{
		TaskID:          "test-task",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		Repository:      "test-repo",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		WorktreePath:    t.TempDir(),
		HomeDir:         "/tmp/home",
		BriefContent:    []byte(brief),
		HarnessName:     "pi",
	}

	prompt, env, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}

	// Prompt must contain charter version.
	if !strings.Contains(prompt, CharterVersion) {
		t.Error("prompt must contain charter version")
	}

	// Prompt must contain brief content.
	if !strings.Contains(prompt, "Do the work.") {
		t.Error("prompt must contain brief content, not just a path reference")
	}

	// Prompt must contain terminal report requirement.
	if !strings.Contains(prompt, "munsu report done") {
		t.Error("prompt must contain terminal report command")
	}

	// Envelope must be populated.
	if env.TaskID != "test-task" {
		t.Errorf("envelope TaskID = %q, want %q", env.TaskID, "test-task")
	}
	if env.ParentCaptainID != "captain-1" {
		t.Errorf("envelope ParentCaptainID = %q", env.ParentCaptainID)
	}
	if env.CharterSHA256 == "" {
		t.Error("envelope must have charter SHA-256")
	}
	if env.BriefSHA256 == "" {
		t.Error("envelope must have brief SHA-256")
	}
	if env.PromptSHA256 == "" {
		t.Error("envelope must have prompt SHA-256")
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
	brief := "# Task\n\nContent.\n"
	prompt, env, err := BuildLaunchPrompt(LaunchPromptInput{
		TaskID:          "test",
		WorktreePath:    t.TempDir(),
		HomeDir:         t.TempDir(),
		BriefContent:    []byte(brief),
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
	if !strings.Contains(prompt, "direct-PR") {
		t.Error("prompt must contain delivery mode")
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
	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "persist-test",
		CharterSHA256:   sha256Content([]byte(charter)),
		BriefSHA256:     sha256Content(brief),
	}

	if err := PersistLaunchFiles(tmp, charter, brief, env); err != nil {
		t.Fatal(err)
	}

	// Verify all three files exist.
	for _, name := range []string{CharterName, BriefName, EnvelopeName} {
		path := filepath.Join(tmp, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
}

func TestPersistLaunchFiles_EnvelopeHashesMatch(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("hash-test", "ship", "direct-PR")
	brief := []byte("# Task brief\n\nVerify hashes.\n")

	charterHash := sha256Content([]byte(charter))
	briefHash := sha256Content(brief)

	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "hash-test",
		CharterSHA256:   charterHash,
		BriefSHA256:     briefHash,
	}

	if err := PersistLaunchFiles(tmp, charter, brief, env); err != nil {
		t.Fatal(err)
	}

	// Verify envelope hashes match written files.
	readEnv, err := ReadEnvelope(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if readEnv.CharterSHA256 != charterHash {
		t.Errorf("envelope charter hash = %q, want %q", readEnv.CharterSHA256, charterHash)
	}
	if readEnv.BriefSHA256 != briefHash {
		t.Errorf("envelope brief hash = %q, want %q", readEnv.BriefSHA256, briefHash)
	}
}

// =============================================================================
// WriteEnvelope / ReadEnvelope edge cases
// =============================================================================

func TestWriteEnvelope_NilEnvFails(t *testing.T) {
	err := WriteEnvelope(t.TempDir(), nil)
	if err == nil {
		t.Error("expected error for nil envelope")
	}
}

func TestReadEnvelope_MissingFile(t *testing.T) {
	_, err := ReadEnvelope(t.TempDir())
	if err == nil {
		t.Error("expected error for missing envelope file")
	}
}

// =============================================================================
// BuildLaunchArgs contract tests (Pi, Codex, Claude, Agy)
// =============================================================================

func TestBuildLaunchArgs_Pi(t *testing.T) {
	bin, args, err := BuildLaunchArgs("/tmp/home", "pi", "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	if bin != "pi" {
		t.Errorf("bin = %q, want 'pi'", bin)
	}
	if len(args) == 0 {
		t.Fatal("expected at least 1 arg")
	}
	// The last arg should be the prompt.
	lastArg := args[len(args)-1]
	if lastArg != "test prompt" {
		t.Errorf("last arg = %q, want 'test prompt'", lastArg)
	}
}

func TestBuildLaunchArgs_Codex(t *testing.T) {
	bin, args, err := BuildLaunchArgs("/tmp/home", "codex", "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	if bin != "codex" {
		t.Errorf("bin = %q, want 'codex'", bin)
	}
	// Should have at least prompt arg.
	if len(args) == 0 {
		t.Fatal("expected at least 1 arg")
	}
	// Last arg should be the prompt.
	lastArg := args[len(args)-1]
	if lastArg != "test prompt" {
		t.Errorf("last arg = %q, want 'test prompt'", lastArg)
	}
}

func TestBuildLaunchArgs_Claude(t *testing.T) {
	bin, args, err := BuildLaunchArgs("/tmp/home", "claude", "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	if bin != "claude" {
		t.Errorf("bin = %q, want 'claude'", bin)
	}
	if len(args) == 0 {
		t.Fatal("expected at least 1 arg")
	}
	lastArg := args[len(args)-1]
	if lastArg != "test prompt" {
		t.Errorf("last arg = %q, want 'test prompt'", lastArg)
	}
}

func TestBuildLaunchArgs_Agy(t *testing.T) {
	bin, args, err := BuildLaunchArgs("/tmp/home", "agy", "test prompt")
	if err != nil {
		t.Fatal(err)
	}
	if bin != "agy" {
		t.Errorf("bin = %q, want 'agy'", bin)
	}
	if len(args) == 0 {
		t.Fatal("expected at least 1 arg")
	}
	// Last arg should be the prompt.
	lastArg := args[len(args)-1]
	if lastArg != "test prompt" {
		t.Errorf("last arg = %q, want 'test prompt'", lastArg)
	}
	// Agy has ExtraArgs including --dangerously-skip-permissions.
	foundSkipPerms := false
	for _, arg := range args {
		if arg == "--dangerously-skip-permissions" {
			foundSkipPerms = true
		}
	}
	if !foundSkipPerms {
		t.Error("agy args should include --dangerously-skip-permissions")
	}
}

func TestBuildLaunchArgs_UnknownHarness(t *testing.T) {
	_, _, err := BuildLaunchArgs("/tmp/home", "unknown-harness", "test prompt")
	if err == nil {
		t.Error("expected error for unknown harness")
	}
}

// =============================================================================
// Terminal report contract tests
// =============================================================================

func TestBuildLaunchPrompt_ContainsExactReportCommand(t *testing.T) {
	brief := "# Task brief: report-test\n\nDo work.\n"
	prompt, env, err := BuildLaunchPrompt(LaunchPromptInput{
		TaskID:          "report-test",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-alpha",
		ParentHome:      "/tmp/parent",
		WorktreePath:    t.TempDir(),
		HomeDir:         "/tmp/home",
		BriefContent:    []byte(brief),
		HarnessName:     "pi",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Must contain the exact report command with PR {url} placeholder.
	if !strings.Contains(prompt, `munsu report done "PR {url}"`) {
		t.Error("prompt must contain exact 'munsu report done \"PR {url}\"' command")
	}

	// Envelope must reference the parent captain.
	if env.ParentCaptainID != "captain-alpha" {
		t.Errorf("env ParentCaptainID = %q, want 'captain-alpha'", env.ParentCaptainID)
	}
}

// =============================================================================
// Recovery identity test
// =============================================================================

func TestBuildLaunchPrompt_RecoveryDeterminism(t *testing.T) {
	brief := "# Task\n\nDeterministic content.\n"
	wt := t.TempDir()

	input := LaunchPromptInput{
		TaskID:          "recovery-test",
		TaskKind:        "ship",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		WorktreePath:    wt,
		HomeDir:         "/tmp/home",
		BriefContent:    []byte(brief),
		HarnessName:     "pi",
	}

	prompt1, env1, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}

	// Same input → same output (deterministic).
	prompt2, env2, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}

	if prompt1 != prompt2 {
		t.Error("repeated BuildLaunchPrompt with same input should produce identical prompt")
	}
	if env1.CharterSHA256 != env2.CharterSHA256 {
		t.Error("repeated BuildLaunchPrompt should produce identical charter hash")
	}
	if env1.PromptSHA256 != env2.PromptSHA256 {
		t.Error("repeated BuildLaunchPrompt should produce identical prompt hash")
	}
}

// =============================================================================
// VerifyRequiredSkills test
// =============================================================================

func TestVerifyRequiredSkills_SourcePresent(t *testing.T) {
	tmp := t.TempDir()
	skillPath := filepath.Join(tmp, "my-skill.md")
	skillContent := "# My Skill\n\nInstructions.\n"
	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		t.Fatal(err)
	}

	env := &LaunchEnvelope{
		RequiredSkills: []SkillEntry{
			{Name: "my-skill", Applicable: true, SourcePath: skillPath, SourceSHA256: sha256Content([]byte(skillContent))},
		},
	}

	_, err := VerifyRequiredSkills(env, tmp)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestVerifyRequiredSkills_SourceMissing(t *testing.T) {
	env := &LaunchEnvelope{
		RequiredSkills: []SkillEntry{
			{Name: "missing-skill", Applicable: true, SourcePath: "/nonexistent/skill.md"},
		},
	}

	_, err := VerifyRequiredSkills(env, "/tmp")
	if err == nil {
		t.Error("expected error for missing required skill source")
	}
}

func TestVerifyRequiredSkills_HashMismatch(t *testing.T) {
	tmp := t.TempDir()
	skillPath := filepath.Join(tmp, "my-skill.md")
	if err := os.WriteFile(skillPath, []byte("original content"), 0644); err != nil {
		t.Fatal(err)
	}

	env := &LaunchEnvelope{
		RequiredSkills: []SkillEntry{
			{Name: "my-skill", Applicable: true, SourcePath: skillPath, SourceSHA256: "deadbeef"},
		},
	}

	_, err := VerifyRequiredSkills(env, tmp)
	if err == nil {
		t.Error("expected error for SHA-256 mismatch")
	}
}

func TestVerifyRequiredSkills_NonApplicableSkipped(t *testing.T) {
	env := &LaunchEnvelope{
		RequiredSkills: []SkillEntry{
			{Name: "captain-skill", Applicable: false, SourcePath: "/nonexistent"},
		},
	}

	_, err := VerifyRequiredSkills(env, "/tmp")
	if err != nil {
		t.Errorf("non-applicable skills should be skipped, got: %v", err)
	}
}

// =============================================================================
// Terminal report reminder test
// =============================================================================

func TestTerminalReportReminder_ContainsTaskKey(t *testing.T) {
	reminder := terminalReportReminder("my-task", "captain-42")
	if !strings.Contains(reminder, "my-task") {
		t.Error("terminal report reminder must include the task ID for --key")
	}
	if !strings.Contains(reminder, "captain-42") {
		t.Error("terminal report reminder must include the parent captain ID")
	}
}
