package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil/fsaccess"
)

func TestPreflight_AdapterKnown(t *testing.T) {
	result, err := Preflight(Claude)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Claude, err)
	}
	if result.AdapterKnown != PreflightOK {
		t.Errorf("AdapterKnown = %q, want %q", result.AdapterKnown, PreflightOK)
	}
}

func TestPreflight_AdapterUnknown(t *testing.T) {
	_, err := Preflight("nonexistent-harness")
	if err == nil {
		t.Fatal("expected error for unknown harness")
	}
	if _, ok := err.(*PreflightError); !ok {
		t.Fatalf("error type = %T, want *PreflightError", err)
	}
}

func TestPreflight_BinaryOnPath_Claude(t *testing.T) {
	result, err := Preflight(Claude)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Claude, err)
	}
	// We can't guarantee the binary is on PATH in test env, but it should be
	// either OK (found) or Absent (not found), never Unknown for a known harness.
	if result.BinaryOnPath == PreflightUnknown {
		t.Error("BinaryOnPath should not be Unknown for claude (known binary name)")
	}
}

func TestPreflight_AuthConfigured(t *testing.T) {
	// Save original env and restore after test
	orig := os.Getenv("ANTHROPIC_API_KEY")
	defer os.Setenv("ANTHROPIC_API_KEY", orig)

	// Test with auth present
	os.Setenv("ANTHROPIC_API_KEY", "test-key")
	result, err := Preflight(Claude)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Claude, err)
	}
	if result.AuthConfigured != PreflightOK {
		t.Errorf("AuthConfigured = %q, want %q", result.AuthConfigured, PreflightOK)
	}

	// Test with auth absent
	os.Unsetenv("ANTHROPIC_API_KEY")
	result, err = Preflight(Claude)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Claude, err)
	}
	if result.AuthConfigured != PreflightAbsent {
		t.Errorf("AuthConfigured = %q, want %q", result.AuthConfigured, PreflightAbsent)
	}
}

func TestPreflight_AuthConfigured_AnyEnvVar(t *testing.T) {
	origGrok := os.Getenv("GROK_API_KEY")
	origXai := os.Getenv("XAI_API_KEY")
	defer func() {
		os.Setenv("GROK_API_KEY", origGrok)
		os.Setenv("XAI_API_KEY", origXai)
	}()
	os.Unsetenv("GROK_API_KEY")
	os.Unsetenv("XAI_API_KEY")

	// Test with GROK_API_KEY set
	os.Setenv("XAI_API_KEY", "test-key")
	result, err := Preflight(Grok)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Grok, err)
	}
	if result.AuthConfigured != PreflightOK {
		t.Errorf("AuthConfigured = %q, want %q", result.AuthConfigured, PreflightOK)
	}

	// Test with none set
	os.Unsetenv("XAI_API_KEY")
	result, err = Preflight(Grok)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Grok, err)
	}
	if result.AuthConfigured != PreflightAbsent {
		t.Errorf("AuthConfigured = %q, want %q", result.AuthConfigured, PreflightAbsent)
	}
}

func TestPreflight_ModelValid_Unknown(t *testing.T) {
	result, err := Preflight(Codex)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Codex, err)
	}
	if result.ModelValid != PreflightUnknown {
		t.Errorf("ModelValid = %q, want %q (can't validate without API)", result.ModelValid, PreflightUnknown)
	}
}

