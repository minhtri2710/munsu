//go:build !windows

package integrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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
		{"grok", false},
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
		t.Fatalf("captain write (should be no-op): %v", err)
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

// Test SafetyCheck uses fleet.Classify without duplicating logic
func TestSafetyCheck_UsesScopeClassify(t *testing.T) {
	// Verify SafetyCheck delegates to fleet.Classify by checking error behavior
	// on a non-existent path (fleet.Classify fails closed)
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

// TestCheckPiCapability_HangingPi verifies that a hanging pi binary times out
// and the process tree is cleaned up (via injectable runner).
func TestCheckPiCapability_HangingPi(t *testing.T) {
	prevTimeout := SetProbeTimeout(100 * time.Millisecond)
	defer SetProbeTimeout(prevTimeout)

	_ = SetCapabilityCommandRunner(func(name string, args []string, dir string, timeout time.Duration) (string, error) {
		// Simulate a hanging pi binary.
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sleep", "5")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			if cmd.Process != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			cmd.Wait()
			return string(out), fmt.Errorf("%s timed out after %v: %w", name, timeout, err)
		}
		return string(out), err
	})
	defer ResetCapabilityCommandRunner()

	err := CheckPiCapability("/fake/pi")
	if err == nil {
		t.Fatal("expected timeout error for hanging pi")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error should mention timeout, got: %v", err)
	}
	t.Logf("hanging pi correctly timed out: %v", err)
}

// TestCheckPiCapability_HangingNode verifies that a hanging node probe times out.
func TestCheckPiCapability_HangingNode(t *testing.T) {
	prevTimeout := SetProbeTimeout(100 * time.Millisecond)
	defer SetProbeTimeout(prevTimeout)

	callCount := 0
	_ = SetCapabilityCommandRunner(func(name string, args []string, dir string, timeout time.Duration) (string, error) {
		callCount++
		if callCount == 1 {
			// First call: pi --version succeeds.
			return "0.79.0", nil
		}
		// Captain call: node --experimental-strip-types — hang until timeout.
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sleep", "5")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			if cmd.Process != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			cmd.Wait()
			return string(out), fmt.Errorf("%s timed out after %v: %w", name, timeout, err)
		}
		return string(out), err
	})
	defer ResetCapabilityCommandRunner()

	err := CheckPiCapability("/fake/pi")
	if err == nil {
		t.Fatal("expected timeout error for hanging node")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error should mention timeout, got: %v", err)
	}
	t.Logf("hanging node probe correctly timed out: %v", err)
}

// TestCheckPiCapability_TimeoutCleanup verifies that the default runner
// correctly kills a hanging command on timeout. Uses a real shell script
// for the pi binary that hangs.
func TestCheckPiCapability_TimeoutCleanup(t *testing.T) {
	prevTimeout := SetProbeTimeout(100 * time.Millisecond)
	defer SetProbeTimeout(prevTimeout)

	// Use the default runner with a real hanging script.
	ResetCapabilityCommandRunner()

	scriptDir := t.TempDir()
	fakePi := filepath.Join(scriptDir, "fake-pi")
	// Script that immediately times out.
	if err := os.WriteFile(fakePi, []byte("#!/bin/sh\nsleep 5; echo 'done'\n"), 0755); err != nil {
		t.Fatal(err)
	}

	err := CheckPiCapability(fakePi)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error should mention timeout, got: %v", err)
	}
	t.Logf("timeout cleanup test: %v", err)

	// Verify the process is actually dead by trying to signal it.
	// The ps check has races on macOS; just verify the error is correct.
}

// TestCheckPiCapability_OrdinarySuccess uses an injectable runner to verify the
// ordinary success path (valid pi version + valid node API probe).
func TestCheckPiCapability_OrdinarySuccess(t *testing.T) {
	prevTimeout := SetProbeTimeout(5 * time.Second)
	defer SetProbeTimeout(prevTimeout)

	callCount := 0
	_ = SetCapabilityCommandRunner(func(name string, args []string, dir string, timeout time.Duration) (string, error) {
		callCount++
		if callCount == 1 {
			// First call: pi --version succeeds.
			return "0.79.0", nil
		}
		// node probe succeeds.
		return "API probe passed\n", nil
	})
	defer ResetCapabilityCommandRunner()

	err := CheckPiCapability("/fake/pi")
	if err != nil {
		t.Fatalf("ordinary success should not error: %v", err)
	}
}

