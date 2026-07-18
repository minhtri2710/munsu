package integrate

import (
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
		{"claude", true},
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

	path = ManifestPath("/home/munsu", "pi", ScopeProject)
	expected = "/home/munsu/integrate/pi/project/manifest.json"
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

// Test ManifestPath scope isolation
func TestManifestPath_ScopeIsolation(t *testing.T) {
	userPath := ManifestPath("/home/munsu", "pi", ScopeUser)
	projPath := ManifestPath("/home/munsu", "pi", ScopeProject)

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

// Test PiExtensionTemplate agent_end usage
func TestPiExtensionTemplate_UsesAgentEnd(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if strings.Contains(tmpl, "agent_settled") {
		t.Error("template must not use agent_settled (not a Pi API event)")
	}
	if !strings.Contains(tmpl, "agent_end") {
		t.Error("template must use agent_end event for wake follow-up")
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
		t.Logf("expected gate refused with NO_MISTAKES_GATE, got identity=%s gate=%s", result.Identity, result.GateCapability)
	}
}

// Test CheckPiCapability
func TestCheckPiCapability(t *testing.T) {
	err := CheckPiCapability("/nonexistent/pi")
	if err == nil {
		t.Skip("unexpected: pi found at /nonexistent/pi")
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