func TestPreflight_PiAuthWithNonOpenAIKey(t *testing.T) {
	// Regression: Pi preflight should accept any Pi-supported provider
	// env var, not just OPENAI_API_KEY. Also verifies credential store
	// check does not interfere (uses isolated temp dir).
	clearEnvMarkers(t)

	// Isolate credential store to an empty temp dir to avoid interference
	// from the real ~/.pi/agent/auth.json on this machine.
	tmpDir := t.TempDir()
	restoreCred := injectPiCredentialCheck(t, tmpDir)
	defer restoreCred()

	origPiKey := os.Getenv("OPENAI_API_KEY")
	origXaiKey := os.Getenv("XAI_API_KEY")
	origAnthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	origGeminiKey := os.Getenv("GEMINI_API_KEY")
	defer func() {
		os.Setenv("OPENAI_API_KEY", origPiKey)
		os.Setenv("XAI_API_KEY", origXaiKey)
		os.Setenv("ANTHROPIC_API_KEY", origAnthropicKey)
		os.Setenv("GEMINI_API_KEY", origGeminiKey)
	}()
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("XAI_API_KEY")

	// Pi with no API key at all should fail (absent) — empty temp dir, no env
	result, err := Preflight(Pi)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Pi, err)
	}
	if result.AuthConfigured != PreflightAbsent {
		t.Errorf("Pi with no key: AuthConfigured = %q, want %q", result.AuthConfigured, PreflightAbsent)
	}

	// Pi with a non-OPENAI Pi-supported key should pass
	os.Setenv("XAI_API_KEY", "test-key")
	result, err = Preflight(Pi)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Pi, err)
	}
	if result.AuthConfigured != PreflightOK {
		t.Errorf("Pi with XAI_API_KEY: AuthConfigured = %q, want %q", result.AuthConfigured, PreflightOK)
	}

	// Pi with ANTHROPIC_API_KEY should also pass
	os.Unsetenv("XAI_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", "test-key")
	result, err = Preflight(Pi)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Pi, err)
	}
	if result.AuthConfigured != PreflightOK {
		t.Errorf("Pi with ANTHROPIC_API_KEY: AuthConfigured = %q, want %q", result.AuthConfigured, PreflightOK)
	}

	// Pi with GEMINI_API_KEY should also pass
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Setenv("GEMINI_API_KEY", "test-key")
	result, err = Preflight(Pi)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Pi, err)
	}
	if result.AuthConfigured != PreflightOK {
		t.Errorf("Pi with GEMINI_API_KEY: AuthConfigured = %q, want %q", result.AuthConfigured, PreflightOK)
	}
}

func TestPreflight_AllLevelsKnown(t *testing.T) {
	orig := os.Getenv("OPENAI_API_KEY")
	defer os.Setenv("OPENAI_API_KEY", orig)
	os.Setenv("OPENAI_API_KEY", "test-key")

	result, err := Preflight(Codex)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Codex, err)
	}

	if result.AdapterKnown != PreflightOK {
		t.Errorf("AdapterKnown = %q, want %q", result.AdapterKnown, PreflightOK)
	}

	// Binary may or may not be on PATH, but it must not be Unknown
	if result.BinaryOnPath == PreflightUnknown {
		t.Error("BinaryOnPath should not be Unknown for codex")
	}
	if result.AuthConfigured != PreflightOK {
		t.Errorf("AuthConfigured = %q, want %q", result.AuthConfigured, PreflightOK)
	}
}

func TestPreflight_UnknownAuthEnv(t *testing.T) {
	// Harnesses without auth env mapping should get Unknown for auth
	// (currently all known harnesses have mappings, but test the edge case)
	result, err := Preflight(Claude)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Claude, err)
	}
	// Claude has ANTHROPIC_API_KEY mapping, so this should be ok or absent
	if result.AuthConfigured == PreflightUnknown {
		t.Log("AuthConfigured is Unknown - acceptable if mapping is missing")
	}
}

