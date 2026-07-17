package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSendCmd_UsesMetaBackend verifies that send reads the backend from task meta
// instead of using the global config when meta has backend set.
func TestSendCmd_UsesMetaBackend(t *testing.T) {
	tmpDir := t.TempDir()

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write task meta with backend=tmux (overrides global config)
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	metaContent := "window=@0\nbackend=tmux\nkind=ship\n"
	if err := os.WriteFile(filepath.Join(stateDir, "test-task.meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}
	statusContent := "working: spawned\n"
	if err := os.WriteFile(filepath.Join(stateDir, "test-task.status"), []byte(statusContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Remove both tmux and herdr from PATH so whichever backend is attempted will fail
	// and tell us which one it tried.
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", "/dev/null")
	defer os.Setenv("PATH", oldPath)

	root := NewRootCommand()
	root.SetArgs([]string{"send", "test-task", "echo hello", "--home", tmpDir})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error (tmux not on PATH), got nil")
	}
	if !strings.Contains(err.Error(), "tmux") {
		t.Errorf("expected error mentioning 'tmux' (from meta backend), got: %v", err)
	}
}

// TestSendCmd_UsesConfigBackendWhenMetaHasNone verifies that send falls back to
// global config when task meta does not specify a backend.
func TestSendCmd_UsesConfigBackendWhenMetaHasNone(t *testing.T) {
	tmpDir := t.TempDir()

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write task meta with NO backend field (uses global config)
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	metaContent := "window=@0\nkind=ship\n" // no backend field
	if err := os.WriteFile(filepath.Join(stateDir, "test-task.meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}
	statusContent := "working: spawned\n"
	if err := os.WriteFile(filepath.Join(stateDir, "test-task.status"), []byte(statusContent), 0644); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", "/dev/null")
	defer os.Setenv("PATH", oldPath)

	root := NewRootCommand()
	root.SetArgs([]string{"send", "test-task", "echo hello", "--home", tmpDir})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error (herdr not on PATH), got nil")
	}
	if !strings.Contains(err.Error(), "herdr") {
		t.Errorf("expected error mentioning 'herdr' (from config fallback), got: %v", err)
	}
}

// TestSendCmd_UnknownMetaBackendFallsThroughToResolve verifies that an unknown backend name
// in task meta falls through to config/auto-detection (it does not hard-error on unknown names).
func TestSendCmd_UnknownMetaBackendFallsThroughToResolve(t *testing.T) {
	tmpDir := t.TempDir()

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write task meta with backend=nonexistent (falls through to config/resolve)
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	metaContent := "window=@0\nbackend=nonexistent\nkind=ship\n"
	if err := os.WriteFile(filepath.Join(stateDir, "test-task.meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Set PATH to nothing so backend resolution fails
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", "/dev/null")
	defer os.Setenv("PATH", oldPath)

	root := NewRootCommand()
	root.SetArgs([]string{"send", "test-task", "echo hello", "--home", tmpDir})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error for unknown backend (fallthrough), got nil")
	}
	// Error should mention the resolved backend (herdr from config) or the send operation,
	// not the original unknown backend name.
	if strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("the unknown backend name should be transparent to the caller after fallthrough, got: %v", err)
	}
}

// TestPeekCmd_UsesMetaBackend verifies that peek reads the backend from task meta
// instead of using the global config when meta has backend set.
func TestPeekCmd_UsesMetaBackend(t *testing.T) {
	tmpDir := t.TempDir()

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write task meta with backend=tmux (overrides global config)
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	metaContent := "window=@0\nbackend=tmux\nkind=ship\n"
	if err := os.WriteFile(filepath.Join(stateDir, "test-task.meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", "/dev/null")
	defer os.Setenv("PATH", oldPath)

	root := NewRootCommand()
	root.SetArgs([]string{"peek", "test-task", "--home", tmpDir})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error (tmux not on PATH), got nil")
	}
	if !strings.Contains(err.Error(), "tmux") {
		t.Errorf("expected error mentioning 'tmux' (from meta backend), got: %v", err)
	}
}

// TestPeekCmd_UsesConfigBackendWhenMetaHasNone verifies that peek falls back to
// global config when task meta does not specify a backend.
func TestPeekCmd_UsesConfigBackendWhenMetaHasNone(t *testing.T) {
	tmpDir := t.TempDir()

	// Write global config saying "herdr"
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write task meta with NO backend field (uses global config)
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	metaContent := "window=@0\nkind=ship\n" // no backend field
	if err := os.WriteFile(filepath.Join(stateDir, "test-task.meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", "/dev/null")
	defer os.Setenv("PATH", oldPath)

	root := NewRootCommand()
	root.SetArgs([]string{"peek", "test-task", "--home", tmpDir})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error (herdr not on PATH), got nil")
	}
	if !strings.Contains(err.Error(), "herdr") {
		t.Errorf("expected error mentioning 'herdr' (from config fallback), got: %v", err)
	}
}
