package harness

import (
	"os"
	"testing"
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
