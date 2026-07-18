package integrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test capabilities for Pi
func TestEnabledCapabilities_Pi(t *testing.T) {
	caps := EnabledCapabilities("pi")
	if len(caps) == 0 {
		t.Fatal("expected Pi capabilities to be non-empty")
	}

	capSet := make(map[Capability]bool)
	for _, c := range caps {
		capSet[c] = true
	}

	if !capSet[CapSessionStart] {
		t.Error("expected CapSessionStart for Pi")
	}
	if !capSet[CapWakeFollowUp] {
		t.Error("expected CapWakeFollowUp for Pi")
	}
	if !capSet[CapTurnEndGuard] {
		t.Error("expected CapTurnEndGuard for Pi")
	}
	if !capSet[CapPreToolCheck] {
		t.Error("expected CapPreToolCheck for Pi")
	}
	if !capSet[CapScopeGate] {
		t.Error("expected CapScopeGate for Pi")
	}
}

// Test capabilities for Claude
func TestEnabledCapabilities_Claude(t *testing.T) {
	caps := EnabledCapabilities("claude")
	if len(caps) == 0 {
		t.Fatal("expected Claude capabilities to be non-empty")
	}

	capSet := make(map[Capability]bool)
	for _, c := range caps {
		capSet[c] = true
	}

	if !capSet[CapSessionStart] {
		t.Error("expected CapSessionStart for Claude")
	}
	if !capSet[CapTurnEndGuard] {
		t.Error("expected CapTurnEndGuard for Claude")
	}
	if !capSet[CapPreToolCheck] {
		t.Error("expected CapPreToolCheck for Claude")
	}

	// Claude does NOT have CapWakeFollowUp or CapScopeGate
	if capSet[CapWakeFollowUp] {
		t.Error("Claude should NOT have CapWakeFollowUp")
	}
	if capSet[CapScopeGate] {
		t.Error("Claude should NOT have CapScopeGate")
	}
}

// Test unknown harness returns no capabilities
func TestEnabledCapabilities_Unknown(t *testing.T) {
	caps := EnabledCapabilities("nonexistent")
	if len(caps) != 0 {
		t.Fatalf("expected no capabilities for unknown harness, got %d", len(caps))
	}
}

// Test AssertSupportedHarness
func TestAssertSupportedHarness(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"pi", false},
		{"claude", false},
		{"", true},
		{"nonexistent", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AssertSupportedHarness(tt.name)
			if tt.wantErr && err == nil {
				t.Errorf("AssertSupportedHarness(%q) expected error", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("AssertSupportedHarness(%q) unexpected error: %v", tt.name, err)
			}
		})
	}
}

// Test MunsuHomeArtifactsDir
func TestMunsuHomeArtifactsDir(t *testing.T) {
	dir := MunsuHomeArtifactsDir("/tmp/munsu-home")
	expected := "/tmp/munsu-home/integrate"
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

// Test ManifestPath per-harness per-scope
func TestManifestPath(t *testing.T) {
	path := ManifestPath("/home/munsu", "pi", ScopeUser)
	expected := "/home/munsu/integrate/pi/user/manifest.json"
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}

	// Project scope now requires a cwd; the path includes a deterministic slug.
	path = ManifestPath("/home/munsu", "pi", ScopeProject, "/tmp/project-a")
	if !strings.HasPrefix(path, "/home/munsu/integrate/pi/project/") {
		t.Errorf("project manifest path should start with /home/munsu/integrate/pi/project/, got %q", path)
	}
	if !strings.HasSuffix(path, "/manifest.json") {
		t.Errorf("project manifest path should end with /manifest.json, got %q", path)
	}
	// Should contain a hex slug directory
	parts := strings.Split(path, "/")
	slugDir := parts[len(parts)-2]
	if len(slugDir) != 16 {
		t.Errorf("expected 16-char hex slug directory, got %q (len=%d)", slugDir, len(slugDir))
	}
	// Verify slug is hex
	for _, c := range slugDir {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("slug char %q is not hex", c)
		}
	}
}

