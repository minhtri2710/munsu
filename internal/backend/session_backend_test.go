package backend

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBackendForTask_BoundIdentityUsed verifies that BackendForTask resolves the
// identity durably bound in meta["backend"], independent of any config file.
// Core Backend never reads config directly — composition translates config once
// into the typed snapshot before operation start.
func TestBackendForTask_BoundIdentityUsed(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a config file saying "herdr" — BackendForTask must NOT consult it.
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !hasTmux() {
		t.Skip("tmux not on PATH (Select verifies the requested capability)")
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

// TestBackendForTask_MissingIdentityFailsClosed verifies that a task whose
// metadata has no "backend" identity FAILS CLOSED — config pins and env/PATH
// markers must NOT fall through.
func TestBackendForTask_MissingIdentityFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "/tmp/tmux-socket")
	t.Setenv("HERDR_ENV", "")

	meta := map[string]string{"window": "@test"}
	if _, _, err := BackendForTask(tmpDir, meta); err == nil {
		t.Fatal("missing bound backend identity must fail CLOSED (no config/env/PATH fallthrough)")
	}
}

// TestBackendForTask_EmptyMetaBackendFailsClosed verifies that an explicitly
// empty bound identity is treated the same as missing — fail closed.
func TestBackendForTask_EmptyMetaBackendFailsClosed(t *testing.T) {
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
	if _, _, err := BackendForTask(tmpDir, meta); err == nil {
		t.Fatal("empty bound backend identity must fail CLOSED")
	}
}

// TestBackendForTask_NilMetaFailsClosed verifies that a nil meta map fails
// closed — no auto-detection fallback.
func TestBackendForTask_NilMetaFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TMUX", "/tmp/tmux-socket")

	if _, _, err := BackendForTask(tmpDir, nil); err == nil {
		t.Fatal("nil meta must fail CLOSED (no backend identity, no auto-detect)")
	}
}

// TestBackendForTask_UnknownMetaBackendFailsClosed verifies that an unknown
// bound backend in meta never falls through to config/default resolution.
func TestBackendForTask_UnknownMetaBackendFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("tmux\n"), 0644); err != nil {
		t.Fatal(err)
	}

	meta := map[string]string{"backend": "nonexistent", "window": "@test"}
	if _, _, err := BackendForTask(tmpDir, meta); err == nil {
		t.Fatal("expected unknown bound backend to fail closed")
	}
}

// TestBackendForTask_BoundHerdrSessionBinding verifies that the herdr branch
// keeps session BINDING from task metadata (session binding is not selection).
func TestBackendForTask_BoundHerdrSessionBinding(t *testing.T) {
	tmpDir := t.TempDir()
	meta := map[string]string{
		"backend":       "herdr",
		"herdr_session": "my-lab-session",
		"window":        "w1",
	}
	bk, name, err := BackendForTask(tmpDir, meta)
	if err != nil {
		t.Fatal(err)
	}
	if name != "herdr" {
		t.Errorf("name = %q, want herdr", name)
	}
	hb, ok := bk.(*HerdrBackend)
	if !ok {
		t.Fatalf("expected *HerdrBackend, got %T", bk)
	}
	if hb.Session != "my-lab-session" {
		t.Errorf("Session = %q, want my-lab-session (bound from meta)", hb.Session)
	}
}
