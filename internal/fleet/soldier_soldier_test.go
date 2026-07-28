//go:build integration

package fleet

import (
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

func TestLaunchEnvelope_IntegrityVerify(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("verify-test", "ship", "direct-PR")
	brief := []byte("# Task brief: verify-test\n\nSome content.\n")
	prompt := "full prompt with charter and brief\n"

	if err := os.WriteFile(filepath.Join(tmp, CharterName), []byte(charter), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, BriefName), brief, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, PromptName), []byte(prompt), 0644); err != nil {
		t.Fatal(err)
	}

	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "verify-test",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		CharterSHA256:   sha256Content([]byte(charter)),
		BriefSHA256:     sha256Content(brief),
		PromptSHA256:    sha256Content([]byte(prompt)),
	}
	if err := WriteEnvelope(tmp, env); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEnvelopeIntegrity(tmp); err != nil {
		t.Errorf("integrity verification failed: %v", err)
	}
}

func TestLaunchEnvelope_IntegrityFailCharter(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("fail-test", "ship", "direct-PR")
	brief := []byte("brief")
	prompt := "prompt"
	if err := os.WriteFile(filepath.Join(tmp, CharterName), []byte(charter), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, BriefName), brief, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, PromptName), []byte(prompt), 0644); err != nil {
		t.Fatal(err)
	}
	// Wrong hash.
	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "fail-test",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		CharterSHA256:   "deadbeef",
		BriefSHA256:     sha256Content(brief),
		PromptSHA256:    sha256Content([]byte(prompt)),
	}
	if err := WriteEnvelope(tmp, env); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEnvelopeIntegrity(tmp); err == nil {
		t.Error("expected integrity verification to fail, got nil")
	}
}

func TestLaunchEnvelope_IntegrityFailMissingBrief(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("brief-test", "ship", "direct-PR")
	if err := os.WriteFile(filepath.Join(tmp, CharterName), []byte(charter), 0644); err != nil {
		t.Fatal(err)
	}
	// No brief file.
	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "brief-test",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		CharterSHA256:   sha256Content([]byte(charter)),
		BriefSHA256:     "should-exist",
		PromptSHA256:    "should-exist",
	}
	if err := WriteEnvelope(tmp, env); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEnvelopeIntegrity(tmp); err == nil {
		t.Error("expected integrity verification to fail for missing brief, got nil")
	}
}

func TestLaunchEnvelope_IntegrityFailMissingPrompt(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("prompt-test", "ship", "direct-PR")
	brief := []byte("brief")
	if err := os.WriteFile(filepath.Join(tmp, CharterName), []byte(charter), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, BriefName), brief, 0644); err != nil {
		t.Fatal(err)
	}
	// No prompt file.
	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "prompt-test",
		DeliveryMode:    "direct-PR",
		ParentCaptainID: "captain-1",
		ParentHome:      "/tmp/parent",
		CharterSHA256:   sha256Content([]byte(charter)),
		BriefSHA256:     sha256Content(brief),
		PromptSHA256:    "should-exist",
	}
	if err := WriteEnvelope(tmp, env); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEnvelopeIntegrity(tmp); err == nil {
		t.Error("expected integrity verification to fail for missing prompt, got nil")
	}
}

func TestLaunchEnvelope_IntegrityEmptyMetaFields(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("meta-test", "ship", "direct-PR")
	brief := []byte("brief")
	prompt := "prompt"
	for _, f := range []string{CharterName, BriefName, PromptName} {
		data := []byte(charter)
		if f == BriefName {
			data = brief
		} else if f == PromptName {
			data = []byte(prompt)
		}
		os.WriteFile(filepath.Join(tmp, f), data, 0644)
	}
	// Envelope with empty TaskID and empty DeliveryMode.
	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		CharterSHA256:   sha256Content([]byte(charter)),
		BriefSHA256:     sha256Content(brief),
		PromptSHA256:    sha256Content([]byte(prompt)),
	}
	if err := WriteEnvelope(tmp, env); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEnvelopeIntegrity(tmp); err == nil {
		t.Error("expected integrity verification to fail for empty meta fields, got nil")
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
	if got := SkillAuthorityClass("tasks-axi", "soldier"); got != "captain" {
		t.Errorf("tasks-axi = %q, want 'captain' (denied)", got)
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
		CharterSHA256:   sha256Content([]byte(charter)),
		BriefSHA256:     sha256Content(brief),
		PromptSHA256:    sha256Content([]byte(prompt)),
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

func TestPersistLaunchFiles_EnvelopeHashesMatch(t *testing.T) {
	tmp := t.TempDir()
	charter := DefaultCharter("hash-test", "ship", "direct-PR")
	brief := []byte("# Task brief\n\nVerify hashes.\n")
	prompt := "complete prompt with charter and brief"
	charterHash := sha256Content([]byte(charter))
	briefHash := sha256Content(brief)
	promptHash := sha256Content([]byte(prompt))
	env := &LaunchEnvelope{
		EnvelopeVersion: EnvelopeVersion,
		TaskID:          "hash-test",
		CharterSHA256:   charterHash,
		BriefSHA256:     briefHash,
		PromptSHA256:    promptHash,
	}
	if err := PersistLaunchFiles(tmp, charter, brief, env, prompt); err != nil {
		t.Fatal(err)
	}
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
	if readEnv.PromptSHA256 != promptHash {
		t.Errorf("envelope prompt hash = %q, want %q", readEnv.PromptSHA256, promptHash)
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
	reminder := terminalReportReminder("test-task-42", "captain-alpha")
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
	note := terminalReportReminder("x-task", "x-captain")
	expected := `munsu report done "PR {url}" --key x-task`
	if !strings.Contains(note, expected) {
		t.Errorf("terminal report must contain exact bytes:\nwant: %s\ngot:  %s", expected, note)
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
	prompt1, env1, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	prompt2, env2, err := BuildLaunchPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	if prompt1 != prompt2 {
		t.Error("repeated BuildLaunchPrompt with same input should produce identical prompt")
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