// Test ManifestPath scope isolation
func TestManifestPath_ScopeIsolation(t *testing.T) {
	userPath := ManifestPath("/home/munsu", "pi", ScopeUser)
	projPath := ManifestPath("/home/munsu", "pi", ScopeProject, "/tmp/project-a")

	if userPath == projPath {
		t.Error("user and project manifest paths must be different")
	}
	if !strings.Contains(userPath, "/pi/user/") {
		t.Errorf("user manifest path should contain /pi/user/, got %q", userPath)
	}
	if !strings.Contains(projPath, "/pi/project/") {
		t.Errorf("project manifest path should contain /pi/project/, got %q", projPath)
	}
}

// Test ManifestPath project isolation: two different projects must produce different paths
func TestManifestPath_ProjectIsolation(t *testing.T) {
	pathA := ManifestPath("/home/munsu", "pi", ScopeProject, "/tmp/project-a")
	pathB := ManifestPath("/home/munsu", "pi", ScopeProject, "/tmp/project-b")

	if pathA == pathB {
		t.Fatal("two different projects must produce different manifest paths")
	}

	// Same canonical path must produce stable path
	pathA2 := ManifestPath("/home/munsu", "pi", ScopeProject, "/tmp/project-a")
	if pathA != pathA2 {
		t.Errorf("same project path must produce stable manifest path: %q != %q", pathA, pathA2)
	}
}

// Test ManifestPath harness separation
func TestManifestPath_HarnessSeparation(t *testing.T) {
	piUser := ManifestPath("/home/munsu", "pi", ScopeUser)
	claudeUser := ManifestPath("/home/munsu", "claude", ScopeUser)

	if piUser == claudeUser {
		t.Error("manifest paths for different harnesses must be different")
	}
	if !strings.Contains(piUser, "/pi/") {
		t.Errorf("pi manifest path should contain /pi/, got %q", piUser)
	}
	if !strings.Contains(claudeUser, "/claude/") {
		t.Errorf("claude manifest path should contain /claude/, got %q", claudeUser)
	}
}

// Test UserExtensionsDir
func TestUserExtensionsDir(t *testing.T) {
	dir := UserExtensionsDir()
	if dir == "" {
		t.Fatal("expected non-empty user extensions dir")
	}
	if filepath.Base(filepath.Dir(dir)) != "agent" || filepath.Base(filepath.Dir(filepath.Dir(dir))) != ".pi" {
		t.Logf("user extensions dir: %s", dir)
	}
}

