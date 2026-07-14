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


func TestLaunch_FallsBackToDetectClaude(t *testing.T) {
	tmp := t.TempDir()

	// Clear other env markers to avoid non-deterministic map iteration in detectFromEnv
	// PI_CODING_AGENT_DIR is commonly set in this environment and would make "pi" win
	for _, env := range []string{"CODECLIMB", "OPENCODE", "PI_CODING_AGENT_DIR", "GROK_VM_ID"} {
		t.Setenv(env, "")
	}
	// Also clear MUNSU_*_OVERRIDE vars that config.Get checks before file reads
	t.Setenv("MUNSU_SECONDMATE-HARNESS_OVERRIDE", "")
	t.Setenv("MUNSU_CREW-HARNESS_OVERRIDE", "")
	t.Setenv("CLAUDE_CODE", "1")

	// No config files at all — harness.Secondmate() falls through to Detect()
	// which should return "claude" from CLAUDE_CODE
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