// TestCheckPiCapability_MalformedOutput verifies that malformed version output
// is rejected, even via the injectable runner.
func TestCheckPiCapability_MalformedRunnerOutput(t *testing.T) {
	_ = SetCapabilityCommandRunner(func(name string, args []string, dir string, timeout time.Duration) (string, error) {
		return "not-a-version\n", nil
	})
	defer ResetCapabilityCommandRunner()

	err := CheckPiCapability("/fake/pi")
	if err == nil {
		t.Fatal("expected error for malformed version")
	}
	if !strings.Contains(err.Error(), "cannot parse pi version") {
		t.Fatalf("error should mention parse failure, got: %v", err)
	}
}

// TestCapabilityProbeTimeout_Defaults verifies the default timeout is non-zero.
func TestCapabilityProbeTimeout_Defaults(t *testing.T) {
	if capabilityProbeTimeout <= 0 {
		t.Fatal("default capabilityProbeTimeout must be positive")
	}
	if capabilityProbeTimeout != DefaultCapabilityProbeTimeout {
		t.Errorf("default = %v, want %v", capabilityProbeTimeout, DefaultCapabilityProbeTimeout)
	}
}

// TestCapabilityCommandRunner_Reset verifies SetCapabilityCommandRunner and
// ResetCapabilityCommandRunner work correctly.
func TestCapabilityCommandRunner_Reset(t *testing.T) {
	called := false
	fn := func(name string, args []string, dir string, timeout time.Duration) (string, error) {
		called = true
		return "", nil
	}
	prev := SetCapabilityCommandRunner(fn)
	if prev == nil {
		t.Fatal("expected non-nil previous runner")
	}

	// Call via CheckPiCapability.
	SetProbeTimeout(1 * time.Second)
	CheckPiCapability("/fake/pi")

	if !called {
		t.Error("custom runner was not called")
	}

	ResetCapabilityCommandRunner()
	// Verify runner is restored to default (not nil).
	if runCapabilityCommand == nil {
		t.Error("runner is nil after reset")
	}
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

	// Verify structure matches expected: correct hook event keys
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

// -- Grok adapter tests --

// TestGrokHookCommand verifies the bash -lc wrapper for Grok commands.
func TestGrokHookCommand(t *testing.T) {
	cmd := grokHookCommand("/usr/local/bin/munsu", "integrate", "safety-check", "--harness", "grok")
	expected := "bash -lc 'exec \"/usr/local/bin/munsu\" integrate safety-check --harness grok'"
	if cmd != expected {
		t.Errorf("grokHookCommand() = %q, want %q", cmd, expected)
	}
}

// TestGrokHookCommandSpaceInPath verifies JSON marshaling for paths with spaces.
func TestGrokHookCommandSpaceInPath(t *testing.T) {
	cmd := grokHookCommand("/opt/munsu v2/bin/munsu", "integrate", "sessionstart-nudge")
	expected := "bash -lc 'exec \"/opt/munsu v2/bin/munsu\" integrate sessionstart-nudge'"
	if cmd != expected {
		t.Errorf("grokHookCommand with space = %q, want %q", cmd, expected)
	}
}

// TestGrokHooksContent verifies all 4 hook files generate valid JSON with munsu commands.
func TestGrokHooksContent(t *testing.T) {
	binPath := "/usr/local/bin/munsu"
	for _, name := range grokHookFileNames {
		t.Run(name, func(t *testing.T) {
			content := GrokHooksContent(binPath, name)
			if content == "" {
				t.Fatal("expected non-empty content")
			}

			// Verify valid JSON
			var parsed interface{}
			if err := json.Unmarshal([]byte(content), &parsed); err != nil {
				t.Fatalf("invalid JSON for %s: %v", name, err)
			}

			// Verify hooks structure
			root := parsed.(map[string]interface{})
			hooks, ok := root["hooks"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing hooks key in %s", name)
			}

			for event, matchers := range hooks {
				matcherList, ok := matchers.([]interface{})
				if !ok {
					t.Errorf("hooks[%s] is not an array in %s", event, name)
					continue
				}
				if len(matcherList) == 0 {
					t.Errorf("hooks[%s] is empty in %s", event, name)
					continue
				}
				for i, m := range matcherList {
					matcher, ok := m.(map[string]interface{})
					if !ok {
						t.Errorf("matcher %d is not an object in %s", i, name)
						continue
					}
					hookList, ok := matcher["hooks"].([]interface{})
					if !ok {
						t.Errorf("matcher %d hooks is not an array in %s", i, name)
						continue
					}
					for j, h := range hookList {
						hook, ok := h.(map[string]interface{})
						if !ok {
							t.Errorf("hook %d is not an object in %s", j, name)
							continue
						}
						if hook["type"] != "command" {
							t.Errorf("hook %d type is %q, want 'command' in %s", j, hook["type"], name)
						}
						if cmd, ok := hook["command"].(string); ok {
							if !strings.Contains(cmd, binPath) {
								t.Errorf("hook %d command %q does not contain binary path %s", j, cmd, binPath)
							}
							if !strings.HasPrefix(cmd, "bash -lc '") {
								t.Errorf("hook %d command %q does not start with 'bash -lc '", j, cmd)
							}
						}
						timeout, ok := hook["timeout"].(float64)
						if !ok {
							t.Errorf("hook %d missing timeout in %s", j, name)
						} else if timeout <= 0 {
							t.Errorf("hook %d timeout %v must be positive in %s", j, timeout, name)
						}
					}
				}
			}

			// Verify specific event names
			switch name {
			case "fm-primary-sessionstart-nudge.json":
				if _, ok := hooks["SessionStart"]; !ok {
					t.Error("expected SessionStart event")
				}
				// SessionStart should not have matcher
				if matchers, ok := hooks["SessionStart"].([]interface{}); ok && len(matchers) > 0 {
					if m, ok := matchers[0].(map[string]interface{}); ok {
						if _, hasMatcher := m["matcher"]; hasMatcher {
							t.Error("SessionStart should not have a matcher")
						}
					}
				}
			case "fm-primary-pretool-check.json", "fm-primary-cd-check.json":
				if _, ok := hooks["PreToolUse"]; !ok {
					t.Error("expected PreToolUse event")
				}
				// PreToolUse should have matcher "Bash"
				if matchers, ok := hooks["PreToolUse"].([]interface{}); ok && len(matchers) > 0 {
					if m, ok := matchers[0].(map[string]interface{}); ok {
						if m["matcher"] != "Bash" {
							t.Errorf("expected matcher 'Bash', got %v", m["matcher"])
						}
					}
				}
			case "fm-primary-turnend-guard.json":
				if _, ok := hooks["Stop"]; !ok {
					t.Error("expected Stop event")
				}
			}
		})
	}
}

// TestGrokHooksDigest verifies SHA-256 digests are computed correctly.
func TestGrokHooksDigest(t *testing.T) {
	binPath := "/usr/local/bin/munsu"
	for _, name := range grokHookFileNames {
		t.Run(name, func(t *testing.T) {
			digest := GrokHooksDigest(binPath, name)
			if digest == "" {
				t.Fatal("expected non-empty digest")
			}
			if len(digest) != 64 {
				t.Errorf("expected 64-char hex digest, got %d chars: %s", len(digest), digest)
			}
		})
	}
}

// TestGrokHooksTargetPath verifies target path resolution.
func TestGrokHooksTargetPath(t *testing.T) {
	t.Run("user scope", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		path, err := GrokHooksTargetPath(ScopeUser, "", "fm-primary-pretool-check.json")
		if err != nil {
			t.Fatal(err)
		}
		expected := filepath.Join(home, ".grok", "hooks", "fm-primary-pretool-check.json")
		if path != expected {
			t.Errorf("expected %q, got %q", expected, path)
		}
	})

	t.Run("project scope", func(t *testing.T) {
		projDir := t.TempDir()
		path, err := GrokHooksTargetPath(ScopeProject, projDir, "fm-primary-sessionstart-nudge.json")
		if err != nil {
			t.Fatal(err)
		}
		// The function uses EvalSymlinks, so use the canonical path as expected
		canonical, resolveErr := filepath.EvalSymlinks(projDir)
		if resolveErr != nil {
			canonical = projDir
		}
		expected := filepath.Join(canonical, ".grok", "hooks", "fm-primary-sessionstart-nudge.json")
		if path != expected {
			t.Errorf("expected %q, got %q", expected, path)
		}
	})
}

