package secondmate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunch_SecondmateHarnessPin(t *testing.T) {
	tmp := t.TempDir()

	// Write config/secondmate-harness = "grok"
	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "secondmate-harness"), []byte("grok\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a secondmate home with AGENTS.md
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test brief\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := Launch(smHome, tmp)
	if err == nil {
		t.Fatal("expected error for grok harness (only pi is supported)")
	}
	if !strings.Contains(err.Error(), `"grok"`) {
		t.Errorf("error should mention resolved harness 'grok', got: %v", err)
	}
	if !strings.Contains(err.Error(), `"pi"`) {
		t.Errorf("error should mention supported harness 'pi', got: %v", err)
	}
}

func TestLaunch_SecondmateHarnessPin_Pi(t *testing.T) {
	tmp := t.TempDir()

	// Write config/secondmate-harness = "pi"
	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "secondmate-harness"), []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a secondmate home with AGENTS.md
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test brief\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// With "pi" pin, the harness check should pass.
	// The function then tries exec.LookPath("pi") and cmd.Start().
	// pi is on PATH in this environment, so it will attempt a real launch.
	// We just verify it gets past the harness check (no "unsupported" error).
	err := Launch(smHome, tmp)
	if err != nil && strings.Contains(err.Error(), "resolved harness") {
		t.Fatalf("pi harness should be accepted but got: %v", err)
	}
	// A real error from LookPath or Start is OK — the harness resolution itself passed.
	t.Logf("Launch returned (harness check passed, pi binary found): %v", err)
}

func TestLaunch_FallsBackToDetectClaude(t *testing.T) {
	tmp := t.TempDir()

	// Set env so Detect() returns "claude"
	t.Setenv("CLAUDE_CODE", "1")

	// No config files at all — harness.Secondmate() falls through to Detect()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test brief\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := Launch(smHome, tmp)
	if err == nil {
		t.Fatal("expected error for claude harness (only pi is supported)")
	}
	if !strings.Contains(err.Error(), `"claude"`) {
		t.Errorf("error should mention resolved harness 'claude', got: %v", err)
	}
	if !strings.Contains(err.Error(), `"pi"`) {
		t.Errorf("error should mention supported harness 'pi', got: %v", err)
	}
}

func TestLaunch_SecondmateHarnessPin_CrewHarnessFallback(t *testing.T) {
	tmp := t.TempDir()

	// No secondmate-harness, but set crew-harness = "grok"
	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "crew-harness"), []byte("grok\n"), 0644); err != nil {
		t.Fatal(err)
	}

	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test brief\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// harness.Secondmate should fall through: no secondmate-harness → crew-harness = "grok"
	err := Launch(smHome, tmp)
	if err == nil {
		t.Fatal("expected error for grok harness from crew-harness fallback")
	}
	if !strings.Contains(err.Error(), `"grok"`) {
		t.Errorf("error should mention resolved harness 'grok', got: %v", err)
	}
}
