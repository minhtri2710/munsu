package harness

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
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
	testutil.WriteFakeExecutable(t, filepath.Join(dir, name), "#!/bin/sh\nexit 0\n")
}

func TestGuardBurnDownSaveDispatchRejectsNilConfig(t *testing.T) {
	if err := SaveDispatch(t.TempDir()+"/dispatch.json", nil); err == nil || !strings.Contains(err.Error(), "nil dispatch config") {
		t.Fatalf("SaveDispatch error = %v, want nil-config refusal", err)
	}
}
