package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardBurnDownDispatchPreflightRejectsAbsentBinary(t *testing.T) {
	t.Setenv("PATH", "/dev/null")
	cfg := &DispatchConfig{DefaultHarness: Claude}
	_, err := ResolveDispatchSelectionWithPreflight(cfg, "task")
	if err == nil || !strings.Contains(err.Error(), "binary not found on PATH") {
		t.Fatalf("ResolveDispatchSelectionWithPreflight error = %v, want absent-binary refusal", err)
	}
}

func TestGuardBurnDownDispatchPreflightRejectsAbsentAuth(t *testing.T) {
	binDir := t.TempDir()
	writeTestHarnessExecutable(t, binDir, "claude")
	t.Setenv("PATH", binDir)
	t.Setenv("ANTHROPIC_API_KEY", "")
	cfg := &DispatchConfig{DefaultHarness: Claude}
	_, err := ResolveDispatchSelectionWithPreflight(cfg, "task")
	if err == nil || !strings.Contains(err.Error(), "auth not configured") {
		t.Fatalf("ResolveDispatchSelectionWithPreflight error = %v, want absent-auth refusal", err)
	}
}

func writeTestHarnessExecutable(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestGuardBurnDownSaveDispatchRejectsNilConfig(t *testing.T) {
	if err := SaveDispatch(t.TempDir()+"/dispatch.json", nil); err == nil || !strings.Contains(err.Error(), "nil dispatch config") {
		t.Fatalf("SaveDispatch error = %v, want nil-config refusal", err)
	}
}
