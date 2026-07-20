package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/marker"
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

func TestSendCmd_MarksCaptainLine(t *testing.T) {
	// Contract: General→Captain sends must carry the from-general marker so the
	// Second answers via parent status, not chat-only.
	line := "report progress on munsu-rank-rename"
	marked := marker.MarkFromGeneral(line)
	if !marker.IsFromGeneral(marked) {
		t.Fatalf("expected marker on captain send line")
	}
	if marked == line {
		t.Fatalf("expected prefix")
	}
}

func TestSendCmd_CaptainDeadPaneQueuesOutbox(t *testing.T) {
	// General sending to a captain whose pane is dead must queue to the outbox,
	// not be blocked. Only 'send general' (uplink to fleet top) is blocked.
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(stateDir, 0755)
	metaContent := "window=@dead\nbackend=herdr\nkind=captain\nsm_id=munsu\nhome=/tmp/captain-home\n"
	os.WriteFile(filepath.Join(stateDir, "captain:munsu.meta"), []byte(metaContent), 0644)

	root := NewRootCommand()
	root.SetArgs([]string{"send", "captain:munsu", "report status", "--home", tmpDir})
	err := root.Execute()
	if err == nil {
		return // outbox enqueue success is acceptable
	}
	if strings.Contains(err.Error(), "uplink use munsu report") {
		t.Fatalf("send to captain:<id> must not be blocked by uplink guard: %v", err)
	}
}

// --- Gate refusal tests for send and teardown ---

// runCmd is a test helper that runs a command in a directory.
func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...); cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %s %v failed (dir=%s): %v\n%s", name, args, dir, err, string(out))
	}
}

// initGateCheckout creates a temp no-mistakes gate checkout at a temp dir and
// returns its path. The checkout's git-common-dir is under
// <nmHome>/repos/<id>.git, matching the no-mistakes gate topology.
// Caller should os.Chdir into the returned checkout path before running a command.
func initGateCheckout(t *testing.T) string {
	t.Helper()
	nmHome := filepath.Join(t.TempDir(), ".no-mistakes")
	commonDir := filepath.Join(nmHome, "repos", "gate.git")
	if err := os.MkdirAll(filepath.Dir(commonDir), 0755); err != nil {
		t.Fatal(err)
	}
	runCmd(t, filepath.Dir(commonDir), "git", "init", "--bare", commonDir)
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: "+commonDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NM_HOME", nmHome)
	return checkout
}

func TestSendCmd_GateRefusesEnvMarker(t *testing.T) {
	t.Setenv("NO_MISTAKES_GATE", "1")
	root := NewRootCommand()
	root.SetArgs([]string{"send", "any-task", "hello"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected gate refusal, got nil")
	}
	if !strings.Contains(err.Error(), "send refused") {
		t.Errorf("expected 'send refused', got: %v", err)
	}
}

func TestSendCmd_GateRefusesEnvEmptyMarker(t *testing.T) {
	t.Setenv("NO_MISTAKES_GATE", "")
	root := NewRootCommand()
	root.SetArgs([]string{"send", "any-task", "hello"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected gate refusal, got nil")
	}
	if !strings.Contains(err.Error(), "send refused") {
		t.Errorf("expected 'send refused', got: %v", err)
	}
}

func TestSendCmd_GateRefusesGateCheckoutPath(t *testing.T) {
	checkout := initGateCheckout(t)
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(checkout); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	root := NewRootCommand()
	root.SetArgs([]string{"send", "any-task", "hello"})
	err = root.Execute()
	if err == nil {
		t.Fatal("expected gate refusal from gate checkout, got nil")
	}
	if !strings.Contains(err.Error(), "send refused") {
		t.Errorf("expected 'send refused', got: %v", err)
	}
}

func TestSendCmd_GateNormalNoMarker(t *testing.T) {
	// Without NO_MISTAKES_GATE and in a non-gate cwd, the send command should
	// proceed past the gate check and fail later on "no state" (no task meta).
	root := NewRootCommand()
	root.SetArgs([]string{"send", "nonexistent-task", "hello"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error about missing task, got nil")
	}
	// The error should NOT mention gate refusal — normal path.
	if strings.Contains(err.Error(), "send refused") {
		t.Errorf("normal send must not produce gate refusal, got: %v", err)
	}
}

func TestTeardownCmd_GateRefusesEnvMarker(t *testing.T) {
	t.Setenv("NO_MISTAKES_GATE", "1")
	root := NewRootCommand()
	root.SetArgs([]string{"teardown", "any-task"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected gate refusal, got nil")
	}
	if !strings.Contains(err.Error(), "teardown refused") {
		t.Errorf("expected 'teardown refused', got: %v", err)
	}
}

func TestTeardownCmd_GateRefusesEnvEmptyMarker(t *testing.T) {
	t.Setenv("NO_MISTAKES_GATE", "")
	root := NewRootCommand()
	root.SetArgs([]string{"teardown", "any-task"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected gate refusal, got nil")
	}
	if !strings.Contains(err.Error(), "teardown refused") {
		t.Errorf("expected 'teardown refused', got: %v", err)
	}
}

func TestTeardownCmd_GateRefusesGateCheckoutPath(t *testing.T) {
	checkout := initGateCheckout(t)
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(checkout); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	root := NewRootCommand()
	root.SetArgs([]string{"teardown", "any-task"})
	err = root.Execute()
	if err == nil {
		t.Fatal("expected gate refusal from gate checkout, got nil")
	}
	if !strings.Contains(err.Error(), "teardown refused") {
		t.Errorf("expected 'teardown refused', got: %v", err)
	}
}

func TestTeardownCmd_GateNormalNoMarker(t *testing.T) {
	// Without NO_MISTAKES_GATE and in a non-gate cwd, the teardown command should
	// proceed past the gate check and fail later on "no state" (no task meta).
	root := NewRootCommand()
	root.SetArgs([]string{"teardown", "nonexistent-task"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error about missing task, got nil")
	}
	// The error should NOT mention gate refusal — normal path.
	if strings.Contains(err.Error(), "teardown refused") {
		t.Errorf("normal teardown must not produce gate refusal, got: %v", err)
	}
}
