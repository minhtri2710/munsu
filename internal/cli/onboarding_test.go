package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

// TestAutoDetectConfig_OnlyWhenAbsent verifies that autoDetectConfig writes config
// files only when they don't exist, and does not clobber without --reconfigure.
func TestAutoDetectConfig_OnlyWhenAbsent(t *testing.T) {
	tmpDir := t.TempDir()

	// Pre-create a config/backend file with a specific value
	if err := config.Set(tmpDir, "backend", "my-custom-backend"); err != nil {
		t.Fatal(err)
	}

	// Set reconfigure false (the init default)
	reconfigure = false

	if err := autoDetectConfig(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Verify backend was NOT overwritten
	val, err := config.Get(tmpDir, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if val != "my-custom-backend" {
		t.Errorf("backend config was clobbered: got %q, want %q", val, "my-custom-backend")
	}
}

// TestAutoDetectConfig_Reconfigure verifies that --reconfigure does NOT write
// backend config (backend is runtime context, not init-time preference).
func TestAutoDetectConfig_Reconfigure(t *testing.T) {
	tmpDir := t.TempDir()

	// Pre-create a config/backend file with a specific value
	if err := config.Set(tmpDir, "backend", "my-custom-backend"); err != nil {
		t.Fatal(err)
	}

	// Set reconfigure true (--reconfigure flag)
	reconfigure = true

	if err := autoDetectConfig(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Backend is runtime context — even --reconfigure should not overwrite it
	val, err := config.Get(tmpDir, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if val != "my-custom-backend" {
		t.Errorf("backend config should not be overwritten with --reconfigure (runtime context); got %q, want %q", val, "my-custom-backend")
	}
}

// TestConfigFileExists verifies configFileExists works correctly.
func TestConfigFileExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Should not exist initially
	if configFileExists(tmpDir, "backend") {
		t.Error("configFileExists should return false for nonexistent config")
	}

	// Create it
	if err := config.Set(tmpDir, "backend", "tmux"); err != nil {
		t.Fatal(err)
	}

	// Should now exist
	if !configFileExists(tmpDir, "backend") {
		t.Error("configFileExists should return true for existing config")
	}
}

// TestAutoDetectConfig_InitHome verifies full init flow in a tmp home.
// Backend is runtime context and must NOT be persisted at init time.
func TestAutoDetectConfig_InitHome(t *testing.T) {
	tmpDir := t.TempDir()

	// Ensure home dir tree
	if err := home.EnsureDirTree(tmpDir); err != nil {
		t.Fatal(err)
	}

	reconfigure = false

	if err := autoDetectConfig(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Backend is runtime context — init should NOT persist it
	if configFileExists(tmpDir, "backend") {
		t.Error("autoDetectConfig should NOT write backend (runtime context, not init-time preference)")
	}

	// Should write soldier-harness only if harness.Detect() succeeds
	// (may or may not detect in test env — that's OK)
	// backlog-backend should be written if tasks-axi is on PATH
}

// TestAutoDetectConfig_Idempotent verifies re-running without --reconfigure
// does not change already-written config.
func TestAutoDetectConfig_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()

	if err := home.EnsureDirTree(tmpDir); err != nil {
		t.Fatal(err)
	}

	// First run
	reconfigure = false
	if err := autoDetectConfig(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Read what was written
	originalBackend, _ := config.Get(tmpDir, "backend")

	// Captain run (without --reconfigure)
	if err := autoDetectConfig(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Should be unchanged
	val, _ := config.Get(tmpDir, "backend")
	if val != originalBackend {
		t.Errorf("captain run changed backend from %q to %q", originalBackend, val)
	}
}

// TestPrintNextSteps verifies the next-steps printer doesn't panic.
func TestPrintNextSteps(t *testing.T) {
	tmpDir := t.TempDir()
	printNextSteps(tmpDir)
}

// TestEnsureDirTreeConfigFiles verifies ensureDirTree + autoDetectConfig create proper files.
func TestEnsureDirTreeConfigFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Only ensure the tree, don't run autoDetectConfig
	if err := home.EnsureDirTree(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Config dir should exist
	configDir := filepath.Join(tmpDir, "config")
	if fi, err := os.Stat(configDir); err != nil || !fi.IsDir() {
		t.Fatalf("config dir should exist: %v", err)
	}

	// No config files should exist yet
	if _, err := config.Get(tmpDir, "backend"); err == nil {
		t.Error("backend should not exist before autoDetectConfig")
	}
}
