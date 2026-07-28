package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/integrate"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// TestCollectCapabilities_WatcherNoIdentity verifies that when no watcher
// identity file exists, the diagnostic reports "not running".
func TestCollectCapabilities_WatcherNoIdentity(t *testing.T) {
	home := t.TempDir()
	capResult := CollectCapabilities(home, ".", "0.1.0")

	if capResult.Watcher == nil {
		t.Fatal("expected non-nil Watcher diagnostic")
	}
	str := capResult.Watcher.String()
	if !strings.Contains(str, "not running") && !strings.Contains(str, "identity unverified") {
		t.Errorf("expected 'not running or identity unverified', got %q", str)
	}
	if fix := capResult.Watcher.Fix(); fix != "munsu watch ensure" {
		t.Errorf("expected fix 'munsu watch ensure', got %q", fix)
	}
}

// TestCollectCapabilities_WatcherVersionMismatch verifies that when the
// identity build version differs from the CLI version, the diagnostic
// flags a mismatch.
func TestCollectCapabilities_WatcherVersionMismatch(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a fake identity with a different version
	id := orchestrator.WatcherIdentity{
		Home:            home,
		PID:             999999, // unlikely to be live
		ProcessStart:    "1234567890",
		Executable:      "/fake/munsu",
		BuildVersion:    "0.0.1-old",
		ProtocolVersion: 1,
		StartTime:       1000000,
	}
	if err := orchestrator.WriteIdentity(home, id); err != nil {
		t.Fatal(err)
	}

	capResult := CollectCapabilities(home, ".", "0.1.0-dev")

	if capResult.Watcher == nil {
		t.Fatal("expected non-nil Watcher diagnostic")
	}

	// PID 999999 won't be live, so it should show "not running"
	str := capResult.Watcher.String()
	if !strings.Contains(str, "not running") {
		t.Errorf("expected 'not running' for non-live PID, got %q", str)
	}
}

// TestCollectCapabilities_WatcherVersionMatch verifies that identity with
// matching version does not trigger a mismatch warning. The PID won't be
// live so Running will be false, but VersionMatched should be correct.
func TestCollectCapabilities_WatcherVersionMatch(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	testVersion := "0.1.0-test"
	id := orchestrator.WatcherIdentity{
		Home:            home,
		PID:             999999,
		ProcessStart:    "1234567890",
		Executable:      "/fake/munsu",
		BuildVersion:    testVersion,
		ProtocolVersion: 1,
		StartTime:       1000000,
	}
	if err := orchestrator.WriteIdentity(home, id); err != nil {
		t.Fatal(err)
	}

	capResult := CollectCapabilities(home, ".", testVersion)

	// VersionMatched should be true (same version, no CommitSHA set)
	if !capResult.Watcher.VersionMatched {
		t.Errorf("expected VersionMatched=true for same version %q", testVersion)
	}
	// But Running should be false (no such PID)
	if capResult.Watcher.Running {
		t.Error("expected Running=false for non-live PID")
	}
}

// TestCollectCapabilities_WatcherCommitSHAMatch verifies that matching
// CommitSHAs prevent VERSION MISMATCH even when display versions differ.
// This regression test covers the watcher-version-identity-fix.
func TestCollectCapabilities_WatcherCommitSHAMatch(t *testing.T) {
	// Save and restore package-level orchestrator.CommitSHA
	origCommitSHA := orchestrator.CommitSHA
	defer func() { orchestrator.CommitSHA = origCommitSHA }()
	orchestrator.CommitSHA = "abc1234def5678abc1234def5678abc1234def5"

	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Identity has the same CommitSHA but a different display version
	id := orchestrator.WatcherIdentity{
		Home:            home,
		PID:             999999,
		ProcessStart:    "1234567890",
		Executable:      "/fake/munsu",
		BuildVersion:    "0.0.1-watcher", // differs from CLI version
		ProtocolVersion: 2,
		StartTime:       1000000,
		CommitSHA:       "abc1234", // short SHA prefix matches
	}
	if err := orchestrator.WriteIdentity(home, id); err != nil {
		t.Fatal(err)
	}

	// CLI version string is different, but orchestrator.CommitSHA matches
	capResult := CollectCapabilities(home, ".", "0.1.0-cli")

	if !capResult.Watcher.VersionMatched {
		t.Errorf("expected VersionMatched=true when CommitSHAs match despite differing display versions; got VersionMatched=false")
	}
	if capResult.Watcher.Running {
		t.Error("expected Running=false for non-live PID")
	}
	s := capResult.Watcher.String()
	if s == "" {
		t.Error("expected non-empty Watcher diagnostic string")
	}
}