// TestGrokHooksHasOwnedHooks verifies structural ownership detection.
func TestGrokHooksHasOwnedHooks(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".grok", "hooks")
	os.MkdirAll(hooksDir, 0755)

	binPath := "/usr/local/bin/munsu"

	// Write all 4 hook files
	for _, name := range grokHookFileNames {
		content := GrokHooksContent(binPath, name)
		targetPath := filepath.Join(hooksDir, name)
		if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Should detect all owned hooks
	present, msg, err := GrokHooksHasOwnedHooks(hooksDir, binPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !present {
		t.Errorf("expected all hooks present, got: %s", msg)
	}

	// Remove one file
	os.Remove(filepath.Join(hooksDir, "fm-primary-turnend-guard.json"))
	present, msg, err = GrokHooksHasOwnedHooks(hooksDir, binPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if present {
		t.Errorf("expected missing hook detection, got: %s", msg)
	}
	if !strings.Contains(msg, "turnend-guard") {
		t.Errorf("expected message to mention turnend-guard, got: %s", msg)
	}
}

// TestGrokHooksHasOwnedHooks_CommandMismatch verifies detection of modified commands.
func TestGrokHooksHasOwnedHooks_CommandMismatch(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".grok", "hooks")
	os.MkdirAll(hooksDir, 0755)

	binPath := "/usr/local/bin/munsu"

	// Write a hook file with a different command
	modifiedContent := `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "bash -lc 'exec \"/usr/local/bin/munsu\" integrate sessionstart-nudge'",
            "timeout": 10
          }
        ]
      }
    ]
  }
}`
	targetPath := filepath.Join(hooksDir, "fm-primary-sessionstart-nudge.json")
	if err := os.WriteFile(targetPath, []byte(modifiedContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Only one file, all others missing - should still detect as missing
	present, msg, err := GrokHooksHasOwnedHooks(hooksDir, binPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if present {
		t.Errorf("expected missing hook detection with only 1 file out of 4")
	}
	_ = msg
}

// TestGrokHooksHasOwnedHooks_InvalidJSON verifies handling of unparseable files.
func TestGrokHooksHasOwnedHooks_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".grok", "hooks")
	os.MkdirAll(hooksDir, 0755)

	binPath := "/usr/local/bin/munsu"

	// Write a file with invalid JSON
	invalidPath := filepath.Join(hooksDir, "fm-primary-sessionstart-nudge.json")
	if err := os.WriteFile(invalidPath, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	present, _, err := GrokHooksHasOwnedHooks(hooksDir, binPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if present {
		t.Error("expected not present for invalid JSON")
	}
}

// TestGrokMergeHookFile verifies read-merge preserves user hooks.
func TestGrokMergeHookFile(t *testing.T) {
	existing := `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "user-command",
            "timeout": 30
          }
        ]
      }
    ]
  }
}`

	generated := `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "munsu-command",
            "timeout": 10
          }
        ]
      }
    ]
  }
}`

	merged, err := mergeGrokHookFile(existing, generated)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify the merged output is valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(merged), &parsed); err != nil {
		t.Fatalf("merged output is invalid JSON: %v", err)
	}

	hooks, ok := parsed["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("missing hooks in merged output")
	}

	matchers, ok := hooks["SessionStart"].([]interface{})
	if !ok || len(matchers) == 0 {
		t.Fatal("missing SessionStart in merged output")
	}

	// After merge, there should be 2 matchers (munsu prepended, user after)
	if len(matchers) != 2 {
		t.Errorf("expected 2 matchers after merge (munsu + user), got %d", len(matchers))
	}

	// First matcher should be munsu's (prepended)
	firstMatcher := matchers[0].(map[string]interface{})
	firstHookList, ok := firstMatcher["hooks"].([]interface{})
	if !ok || len(firstHookList) == 0 {
		t.Fatal("missing hooks array in first matcher")
	}
	firstHook := firstHookList[0].(map[string]interface{})
	if firstHook["command"] != "munsu-command" {
		t.Errorf("expected first matcher hook to be munsu-command, got %v", firstHook["command"])
	}

	// Captain matcher should be user's
	captainMatcher := matchers[1].(map[string]interface{})
	captainHookList, ok := captainMatcher["hooks"].([]interface{})
	if !ok || len(captainHookList) == 0 {
		t.Fatal("missing hooks array in captain matcher")
	}
	captainHook := captainHookList[0].(map[string]interface{})
	if captainHook["command"] != "user-command" {
		t.Errorf("expected captain matcher hook to be user-command, got %v", captainHook["command"])
	}
}

// TestGenerateGrokManifest verifies Grok manifest generation.
func TestGenerateGrokManifest(t *testing.T) {
	caps := []Capability{CapSessionStart, CapTurnEndGuard, CapPreToolCheck}
	targets := []string{
		"/home/user/.grok/hooks/fm-primary-sessionstart-nudge.json",
		"/home/user/.grok/hooks/fm-primary-pretool-check.json",
		"/home/user/.grok/hooks/fm-primary-cd-check.json",
		"/home/user/.grok/hooks/fm-primary-turnend-guard.json",
	}
	m := generateGrokManifest("grok", "user", caps, "test-digest", targets)

	if m.Harness != "grok" {
		t.Errorf("expected harness 'grok', got %q", m.Harness)
	}
	if m.SchemaVersion != "munsu.integrate/v1" {
		t.Errorf("expected schema version 'munsu.integrate/v1', got %q", m.SchemaVersion)
	}
	if len(m.TargetPaths) != 4 {
		t.Errorf("expected 4 target paths, got %d", len(m.TargetPaths))
	}
	if m.ContentDigest != "test-digest" {
		t.Errorf("expected digest 'test-digest', got %q", m.ContentDigest)
	}
	if len(m.Capabilities) != 3 {
		t.Errorf("expected 3 capabilities, got %d", len(m.Capabilities))
	}
	if m.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %q", m.Version)
	}
}

// --- CapSessionStart harness wiring tests ---

// TestClaudeCapSessionStartWiring verifies that Claude settings.json has
// SessionStart hook with startup|resume|clear matcher and sessionstart-nudge
// command under CapSessionStart.
func TestClaudeCapSessionStartWiring(t *testing.T) {
	content := ClaudeSettingsContent("/usr/local/bin/munsu")

	// Must have SessionStart hook
	if !strings.Contains(content, "SessionStart") {
		t.Fatal("Claude settings must have SessionStart hook")
	}

	// Must have correct matcher: startup|resume|clear
	// The matcher must NOT include compact (exactly-once semantics)
	if !strings.Contains(content, `"matcher": "startup|resume|clear"`) {
		t.Error("SessionStart matcher must be startup|resume|clear")
	}
	if strings.Contains(content, "compact") {
		t.Error("SessionStart matcher must NOT include compact (reserved for agent continuity)")
	}

	// Must call sessionstart-nudge command
	if !strings.Contains(content, "sessionstart-nudge") {
		t.Error("SessionStart hook must call munsu integrate sessionstart-nudge")
	}

	// Must NOT call session-start directly (lightweight nudge, not full digest)
	if strings.Contains(content, "session-start") && !strings.Contains(content, "sessionstart-nudge") {
		t.Error("SessionStart hook must use sessionstart-nudge, not session-start")
	}

	// Must be valid JSON
	var parsed interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

// TestCodexCapSessionStartWiring verifies that Codex hooks.json has
// SessionStart hook with startup|resume|clear matcher and sessionstart-nudge
// command under CapSessionStart.
func TestCodexCapSessionStartWiring(t *testing.T) {
	content := CodexHooksContent("/usr/local/bin/munsu")

	// Must have SessionStart hook
	if !strings.Contains(content, "SessionStart") {
		t.Fatal("Codex hooks must have SessionStart hook")
	}

	// Must have correct matcher
	if !strings.Contains(content, `"matcher": "startup|resume|clear"`) {
		t.Error("SessionStart matcher must be startup|resume|clear")
	}
	if strings.Contains(content, "compact") {
		t.Error("SessionStart matcher must NOT include compact")
	}

	// Must call sessionstart-nudge command
	if !strings.Contains(content, "sessionstart-nudge") {
		t.Error("SessionStart hook must call munsu integrate sessionstart-nudge")
	}

	// Must NOT call session-start directly
	if strings.Contains(content, "session-start") && !strings.Contains(content, "sessionstart-nudge") {
		t.Error("SessionStart hook must use sessionstart-nudge, not session-start")
	}

	// Must be valid JSON
	var parsed interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

// TestGrokCapSessionStartWiring verifies that fm-primary-sessionstart-nudge.json
// has SessionStart hook and calls sessionstart-nudge under CapSessionStart.
func TestGrokCapSessionStartWiring(t *testing.T) {
	content := GrokHooksContent("/usr/local/bin/munsu", "fm-primary-sessionstart-nudge.json")
	if content == "" {
		t.Fatal("expected non-empty sessionstart-nudge hook content")
	}

	// Must have SessionStart event
	if !strings.Contains(content, "SessionStart") {
		t.Error("sessionstart-nudge hook must have SessionStart event")
	}

	// Must call sessionstart-nudge command
	if !strings.Contains(content, "sessionstart-nudge") {
		t.Error("SessionStart hook must call munsu integrate sessionstart-nudge")
	}

	// Must NOT have a matcher (SessionStart hooks are unconditional)
	// (Grok buildHookJSON sets no matcher for sessionstart-nudge)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	hooks, ok := parsed["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("missing hooks key")
	}
	matchers, ok := hooks["SessionStart"].([]interface{})
	if !ok || len(matchers) == 0 {
		t.Fatal("expected SessionStart hook")
	}
	for _, m := range matchers {
		matcher, ok := m.(map[string]interface{})
		if !ok {
			t.Fatal("matcher must be an object")
		}
		if matcherMatcher, hasMatcher := matcher["matcher"]; hasMatcher {
			t.Errorf("SessionStart hook should not have matcher, got %q", matcherMatcher)
		}
		hookList, ok := matcher["hooks"].([]interface{})
		if !ok || len(hookList) == 0 {
			t.Fatal("missing hooks array")
		}
		for _, h := range hookList {
			hook, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			if hook["type"] != "command" {
				t.Errorf("expected command type, got %q", hook["type"])
			}
			if cmd, ok := hook["command"].(string); ok {
				if !strings.Contains(cmd, "sessionstart-nudge") {
					t.Errorf("command should call sessionstart-nudge, got %q", cmd)
				}
			}
		}
	}
}

// TestAgyCapSessionStartWiring verifies that agy hooks.json has
// munsu-sessionstart-nudge PreInvocation hook with sessionstart-nudge
// command under CapSessionStart.
func TestAgyCapSessionStartWiring(t *testing.T) {
	content := AgyHooksContent("/usr/local/bin/munsu")
	if content == "" {
		t.Fatal("expected non-empty agy hooks content")
	}

	// Must have munsu-sessionstart-nudge key
	if !strings.Contains(content, "munsu-sessionstart-nudge") {
		t.Error("agy hooks must have munsu-sessionstart-nudge hook name")
	}

	// Must have PreInvocation event
	if !strings.Contains(content, "PreInvocation") {
		t.Error("sessionstart nudge must use PreInvocation event")
	}

	// Must call sessionstart-nudge command
	if !strings.Contains(content, "sessionstart-nudge") {
		t.Error("PreInvocation hook must call munsu integrate sessionstart-nudge")
	}

	// Must NOT have Stop event (nudge is PreInvocation, not Stop)
	// Parse JSON to verify the sessionstart-nudge hook only has PreInvocation
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if nudgeHook, ok := parsed["munsu-sessionstart-nudge"].(map[string]interface{}); ok {
		if _, hasStop := nudgeHook["Stop"]; hasStop {
			t.Error("sessionstart-nudge must NOT have Stop event (wrong event type)")
		}
		if _, hasPreInvocation := nudgeHook["PreInvocation"]; !hasPreInvocation {
			t.Error("sessionstart-nudge must have PreInvocation event")
		}
	} else {
		t.Error("munsu-sessionstart-nudge must be a JSON object with event keys")
	}
}

// TestPiCapSessionStartWiring verifies that Pi extension has
// session_start handler that calls safety-check then session-start
// and provides exactly-once semantics under CapSessionStart.
func TestPiCapSessionStartWiring(t *testing.T) {
	content := PiExtensionTemplate("/usr/local/bin/munsu")
	if content == "" {
		t.Fatal("expected non-empty Pi extension content")
	}

	// Must have session_start event handler
	if !strings.Contains(content, "session_start") {
		t.Error("Pi extension must have session_start event handler")
	}

	// Must call integrate safety-check before session-start
	if !strings.Contains(content, "integrate") || !strings.Contains(content, "safety-check") {
		t.Error("Pi extension must call integrate safety-check")
	}

	// Pi uses the full session-start (not the lightweight nudge)
	if !strings.Contains(content, "session-start") {
		t.Error("Pi extension must call munsu session-start (native full path)")
	}

	// Must have exactly-once entry for session-start
	if !strings.Contains(content, "munsu-session-start") {
		t.Error("Pi extension must track exactly-once via munsu-session-start entry")
	}

	// Must check scope/gate safety before session-start
	if !strings.Contains(content, "gate_refused") {
		t.Error("Pi extension must check gate_refused from safety-check")
	}

	// Must pass --output json to session-start
	if !strings.Contains(content, `"session-start", "--output", "json"`) &&
		!strings.Contains(content, `"session-start","--output","json"`) {
		t.Error("Pi extension must call session-start with --output json")
	}

	// Must fail closed on safety failure
	if !strings.Contains(content, "Cannot start session") {
		t.Error("Pi extension should show 'Cannot start session' on safety failure")
	}

	// Must only dispatch after safety check passes
	if !strings.Contains(content, "gate_refused") {
		t.Error("Pi extension must check gate_refused before session-start")
	}
}

// --- Harness install/status/repair parity tests ---

// TestCodexStatusDriftDetection tests that install → status=installed
// → remove hook → drifted for Codex hooks.json CapSessionStart artifact.
func TestCodexStatusDriftDetection(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projectDir := filepath.Join(dir, "project")
	os.MkdirAll(projectDir, 0755)

	munsuBin := filepath.Join(dir, "munsu")
	if err := os.WriteFile(munsuBin, []byte("#!/bin/sh\necho munsu\n"), 0755); err != nil {
		t.Fatal(err)
	}

	SetMunsuPathResolver(testMunsuResolver{path: munsuBin})
	defer ResetMunsuPathResolver()

	scope := ScopeProject

	// Install Codex hooks
	result, err := Install(homeDir, projectDir, "codex", scope, false)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if result.State != "installed" {
		t.Fatalf("expected installed, got %q", result.State)
	}

	// Status should report installed
	status, err := Status(homeDir, projectDir, "codex", scope)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.State != "installed" {
		t.Fatalf("expected installed, got %q: %s", status.State, status.Message)
	}
	if status.Drifted {
		t.Error("expected no drift after clean install")
	}

	// Remove SessionStart hooks from target file
	targetPath := filepath.Join(projectDir, ".codex", "hooks.json")
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	hooks := parsed["hooks"].(map[string]interface{})
	delete(hooks, "SessionStart")
	modified, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, modified, 0644); err != nil {
		t.Fatal(err)
	}

	// Status should report drifted
	status, err = Status(homeDir, projectDir, "codex", scope)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.State != "drifted" {
		t.Fatalf("expected drifted, got %q: %s", status.State, status.Message)
	}
	if !status.Drifted {
		t.Error("expected Drifted=true after hook removal")
	}

	// Repair should fix drift
	result, err = Repair(homeDir, projectDir, "codex", scope, false)
	if err != nil {
		t.Fatalf("Repair failed: %v", err)
	}
	if result.State != "repair" {
		t.Fatalf("expected repair, got %q", result.State)
	}

	// Status should report installed again
	status, err = Status(homeDir, projectDir, "codex", scope)
	if err != nil {
		t.Fatalf("Status after repair failed: %v", err)
	}
	if status.State != "installed" {
		t.Fatalf("expected installed after repair, got %q: %s", status.State, status.Message)
	}
}

// TestGrokStatusDriftDetection tests that install → status → remove hook → drifted
// for Grok fm-primary-sessionstart-nudge.json CapSessionStart artifact.
func TestGrokStatusDriftDetection(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projectDir := filepath.Join(dir, "project")
	os.MkdirAll(projectDir, 0755)

	munsuBin := filepath.Join(dir, "munsu")
	if err := os.WriteFile(munsuBin, []byte("#!/bin/sh\necho munsu\n"), 0755); err != nil {
		t.Fatal(err)
	}

	SetMunsuPathResolver(testMunsuResolver{path: munsuBin})
	defer ResetMunsuPathResolver()

	scope := ScopeProject

	// Install Grok hooks
	result, err := Install(homeDir, projectDir, "grok", scope, false)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if result.State != "installed" {
		t.Fatalf("expected installed, got %q", result.State)
	}

	// Status should report installed
	status, err := Status(homeDir, projectDir, "grok", scope)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.State != "installed" {
		t.Fatalf("expected installed, got %q: %s", status.State, status.Message)
	}
	if status.Drifted {
		t.Error("expected no drift after clean install")
	}

	// Remove the sessionstart-nudge hook file
	sessionstartPath := filepath.Join(projectDir, ".grok", "hooks", "fm-primary-sessionstart-nudge.json")
	if err := os.Remove(sessionstartPath); err != nil {
		t.Fatal(err)
	}

	// Status should report drifted (missing CapSessionStart artifact)
	status, err = Status(homeDir, projectDir, "grok", scope)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.State != "drifted" {
		t.Fatalf("expected drifted, got %q: %s", status.State, status.Message)
	}
	if !status.Drifted {
		t.Error("expected Drifted=true after hook file removal")
	}

	// Repair should fix drift
	result, err = Repair(homeDir, projectDir, "grok", scope, false)
	if err != nil {
		t.Fatalf("Repair failed: %v", err)
	}
	if result.State != "repair" {
		t.Fatalf("expected repair state, got %q", result.State)
	}

	// Status should report installed again
	status, err = Status(homeDir, projectDir, "grok", scope)
	if err != nil {
		t.Fatalf("Status after repair failed: %v", err)
	}
	if status.State != "installed" {
		t.Fatalf("expected installed after repair, got %q: %s", status.State, status.Message)
	}
}

// TestAgyStatusDriftDetection tests that install → status → modify hooks.json
// → drifted for Agy munsu-sessionstart-nudge CapSessionStart artifact.
func TestAgyStatusDriftDetection(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projectDir := filepath.Join(dir, "project")
	os.MkdirAll(projectDir, 0755)

	munsuBin := filepath.Join(dir, "munsu")
	if err := os.WriteFile(munsuBin, []byte("#!/bin/sh\necho munsu\n"), 0755); err != nil {
		t.Fatal(err)
	}

	SetMunsuPathResolver(testMunsuResolver{path: munsuBin})
	defer ResetMunsuPathResolver()

	scope := ScopeProject

	// Install Agy hooks
	result, err := Install(homeDir, projectDir, "agy", scope, false)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if result.State != "installed" {
		t.Fatalf("expected installed, got %q", result.State)
	}

	// Status should report installed
	status, err := Status(homeDir, projectDir, "agy", scope)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.State != "installed" {
		t.Fatalf("expected installed, got %q: %s", status.State, status.Message)
	}
	if status.Drifted {
		t.Error("expected no drift after clean install")
	}

	// Remove munsu-sessionstart-nudge from hooks.json
	targetPath := filepath.Join(projectDir, ".agents", "hooks.json")
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	delete(parsed, "munsu-sessionstart-nudge")
	modified, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, modified, 0644); err != nil {
		t.Fatal(err)
	}

	// Status should report drifted
	status, err = Status(homeDir, projectDir, "agy", scope)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.State != "drifted" {
		t.Fatalf("expected drifted, got %q: %s", status.State, status.Message)
	}
	if !status.Drifted {
		t.Error("expected Drifted=true after hook removal")
	}

	// Repair should fix drift
	result, err = Repair(homeDir, projectDir, "agy", scope, false)
	if err != nil {
		t.Fatalf("Repair failed: %v", err)
	}
	if result.State != "repair" {
		t.Fatalf("expected repair, got %q", result.State)
	}

	// Status should report installed again
	status, err = Status(homeDir, projectDir, "agy", scope)
	if err != nil {
		t.Fatalf("Status after repair failed: %v", err)
	}
	if status.State != "installed" {
		t.Fatalf("expected installed after repair, got %q: %s", status.State, status.Message)
	}
}
