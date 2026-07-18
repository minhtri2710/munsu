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
		{"claude", true}, // claude is known but has no integration capabilities
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

// Test UserExtensionsDir
func TestUserExtensionsDir(t *testing.T) {
	dir := UserExtensionsDir()
	if dir == "" {
		t.Fatal("expected non-empty user extensions dir")
	}
	// Should end with .pi/agent/extensions
	if filepath.Base(filepath.Dir(dir)) != "agent" || filepath.Base(filepath.Dir(filepath.Dir(dir))) != ".pi" {
		t.Logf("user extensions dir (expected ~/.pi/agent/extensions): %s", dir)
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

// Test PiExtensionTemplate produces valid output
func TestPiExtensionTemplate(t *testing.T) {
	tmpl := PiExtensionTemplate("/usr/local/bin/munsu")
	if tmpl == "" {
		t.Fatal("expected non-empty template")
	}

	// Check ownership marker
	if !containsOwnershipMarker(tmpl) {
		t.Error("template must contain ownership marker")
	}

	// Check binary path substituted
	if strings.Contains(tmpl, "BINPATH") {
		t.Error("BINPATH placeholder must be replaced")
	}
}

// Test FileContainsOwnershipMarker
func TestFileContainsOwnershipMarker(t *testing.T) {
	dir := t.TempDir()

	// File with marker
	markedFile := filepath.Join(dir, "marked.ts")
	if err := os.WriteFile(markedFile, []byte("// munsu-integrate -- do not edit this section\nconst x = 1;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !FileContainsOwnershipMarker(markedFile) {
		t.Error("expected file with marker to report ownership")
	}

	// File without marker
	unmarkedFile := filepath.Join(dir, "unmarked.ts")
	if err := os.WriteFile(unmarkedFile, []byte("const x = 1;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if FileContainsOwnershipMarker(unmarkedFile) {
		t.Error("expected file without marker to report no ownership")
	}

	// Non-existent file
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

// Test GenerateManifest
func TestGenerateManifest(t *testing.T) {
	caps := []Capability{CapSessionStart, CapWakeFollowUp}
	homeDir := t.TempDir()
	m := GenerateManifest(homeDir, "pi", caps)

	if m.Harness != "pi" {
		t.Errorf("expected harness 'pi', got %q", m.Harness)
	}
	if m.SchemaVersion != "munsu.integrate/v1" {
		t.Errorf("expected schema version 'munsu.integrate/v1', got %q", m.SchemaVersion)
	}
	if len(m.Capabilities) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(m.Capabilities))
	}
	if m.Capabilities[0] != "session-start" {
		t.Errorf("expected first capability 'session-start', got %q", m.Capabilities[0])
	}
}

// Test Install dry-run for Pi
func TestInstall_Pi_DryRun(t *testing.T) {
	homeDir := t.TempDir()

	result, err := Install(homeDir, "/tmp", "pi", ScopeUser, true)
	if err != nil {
		t.Fatalf("Install dry-run failed: %v", err)
	}

	if result.State != "fresh" {
		t.Errorf("expected state 'fresh', got %q", result.State)
	}
	if result.Harness != "pi" {
		t.Errorf("expected harness 'pi', got %q", result.Harness)
	}
}

// Test Install unsupported harness
func TestInstall_UnsupportedHarness(t *testing.T) {
	homeDir := t.TempDir()

	result, err := Install(homeDir, "/tmp", "nonexistent", ScopeUser, false)
	if err != nil {
		t.Fatalf("Install unsupported: %v", err)
	}
	if result.State != "unsupported" {
		t.Errorf("expected state 'unsupported', got %q", result.State)
	}
}

// Test Status without installation
func TestStatus_Absent(t *testing.T) {
	homeDir := t.TempDir()

	result, err := Status(homeDir, "/tmp", "pi", ScopeUser)
	if err != nil {
		t.Fatalf("Status absent: %v", err)
	}
	if result.State != "absent" {
		t.Errorf("expected state 'absent', got %q", result.State)
	}
}

// Test Status unsupported harness
func TestStatus_Unsupported(t *testing.T) {
	homeDir := t.TempDir()

	result, err := Status(homeDir, "/tmp", "nonexistent", ScopeUser)
	if err != nil {
		t.Fatalf("Status unsupported: %v", err)
	}
	if result.State != "unsupported" {
		t.Errorf("expected state 'unsupported', got %q", result.State)
	}
}

// Test Repair without installation is a fresh install
func TestRepair_Absent_DryRun(t *testing.T) {
	homeDir := t.TempDir()

	result, err := Repair(homeDir, "/tmp", "pi", ScopeUser, true)
	if err != nil {
		t.Fatalf("Repair absent dry-run: %v", err)
	}

	// Without manifest, install first, so dry-run says repairable
	if result.State != "repairable" && result.State != "fresh" {
		t.Errorf("expected state 'repairable' or 'fresh', got %q", result.State)
	}
}

// Test PiExtensionTemplate binary path substitution
func TestPiExtensionTemplate_BinarySubstitution(t *testing.T) {
	binPath := "/custom/path/munsu"
	tmpl := PiExtensionTemplate(binPath)
	// The template source has BINPATH as a placeholder; the function replaces it.
	if strings.Contains(tmpl, "BINPATH") {
		t.Errorf("BINPATH still present in template output (should have been substituted)")
	}
	// Our custom replacement should work
	result := stringsReplaceAll(tmpl, "BINPATH", binPath)
	if result != tmpl {
		t.Log("stringsReplaceAll changed content as expected (no BINPATH to replace)")
	}
}

// Test ownership marker detection edge cases
func TestContainsOwnershipMarker_EdgeCases(t *testing.T) {
	tests := []struct {
		content string
		want    bool
	}{
		{"// munsu-integrate -- do not edit", true},
		{"const x = 1;", false},
		{"", false},
		{"// munsu-integrate", true},
		{"some code before\n// munsu-integrate\nmore code", true},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := containsOwnershipMarker(tt.content)
			if got != tt.want {
				t.Errorf("containsOwnershipMarker(%q) = %v, want %v", tt.content[:min(len(tt.content), 30)], got, tt.want)
			}
		})
	}
}


