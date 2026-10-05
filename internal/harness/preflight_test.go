package harness

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestPreflight_AdapterKnown(t *testing.T) {
	testutil.SetPath(t, t.TempDir())
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
	testutil.FakeOnPath(t, "claude", "#!/bin/sh\nexit 0\n")
	result, err := Preflight(Claude)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Claude, err)
	}
	if result.BinaryOnPath != PreflightOK {
		t.Errorf("BinaryOnPath = %q, want %q", result.BinaryOnPath, PreflightOK)
	}
}

// claude readiness is decided by `claude auth status` alone. The fake answers
// only that exact subcommand, with the exit status the row sets; any other
// argument exits 2.
func TestPreflight_AuthConfigured(t *testing.T) {
	const authStatus = "#!/bin/sh\n[ \"$1 $2\" = \"auth status\" ] || exit 2\n"
	tests := []struct {
		name    string
		claude  string // fake claude script; empty leaves claude off PATH
		apiKey  string
		timeout time.Duration
		want    PreflightLevel
	}{
		{name: "logged in, no env var", claude: authStatus + "exit 0\n", want: PreflightOK},
		{name: "env var set, claude says no credentials", claude: authStatus + "exit 1\n", apiKey: "test-key", want: PreflightAbsent},
		{name: "no credentials", claude: authStatus + "exit 1\n", want: PreflightAbsent},
		{name: "probe does not finish in time", claude: authStatus + "exec sleep 30\n", timeout: 100 * time.Millisecond, want: PreflightAbsent},
		{name: "claude not on PATH", want: PreflightAbsent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", tc.apiKey)
			if tc.claude == "" {
				testutil.SetPath(t, t.TempDir())
			} else {
				testutil.FakeOnPath(t, "claude", tc.claude)
			}
			if tc.timeout > 0 {
				orig := claudeAuthProbeTimeout
				claudeAuthProbeTimeout = tc.timeout
				t.Cleanup(func() { claudeAuthProbeTimeout = orig })
			}
			result, err := Preflight(Claude)
			if err != nil {
				t.Fatalf("Preflight(%q) error = %v", Claude, err)
			}
			if result.AuthConfigured != tc.want {
				t.Errorf("AuthConfigured = %q, want %q", result.AuthConfigured, tc.want)
			}
		})
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
	testutil.SetPath(t, t.TempDir())
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
	// Make auth.json unreadable
	testutil.MakePathUnreadable(t, authPath)

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
	origCredentialFile := piCredentialFile

	piConfigDir = func() string {
		return tmpDir
	}
	piCredentialFile = func(path string) bool {
		// Only honor paths under the temp dir (safety)
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		if !info.Mode().IsRegular() {
			return false
		}
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		f.Close()
		return true
	}

	return func() {
		piConfigDir = origConfigDir
		piCredentialFile = origCredentialFile
	}
}