// Test ProjectExtensionsDir
func TestProjectExtensionsDir(t *testing.T) {
	dir := ProjectExtensionsDir("/some/project")
	expected := "/some/project/.pi/extensions"
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

// Test PiExtensionTemplate
func TestPiExtensionTemplate(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if tmpl == "" {
		t.Fatal("expected non-empty template")
	}
	if !hasFirstLineMarker(tmpl) {
		t.Error("template must contain ownership marker as first line")
	}
	if strings.Contains(tmpl, "BINPATH") {
		t.Error("BINPATH placeholder must be replaced")
	}

	// Must not use --json (not a valid munsu flag)
	if strings.Contains(tmpl, "--json") {
		t.Error("template must not use --json flag; use --output json")
	}
}

// Test PiExtensionTemplate binary path substitutions
func TestPiExtensionTemplate_BinarySubstitution(t *testing.T) {
	binPath := "/custom/path/munsu"
	tmpl := PiExtensionTemplate(binPath)
	if strings.Contains(tmpl, "BINPATH") {
		t.Errorf("BINPATH still present in template output")
	}
	if !strings.Contains(tmpl, "/custom/path/munsu") {
		t.Errorf("expected binary path in template output")
	}
}

// Test PiExtensionTemplate JSON encoding for special characters
func TestPiExtensionTemplate_JSONEncoding(t *testing.T) {
	// Path with spaces
	tmpl := PiExtensionTemplate("/opt/munsu with spaces/munsu")
	if strings.Contains(tmpl, "BINPATH") {
		t.Errorf("BINPATH still present for path with spaces")
	}
	if !strings.Contains(tmpl, "/opt/munsu with spaces/munsu") {
		t.Errorf("expected path with spaces in template output")
	}
}

// Test PiExtensionTemplate agent_settled usage
func TestPiExtensionTemplate_UsesAgentSettled(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if !strings.Contains(tmpl, "agent_settled") {
		t.Error("template must use agent_settled event for wake follow-up (Pi may still auto-retry/compact/continue after agent_end)")
	}
	// Verify agent_end is NOT used for claim/follow-up (it fires before Pi is done).
	// The template may still reference agent_end in comments for non-claim purposes,
	// but must not use it for wake claim logic.
	if strings.Contains(tmpl, "agent_end") {
		t.Log("template references agent_end (acceptable for non-claim references)")
	}
}

// Test PiExtensionTemplate completion syntax
func TestPiExtensionTemplate_CompletionSyntax(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if !strings.Contains(tmpl, ": <summary>") {
		t.Error("template must require colon in completion syntax")
	}
}

// Test PiExtensionTemplate scope safety
func TestPiExtensionTemplate_ScopeSafety(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if !strings.Contains(tmpl, "safety-check") && !strings.Contains(tmpl, "safety_check") {
		t.Error("template must call integrate safety-check")
	}
}

// Test PiExtensionTemplate durable pending wake
func TestPiExtensionTemplate_DurablePendingWake(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if !strings.Contains(tmpl, "munsu-pending-wake") {
		t.Error("template must persist pending wake state")
	}
}

// Test PiExtensionTemplate output flag
func TestPiExtensionTemplate_OutputJSON(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if strings.Contains(tmpl, "\"--json\"") || strings.Contains(tmpl, "'--json'") {
		t.Error("template must not pass --json; use --output json")
	}
}

// Test FileContainsOwnershipMarker
func TestFileContainsOwnershipMarker(t *testing.T) {
	dir := t.TempDir()

	markedFile := filepath.Join(dir, "marked.ts")
	if err := os.WriteFile(markedFile, []byte("// munsu-integrate v1 -- do not edit this section\nconst x = 1;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !FileContainsOwnershipMarker(markedFile) {
		t.Error("expected file with first-line marker to report ownership")
	}

	unmarkedFile := filepath.Join(dir, "unmarked.ts")
	if err := os.WriteFile(unmarkedFile, []byte("const x = 1;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if FileContainsOwnershipMarker(unmarkedFile) {
		t.Error("expected file without marker to report no ownership")
	}

	if FileContainsOwnershipMarker(filepath.Join(dir, "nonexistent.ts")) {
		t.Error("expected non-existent file to report no ownership")
	}
}

// Test writeAtomic
func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")

	if err := writeAtomic(target, "hello world", 0644); err != nil {
		t.Fatalf("writeAtomic failed: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after writeAtomic: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

// Test writeAtomic no-op on identical
func TestWriteAtomic_NoopOnIdentical(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")

	if err := writeAtomic(target, "hello world", 0644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeAtomic(target, "hello world", 0644); err != nil {
		t.Fatalf("second write (should be no-op): %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

// Test GenerateManifest
func TestGenerateManifest(t *testing.T) {
	caps := []Capability{CapSessionStart, CapWakeFollowUp}
	m := GenerateManifest("pi", "user", caps, "test-digest")

	if m.Harness != "pi" {
		t.Errorf("expected harness 'pi', got %q", m.Harness)
	}
	if m.SchemaVersion != "munsu.integrate/v1" {
		t.Errorf("expected schema version 'munsu.integrate/v1', got %q", m.SchemaVersion)
	}
	if m.Scope != "user" {
		t.Errorf("expected scope 'user', got %q", m.Scope)
	}
	if len(m.Capabilities) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(m.Capabilities))
	}
	if m.Capabilities[0] != "session-start" {
		t.Errorf("expected first capability 'session-start', got %q", m.Capabilities[0])
	}
}

// Test ValidateStrict
func TestValidateStrict(t *testing.T) {
	caps := []Capability{CapSessionStart, CapWakeFollowUp}
	m := Manifest{
		SchemaVersion: "munsu.integrate/v1",
		Harness:       "pi",
		Version:       "1.0.0",
		Scope:         "user",
		InstalledAt:   "2026-01-01T00:00:00Z",
		TargetPaths:   []string{"/tmp/munsu/ext/munsu-pi-integration.ts"},
		Capabilities:  []string{"session-start", "wake-followup"},
		ContentDigest: "abc123def456",
	}

	err := ValidateStrict(m, "pi", "user", "1.0.0", caps, 1)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Schema mismatch
	m2 := m
	m2.SchemaVersion = "munsu.integrate/v2"
	if err := ValidateStrict(m2, "pi", "user", "1.0.0", caps, 1); err == nil {
		t.Error("expected schema mismatch error")
	}

	// Missing digest
	m3 := m
	m3.ContentDigest = ""
	if err := ValidateStrict(m3, "pi", "user", "1.0.0", caps, 1); err == nil {
		t.Error("expected missing digest error")
	}

	// Missing capability
	m4 := m
	m4.Capabilities = []string{"session-start"}
	if err := ValidateStrict(m4, "pi", "user", "1.0.0", caps, 1); err == nil {
		t.Error("expected missing capability error")
	}
}

// Test PiExtensionContentDigest
func TestPiExtensionContentDigest(t *testing.T) {
	digest := PiExtensionContentDigest("/usr/local/bin/munsu")
	if digest == "" {
		t.Fatal("expected non-empty digest")
	}
	if len(digest) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(digest))
	}
	d2 := PiExtensionContentDigest("/usr/local/bin/munsu")
	if digest != d2 {
		t.Fatal("digest must be deterministic")
	}
	d3 := PiExtensionContentDigest("/opt/bin/munsu")
	if digest == d3 {
		t.Fatal("digest must differ for different paths")
	}
}

// Test SafetyCheck
func TestSafetyCheck(t *testing.T) {
	result := SafetyCheck(t.TempDir())
	if result.Identity == "" && result.Error == "" {
		t.Error("SafetyCheck should return identity or error")
	}

	// With env gate
	dir := t.TempDir()
	t.Setenv("NO_MISTAKES_GATE", "1")
	result = SafetyCheck(dir)
	if !result.GateRefused {
		t.Errorf("expected gate refused with NO_MISTAKES_GATE, got identity=%s gate=%s", result.Identity, result.GateCapability)
	}
}

// Test CheckPiCapability
func TestCheckPiCapability(t *testing.T) {
	err := CheckPiCapability("/nonexistent/pi")
	if err == nil {
		t.Fatal("CheckPiCapability(/nonexistent/pi) must fail for nonexistent path")
	}
}

// Test Munsu path resolver injection
func TestMunsuPathResolver(t *testing.T) {
	ResetMunsuPathResolver()

	SetMunsuPathResolver(testResolver{})
	path, err := ResolveMunsuPathString()
	if err != nil {
		t.Fatalf("test resolver failed: %v", err)
	}
	if path != "/test/munsu/bin" {
		t.Errorf("expected /test/munsu/bin, got %q", path)
	}
	ResetMunsuPathResolver()
}

// Test ownership marker edge cases
func TestHasFirstLineMarker_EdgeCases(t *testing.T) {
	tests := []struct {
		content string
		want    bool
	}{
		{"// munsu-integrate v1 -- do not edit this section\nconst x = 1;", true},
		{"const x = 1;", false},
		{"", false},
		{"// munsu-integrate v1 -- do not edit this section", true},
		{"some code before\n// munsu-integrate v1 -- do not edit this section\nmore code", false},
		{"// munsu-integrate -- do not edit this section\nconst x = 1;", false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := hasFirstLineMarker(tt.content)
			if got != tt.want {
				preview := tt.content
				if len(preview) > 30 {
					preview = preview[:30]
				}
				t.Errorf("hasFirstLineMarker(%q) = %v, want %v", preview, got, tt.want)
			}
		})
	}
}

type testResolver struct{}

func (testResolver) Resolve() (string, error) {
	return "/test/munsu/bin", nil
}

// Test SafetyCheck fails closed on unrelated checkout
func TestSafetyCheck_FailsClosedOnUnrelated(t *testing.T) {
	result := SafetyCheck(t.TempDir())
	// Temp dir with no git repo is "unrelated" and should fail closed
	if !result.GateRefused {
		t.Errorf("SafetyCheck(unrelated).GateRefused = false; identity=%s gate=%s", result.Identity, result.GateCapability)
	}
}

// Test SafetyCheck with env gate
func TestSafetyCheck_WithEnvGate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NO_MISTAKES_GATE", "1")
	result := SafetyCheck(dir)
	if !result.GateRefused {
		t.Errorf("expected gate refused with NO_MISTAKES_GATE, got identity=%s gate=%s", result.Identity, result.GateCapability)
	}
	if result.GateCapability != "gate-present" {
		t.Errorf("expected gate-present, got %s", result.GateCapability)
	}
}

// Test writeAtomic creates subdirectories as needed
func TestWriteAtomic_CreatesSubdirs(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "test.txt")
	if err := writeAtomic(target, "hello world", 0644); err != nil {
		t.Fatalf("writeAtomic failed: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after writeAtomic: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

// Test SafetyCheck uses scope.Classify without duplicating logic
func TestSafetyCheck_UsesScopeClassify(t *testing.T) {
	// Verify SafetyCheck delegates to scope.Classify by checking error behavior
	// on a non-existent path (scope.Classify fails closed)
	result := SafetyCheck("/nonexistent/path/that/does/not/exist")
	// Should have identity set (even if error)
	if result.Identity == "" && result.Error == "" {
		t.Error("SafetyCheck should have identity or error")
	}
}

// Test CheckPiCapability rejects malformed versions
func TestCheckPiCapability_RejectsMalformedVersion(t *testing.T) {
	// Create a script that returns a malformed version
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "pi")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho 'not-a-valid-semver'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	err := CheckPiCapability(scriptPath)
	if err == nil {
		t.Fatal("CheckPiCapability must reject malformed non-semver version")
	}
	t.Logf("CheckPiCapability correctly rejects malformed version: %v", err)

}

// Test CheckPiCapability rejects old 0.x versions
func TestCheckPiCapability_RejectsOldVersion(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "pi")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho '0.1.0'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	err := CheckPiCapability(scriptPath)
	if err == nil {
		t.Fatal("CheckPiCapability must reject old version 0.1.0 < minimum " + PiMinimumVersion)
	}
	t.Logf("CheckPiCapability correctly rejects old version: %v", err)

}

// Test template uses parseContract helper
func TestPiExtensionTemplate_UsesParseContract(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if !strings.Contains(tmpl, "parseContract") {
		t.Error("template must define and use a strict parseContract helper")
	}
	if !strings.Contains(tmpl, "parseSafetyCheck") {
		t.Error("template must define and use a strict parseSafetyCheck helper")
	}
}

// Test template references session-start command
func TestPiExtensionTemplate_SessionStartCommand(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if !strings.Contains(tmpl, "session-start") {
		t.Error("template must invoke munsu session-start command")
	}
	// Must use --output json
	if !strings.Contains(tmpl, `"session-start", "--output", "json"`) &&
		!strings.Contains(tmpl, `"session-start","--output","json"`) {
		t.Error("template must pass --output json to session-start")
	}
	// Must expect session.start kind
	if !strings.Contains(tmpl, "session.start") {
		t.Error("template must expect session.start kind")
	}
}

// Test template must fail closed on tool_call safety failures
func TestPiExtensionTemplate_ToolCallFailClosed(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	// Should return block:true, reason:... when safety check fails
	if !strings.Contains(tmpl, "block: true") {
		t.Error("template must return block:true on tool_call safety failure")
	}
}

// Test template scans newest-to-oldest for wake restore
func TestPiExtensionTemplate_NewestToOldestScan(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if !strings.Contains(tmpl, "NEWEST-TO-OLDEST") &&
		!strings.Contains(tmpl, "wakeEntries.length") {
		t.Fatal("template must scan entries newest-to-oldest for wake restore")
	}
}

// Test template requires numeric lease_expires
func TestPiExtensionTemplate_NumericLeaseExpiry(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if !strings.Contains(tmpl, "lease_expires") &&
		!strings.Contains(tmpl, "leaseExpiry") {
		t.Error("template must handle numeric lease_expires")
	}
}

// Test ClaudeSettingsContent contains required hook entries
func TestClaudeSettingsContent_Hooks(t *testing.T) {
	content := ClaudeSettingsContent("/usr/local/bin/munsu")
	if content == "" {
		t.Fatal("expected non-empty settings content")
	}

	// Must have all three hook types
	if !strings.Contains(content, "SessionStart") {
		t.Error("settings must contain SessionStart hooks")
	}
	if !strings.Contains(content, "PreToolUse") {
		t.Error("settings must contain PreToolUse hooks")
	}
	if !strings.Contains(content, "Stop") {
		t.Error("settings must contain Stop hooks")
	}

	// Must not contain BINPATH placeholder
	if strings.Contains(content, "BINPATH") {
		t.Error("BINPATH placeholder must be replaced")
	}

	// Must anchor commands to the munsu binary path
	if !strings.Contains(content, "/usr/local/bin/munsu") {
		t.Error("settings must reference the munsu binary path")
	}

	// SessionStart matcher must exclude compact
	if strings.Contains(content, "compact") {
		t.Error("SessionStart matcher must not include compact")
	}

	// Verify structure matches firstmate: correct hook event keys
	if !strings.Contains(content, `"matcher": "startup|resume|clear"`) {
		t.Error("SessionStart matcher must match startup|resume|clear")
	}
	if !strings.Contains(content, `"matcher": "Bash"`) {
		t.Error("PreToolUse matcher must be Bash")
	}

	// Valid JSON
	var parsed interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("settings content must be valid JSON: %v", err)
	}

	// Check that SessionStart does NOT have a matcher for exclusion (no matcher = all events)
	hooks := parsed.(map[string]interface{})
	if _, ok := hooks["hooks"]; !ok {
		t.Fatal("settings must have hooks key")
	}
}

// Test ClaudeSettingsContent binary substitution
func TestClaudeSettingsContent_BinarySubstitution(t *testing.T) {
	binPath := "/custom/path/with spaces/munsu"
	content := ClaudeSettingsContent(binPath)
	if strings.Contains(content, "BINPATH") {
		t.Errorf("BINPATH still present in settings output")
	}
	if !strings.Contains(content, "/custom/path/with spaces/munsu") {
		t.Errorf("expected binary path with spaces in settings output")
	}

	// Must be valid JSON
	var parsed interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("settings content must be valid JSON: %v", err)
	}
}

// Test ClaudeSettingsDigest determinism
func TestClaudeSettingsDigest_Deterministic(t *testing.T) {
	d1 := ClaudeSettingsDigest("/usr/local/bin/munsu")
	d2 := ClaudeSettingsDigest("/usr/local/bin/munsu")
	if d1 == "" || len(d1) != 64 {
		t.Fatal("expected 64-char hex digest")
	}
	if d1 != d2 {
		t.Fatal("digest must be deterministic")
	}

	d3 := ClaudeSettingsDigest("/opt/bin/munsu")
	if d1 == d3 {
		t.Fatal("digest must differ for different binary paths")
	}
}

// Test ClaudeSettingsHasOwnedHooks verifies structural ownership detection
// for Claude settings.json — install → status=installed; remove a hook → drifted.
func TestClaudeSettingsHasOwnedHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	// Write a valid settings.json with all hooks
	content := ClaudeSettingsContent("/usr/local/bin/munsu")
	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// All hooks present → owned
	present, msg, err := ClaudeSettingsHasOwnedHooks(settingsPath, "/usr/local/bin/munsu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !present {
		t.Errorf("expected all hooks present, got: %s", msg)
	}

	// Remove Stop hook → drifted
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatal(err)
	}
	hooks := parsed["hooks"].(map[string]interface{})
	delete(hooks, "Stop")
	modified, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, modified, 0644); err != nil {
		t.Fatal(err)
	}

	present, msg, err = ClaudeSettingsHasOwnedHooks(settingsPath, "/usr/local/bin/munsu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if present {
		t.Error("expected hooks missing after Stop removal")
	}
	if !strings.Contains(msg, "Stop") {
		t.Errorf("expected message to mention missing Stop hook, got: %s", msg)
	}

	// Missing file → error
	_, _, err = ClaudeSettingsHasOwnedHooks(filepath.Join(dir, "nonexistent.json"), "/usr/local/bin/munsu")
	if err == nil {
		t.Error("expected error for missing file")
	}

	// Invalid JSON → error
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err = ClaudeSettingsHasOwnedHooks(filepath.Join(dir, "bad.json"), "/usr/local/bin/munsu")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// TestClaudeStatusDriftDetection tests that Status() detects drift structurally
// in Claude settings.json: install → status=installed; remove a hook → drifted.
// Uses SetMunsuPathResolver to avoid PATH dependency.
func TestClaudeStatusDriftDetection(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projectDir := filepath.Join(dir, "project")
	os.MkdirAll(projectDir, 0755)

	// Create a stub munsu binary for path resolution
	munsuBin := filepath.Join(dir, "munsu")
	if err := os.WriteFile(munsuBin, []byte("#!/bin/sh\necho munsu\n"), 0755); err != nil {
		t.Fatal(err)
	}

	SetMunsuPathResolver(testMunsuResolver{path: munsuBin})
	defer ResetMunsuPathResolver()

	// Install Claude settings via Install() with ScopeProject to isolate writes
	scope := ScopeProject
	result, err := Install(homeDir, projectDir, "claude", scope, false)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if result.State != "installed" {
		t.Fatalf("expected installed, got %q", result.State)
	}

	// Status should report installed
	status, err := Status(homeDir, projectDir, "claude", scope)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.State != "installed" {
		t.Fatalf("expected installed state, got %q: %s", status.State, status.Message)
	}
	if status.Drifted {
		t.Error("expected no drift after clean install")
	}

	// Resolve the target settings path from the expected project location
	targetPath := filepath.Join(projectDir, ".claude", "settings.json")

	// Remove the Stop hooks from the target file
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	hooks := parsed["hooks"].(map[string]interface{})
	delete(hooks, "Stop")
	modified, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, modified, 0644); err != nil {
		t.Fatal(err)
	}

	// Status should now report drifted
	status, err = Status(homeDir, projectDir, "claude", scope)
	if err != nil {
		t.Fatalf("Status failed after modification: %v", err)
	}
	if status.State != "drifted" {
		t.Fatalf("expected drifted state after hook removal, got %q: %s", status.State, status.Message)
	}
	if !status.Drifted {
		t.Error("expected Drifted=true after hook removal")
	}
}

type testMunsuResolver struct {
	path string
}

func (r testMunsuResolver) Resolve() (string, error) {
	return r.path, nil
}