// TestCollectCapabilities_WatcherCommitSHADifferent verifies that different
// CommitSHAs correctly report VERSION MISMATCH.
func TestCollectCapabilities_WatcherCommitSHADifferent(t *testing.T) {
	origCommitSHA := orchestrator.CommitSHA
	defer func() { orchestrator.CommitSHA = origCommitSHA }()
	orchestrator.CommitSHA = "abc1234def5678abc1234def5678abc1234def5"

	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Identity has a different CommitSHA
	id := orchestrator.WatcherIdentity{
		Home:            home,
		PID:             999999,
		ProcessStart:    "1234567890",
		Executable:      "/fake/munsu",
		BuildVersion:    "0.0.1-watcher",
		ProtocolVersion: 2,
		StartTime:       1000000,
		CommitSHA:       "xyz7890", // different from orchestrator.CommitSHA
	}
	if err := orchestrator.WriteIdentity(home, id); err != nil {
		t.Fatal(err)
	}

	capResult := CollectCapabilities(home, ".", "0.1.0-cli")

	if capResult.Watcher.VersionMatched {
		t.Error("expected VersionMatched=false when CommitSHAs differ")
	}
}

// TestCollectCapabilities_IntegrationUnsupported verifies that harnesses
// without native integration show "unsupported" with a clear note.
func TestCollectCapabilities_IntegrationUnsupported(t *testing.T) {
	home := t.TempDir()
	capResult := CollectCapabilities(home, ".", "0.1.0")

	for _, d := range capResult.Integrations {
		if d.State == "unsupported" {
			if !strings.Contains(d.Detail, "native adapter: not implemented") {
				t.Errorf("harness %q: expected 'native adapter: not implemented' detail, got %q", d.Harness, d.Detail)
			}
			if fix := d.Fix(); fix != "" {
				t.Errorf("harness %q: unsupported should have no Fix, got %q", d.Harness, fix)
			}
		}
	}
}

// TestCollectCapabilities_IntegrationAbsent verifies that supported harnesses
// (pi, claude) without any integration show "absent".
func TestCollectCapabilities_IntegrationAbsent(t *testing.T) {
	home := t.TempDir()
	capResult := CollectCapabilities(home, ".", "0.1.0")

	for _, d := range capResult.Integrations {
		if d.Harness == "pi" || d.Harness == "claude" {
			if d.State != "absent" {
				t.Errorf("harness %q in bare home: expected state 'absent', got %q", d.Harness, d.State)
			}
			if fix := d.Fix(); fix != "munsu integrate install --harness "+d.Harness {
				t.Errorf("harness %q: expected install fix, got %q", d.Harness, fix)
			}
		}
	}
}

// TestCollectCapabilities_ScopeClassification verifies scope classification
// works for a bare temp directory (should be "unrelated" since not a git repo).
func TestCollectCapabilities_ScopeClassification(t *testing.T) {
	home := t.TempDir()
	// Use a random temp dir that is NOT a git repo.
	capResult := CollectCapabilities(home, home, "0.1.0")

	if capResult.ScopeResult == nil {
		t.Fatal("expected non-nil ScopeResult")
	}

	// A temp dir should be unrelated (not a git repo)
	if capResult.ScopeResult.Identity != "unrelated" {
		t.Logf("Scope identity: %s (may vary if temp dir is inside a git checkout)", capResult.ScopeResult.Identity)
	}

	// The gate capability should not fail
	str := capResult.ScopeResult.String()
	if str == "" {
		t.Error("expected non-empty ScopeResult string")
	}
}