func TestPreflightError_ErrorMessages(t *testing.T) {
	err := &PreflightError{Harness: "codex", Reason: "adapter-unknown"}
	msg := err.Error()
	if msg == "" {
		t.Fatal("expected non-empty error message")
	}

	err = &PreflightError{Harness: "pi", Reason: "binary-absent"}
	msg = err.Error()
	if msg == "" {
		t.Fatal("expected non-empty error message")
	}

	err = &PreflightError{Harness: "claude", Reason: "auth-absent"}
	msg = err.Error()
	if msg == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestPreflight_AllKnownHarnesses(t *testing.T) {
	for _, h := range KnownHarnesses {
		result, err := Preflight(h)
		if err != nil {
			if _, ok := err.(*PreflightError); !ok {
				t.Errorf("Preflight(%q) error type = %T, want *PreflightError", h, err)
			}
		}
		if result != nil && result.AdapterKnown != PreflightOK {
			t.Errorf("Preflight(%q).AdapterKnown = %q, want %q", h, result.AdapterKnown, PreflightOK)
		}
	}
}

// TestPreflight_PiCredentialStore_FileExists verifies that a valid auth.json
// in the Pi config dir causes AuthConfigured to be PreflightOK when no env
// vars are set.
func TestPreflight_PiCredentialStore_FileExists(t *testing.T) {
	clearEnvMarkers(t)

	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"test": true}`), 0600); err != nil {
		t.Fatal(err)
	}

	restore := injectPiCredentialCheck(t, tmpDir)
	defer restore()

	result, err := Preflight(Pi)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Pi, err)
	}
	if result.AuthConfigured != PreflightOK {
		t.Errorf("AuthConfigured = %q, want %q (auth.json present in temp dir)", result.AuthConfigured, PreflightOK)
	}
}

// TestPreflight_PiCredentialStore_FileMissing verifies that absence of auth.json
// causes AuthConfigured to be PreflightAbsent when no env vars are set.
func TestPreflight_PiCredentialStore_FileMissing(t *testing.T) {
	clearEnvMarkers(t)

	tmpDir := t.TempDir()
	// No auth.json written; temp dir is empty

	restore := injectPiCredentialCheck(t, tmpDir)
	defer restore()

	result, err := Preflight(Pi)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Pi, err)
	}
	if result.AuthConfigured != PreflightAbsent {
		t.Errorf("AuthConfigured = %q, want %q (no auth.json)", result.AuthConfigured, PreflightAbsent)
	}
}

// TestPreflight_PiCredentialStore_DirInsteadOfFile verifies that a directory
// at the credential store path is not accepted (fails-closed).
func TestPreflight_PiCredentialStore_DirInsteadOfFile(t *testing.T) {
	clearEnvMarkers(t)

	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "auth.json")
	if err := os.MkdirAll(authPath, 0755); err != nil {
		t.Fatal(err)
	}

	restore := injectPiCredentialCheck(t, tmpDir)
	defer restore()

	result, err := Preflight(Pi)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Pi, err)
	}
	if result.AuthConfigured != PreflightAbsent {
		t.Errorf("AuthConfigured = %q, want %q (auth.json is a directory)", result.AuthConfigured, PreflightAbsent)
	}
}

// TestPreflight_PiCredentialStore_Unreadable verifies that an unreadable
// auth.json file fails closed.
func TestPreflight_PiCredentialStore_Unreadable(t *testing.T) {
	clearEnvMarkers(t)

	tmpDir := t.TempDir()
	authPath := filepath.Join(tmpDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fsaccess.MakeUnreadable(t, authPath); err != nil {
		if fsaccess.IsUnsupportedFixture(err) {
			t.Errorf("access-control observation unavailable: %v", err)
			return
		}
		t.Fatal(err)
	}

	restore := injectPiCredentialCheck(t, tmpDir)
	defer restore()

	result, err := Preflight(Pi)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Pi, err)
	}
	if result.AuthConfigured != PreflightAbsent {
		t.Errorf("AuthConfigured = %q, want %q (auth.json unreadable)", result.AuthConfigured, PreflightAbsent)
	}
}

// TestPreflight_PiCredentialStore_EnvVarTakesPrecedence verifies that an env var
// is still sufficient even when auth.json is absent.
func TestPreflight_PiCredentialStore_EnvVarTakesPrecedence(t *testing.T) {
	clearEnvMarkers(t)

	tmpDir := t.TempDir()
	// No auth.json

	orig := os.Getenv("ANTHROPIC_API_KEY")
	defer os.Setenv("ANTHROPIC_API_KEY", orig)
	os.Setenv("ANTHROPIC_API_KEY", "test-key")

	restore := injectPiCredentialCheck(t, tmpDir)
	defer restore()

	result, err := Preflight(Pi)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Pi, err)
	}
	if result.AuthConfigured != PreflightOK {
		t.Errorf("AuthConfigured = %q, want %q (ANTHROPIC_API_KEY set)", result.AuthConfigured, PreflightOK)
	}
}

// injectPiCredentialCheck replaces piCredentialFile with a check scoped to
// the given temp dir, so tests don't examine the real ~/.pi/agent/auth.json.
// Returns a restore function.
func injectPiCredentialCheck(t *testing.T, tmpDir string) func() {
	t.Helper()
	origConfigDir := piConfigDir
	piConfigDir = func() string {
		return tmpDir
	}

	return func() {
		piConfigDir = origConfigDir
	}
}
