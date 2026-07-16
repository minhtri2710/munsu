package session

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBackendForTask_MetaBackendPrecedence verifies that a backend specified
// in task metadata takes precedence over the config file.
func TestBackendForTask_MetaBackendPrecedence(t *testing.T) {
	tmpDir := t.TempDir()

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	meta := map[string]string{"backend": "tmux", "window": "@test"}
	bk, name, err := BackendForTask(tmpDir, meta)
	if err != nil {
		t.Fatal(err)
	}
	if name != "tmux" {
		t.Errorf("name = %q, want tmux", name)
	}
	if _, ok := bk.(*TmuxBackend); !ok {
		t.Errorf("expected TmuxBackend, got %T", bk)
	}
}

// TestBackendForTask_MissingMetaWithConfigPin verifies that when task metadata
// has no "backend" field, the config file (homeDir/config/backend) is respected.
func TestBackendForTask_MissingMetaWithConfigPin(t *testing.T) {
	tmpDir := t.TempDir()

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// No backend in meta
	meta := map[string]string{"window": "@test"}
	bk, name, err := BackendForTask(tmpDir, meta)
	if err != nil {
		t.Fatal(err)
	}
	if name != "herdr" {
		t.Errorf("name = %q, want herdr", name)
	}
	if _, ok := bk.(*HerdrBackend); !ok {
		t.Errorf("expected HerdrBackend, got %T", bk)
	}
}

// TestBackendForTask_MissingMetaWithRuntimeAuto verifies that when task metadata
// has no "backend" field and no config file exists, runtime auto-detection
// (based on environment variables and PATH) is used.
func TestBackendForTask_MissingMetaWithRuntimeAuto(t *testing.T) {
	tmpDir := t.TempDir()

	// No config file — rely on auto-detection.
	// Set TMUX to trigger tmux backend detection.
	t.Setenv("TMUX", "/tmp/tmux-socket")
	t.Setenv("HERDR_ENV", "")

	meta := map[string]string{"window": "@test"}
	bk, name, err := BackendForTask(tmpDir, meta)
	if err != nil {
		t.Fatal(err)
	}
	if name != "tmux" {
		t.Errorf("name = %q, want tmux", name)
	}
	if _, ok := bk.(*TmuxBackend); !ok {
		t.Errorf("expected TmuxBackend, got %T", bk)
	}
}

// TestBackendForTask_EmptyMetaBackend verifies that an empty backend field in
// meta (backend=) is treated the same as missing — falls through to Resolve.
func TestBackendForTask_EmptyMetaBackend(t *testing.T) {
	tmpDir := t.TempDir()

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	meta := map[string]string{"backend": "", "window": "@test"}
	bk, name, err := BackendForTask(tmpDir, meta)
	if err != nil {
		t.Fatal(err)
	}
	if name != "herdr" {
		t.Errorf("name = %q, want herdr", name)
	}
	if _, ok := bk.(*HerdrBackend); !ok {
		t.Errorf("expected HerdrBackend, got %T", bk)
	}
}

// TestBackendForTask_UnknownMetaBackendFallsThrough verifies that an unknown
// backend in meta causes a fallthrough to Resolve.
func TestBackendForTask_UnknownMetaBackendFallsThrough(t *testing.T) {
	tmpDir := t.TempDir()

	// Set up a config pin so the fallthrough has something to find.
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("tmux\n"), 0644); err != nil {
		t.Fatal(err)
	}

	meta := map[string]string{"backend": "nonexistent", "window": "@test"}
	bk, name, err := BackendForTask(tmpDir, meta)
	if err != nil {
		t.Fatal(err)
	}
	if name != "tmux" {
		t.Errorf("name = %q, want tmux (from config fallthrough), got %s", name, name)
	}
	if _, ok := bk.(*TmuxBackend); !ok {
		t.Errorf("expected TmuxBackend, got %T", bk)
	}
}

// TestBackendForTask_NilMeta verifies that a nil meta map is handled gracefully.
func TestBackendForTask_NilMeta(t *testing.T) {
	tmpDir := t.TempDir()

	// Set TMUX to trigger auto-detection.
	t.Setenv("TMUX", "/tmp/tmux-socket")

	bk, name, err := BackendForTask(tmpDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if name != "tmux" {
		t.Errorf("name = %q, want tmux", name)
	}
	if _, ok := bk.(*TmuxBackend); !ok {
		t.Errorf("expected TmuxBackend, got %T", bk)
	}
}