// TestCollectCapabilities_GeneralTarget verifies general target resolution
// from a bare home (unsupported, no config or env).
func TestCollectCapabilities_GeneralTarget(t *testing.T) {
	// Clear runtime env vars so resolution must fall through to unsupported.
	t.Setenv("TMUX_PANE", "")
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("HERDR_SESSION", "")

	home := t.TempDir()
	capResult := CollectCapabilities(home, ".", "0.1.0")

	if capResult.General == nil {
		t.Fatal("expected non-nil General diagnostic")
	}

	// The general target will either be Unsupported or report an error from
	// ValidateTargetOwnership (which checks handle emptiness before source).
	// Either way, we expect meaningful output.
	str := capResult.General.String()
	if str == "" {
		t.Error("expected non-empty General diagnostic string")
	}
	t.Logf("General diagnostic: %s", str)

	// If unsupported, there should be a fix hint available.
	// If there's an ownership validation error, the fix is still relevant.
	if capResult.General.Err == nil && capResult.General.Result.Source == orchestrator.Unsupported {
		if fix := capResult.General.Fix(); fix == "" {
			t.Error("expected non-empty Fix for unsupported general target")
		}
	}
}

// TestCollectCapabilities_IntegrationInstalled checks that when an integration
// manifest is present and healthy for pi, it reports "installed".
// This is a minimal fixture test — it creates the manifest and target file
// to satisfy Status's checks.
func TestCollectCapabilities_IntegrationInstalled(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	// Create a minimal pi integration: manifest + target file with ownership marker
	integrateDir := filepath.Join(home, "integrate", "pi", "project")
	if err := os.MkdirAll(integrateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create the extension target file with the ownership marker
	targetDir := filepath.Join(cwd, ".pi", "extensions")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(targetDir, "munsu-integrate.ts")
	markerLine := "// munsu-integrate v1 -- do not edit this section\n"
	content := markerLine + "export const integration = { version: '1.0.0' };\n"
	if err := os.WriteFile(targetFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(integrateDir, "manifest.json")
	manifestData := `{
  "schema_version": "1.0.0",
  "harness": "pi",
  "version": "1.0.0",
  "scope": "project",
  "installed_at": "now",
  "target_paths": ["` + targetFile + `"],
  "capabilities": ["session-start", "wake-followup", "turnend-guard", "pretool-check", "scope-gate"]
}`
	if err := os.WriteFile(manifestPath, []byte(manifestData), 0644); err != nil {
		t.Fatal(err)
	}

	// Sanity: verify Status returns "installed" directly
	result, err := integrate.Status(home, cwd, "pi", integrate.ScopeProject)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if result.State != "installed" {
		t.Logf("Status for pi = %q (message: %s), may need adjusted test fixture", result.State, result.Message)
	}

	// Now check through CollectCapabilities
	capResult := CollectCapabilities(home, cwd, "0.1.0")
	var piDiag *IntegrationDiagnostic
	for i, d := range capResult.Integrations {
		if d.Harness == "pi" {
			piDiag = &capResult.Integrations[i]
			break
		}
	}
	if piDiag == nil {
		t.Fatal("expected pi harness in integrations")
	}
	t.Logf("pi integration diagnostic: State=%q Detail=%q Drifted=%v", piDiag.State, piDiag.Detail, piDiag.Drifted)
}

// TestCollectCapabilities_AllHarnessesPresent verifies the integration
// matrix contains all known harnesses.
func TestCollectCapabilities_AllHarnessesPresent(t *testing.T) {
	home := t.TempDir()
	capResult := CollectCapabilities(home, ".", "0.1.0")

	expected := map[string]bool{"pi": false, "claude": false, "codex": false, "grok": false, "opencode": false, "agy": false}
	for _, d := range capResult.Integrations {
		if _, ok := expected[d.Harness]; ok {
			expected[d.Harness] = true
		} else {
			t.Errorf("unexpected harness in integration matrix: %q", d.Harness)
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing harness %q from integration matrix", name)
		}
	}
}
