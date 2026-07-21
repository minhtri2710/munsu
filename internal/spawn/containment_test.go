package spawn

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/delivery"
)

// TestNoMistakesYAML_DisableProjectSettingsIsTrue asserts that the repository's
// .no-mistakes.yaml has disable_project_settings: true.
// This is a contract test — it guards against weakening project-instruction isolation.
func TestNoMistakesYAML_DisableProjectSettingsIsTrue(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// Walk up to repo root (spawn/spawn_test.go → spawn/ → internal/ → repo root)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	yamlPath := filepath.Join(repoRoot, ".no-mistakes.yaml")

	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("reading .no-mistakes.yaml: %v", err)
	}

	if !strings.Contains(string(data), "disable_project_settings: true") {
		t.Errorf(".no-mistakes.yaml must preserve disable_project_settings: true; current content:\n%s", string(data))
	}
}

// TestEnsureDeliveryModeRunnable_AbsentBinary tests explicit mode fails on absent binary.
func TestEnsureDeliveryModeRunnable_AbsentBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := EnsureDeliveryModeRunnable("no-mistakes")
	if err == nil {
		t.Fatal("expected error for absent binary")
	}
	if !strings.Contains(err.Error(), "requires the no-mistakes binary") {
		t.Errorf("expected binary guidance, got: %v", err)
	}
}

// TestEnsureDeliveryModeRunnable_UnsupportedVersion tests explicit mode fails on old version.
func TestEnsureDeliveryModeRunnable_UnsupportedVersion(t *testing.T) {
	tmpDir := createFakeNoMistakesVersion(t, "0.5.0")
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	err := EnsureDeliveryModeRunnable("no-mistakes")
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
	// Accept either unsupported (version too old) or failed (axi surface missing)
	if !strings.Contains(err.Error(), "compatibility check failed") && !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "compat") {
		t.Errorf("expected compatibility error, got: %v", err)
	}
}

// TestEnsureDeliveryModeRunnable_Ready verifies that the real binary is accepted.
func TestEnsureDeliveryModeRunnable_Ready(t *testing.T) {
	if _, err := exec.LookPath("no-mistakes"); err != nil {
		t.Skip("no-mistakes not on PATH")
	}
	if err := EnsureDeliveryModeRunnable("no-mistakes"); err != nil {
		t.Errorf("expected nil for ready binary, got: %v", err)
	}
}

// TestNoMistakesProbe_AutoFallbackNeverErrors verifies that auto mode
// (no explicit/project/config selection) never errors — it falls back gracefully.
func TestNoMistakesProbe_AutoFallbackNeverErrors(t *testing.T) {
	// Absent binary: should return direct-PR without error
	t.Setenv("PATH", t.TempDir())
	mode, err := ResolveDeliveryMode(t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("auto should not error on absent binary, got: %v", err)
	}
	if mode != "direct-PR" {
		t.Errorf("auto absent binary should give direct-PR, got %q", mode)
	}
}

// TestNoMistakesProbe_AutoFallbackOnIncompatible is already in the other test file.
// This just adds a specific Preflight-level check.
func TestPreflight_NoMistakes_FailedProbe(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	// Preflight should see the binary on PATH but the probe will fail
	result, err := delivery.Preflight("no-mistakes", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Feasible {
		t.Error("expected preflight to be not feasible for broken binary")
	}
}

// TestNoMistakesRun_FailsClosedOnRuntimeError verifies that NoMistakesRun
// propagates errors without degrading mode.
func TestNoMistakesRun_FailsClosedOnRuntimeError(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	// Binary that fails on axi run
	script := `#!/bin/sh
case "$1" in
  --version)
    echo "no-mistakes version v1.40.0 (test)"
    exit 0
    ;;
  axi)
    case "$2" in
      status)
        echo "{}"
        exit 0
        ;;
      run)
        echo "gate agent error" >&2
        exit 1
        ;;
    esac
    exit 1
    ;;
esac
exit 1
`
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	err := delivery.NoMistakesRun("test-intent", nil)
	if err == nil {
		t.Fatal("expected error for failing axi run")
	}
	if !strings.Contains(err.Error(), "no-mistakes axi run") {
		t.Errorf("expected no-mistakes axi run error, got: %v", err)
	}
}
