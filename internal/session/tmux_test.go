package session

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// hasTmux reports whether tmux is available on PATH.
func hasTmux() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

func TestTmuxBin_Found(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}
	path, err := tmuxBin()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("tmuxBin() returned empty path")
	}
	if !strings.Contains(path, "tmux") {
		t.Errorf("tmuxBin() = %q, expected path containing 'tmux'", path)
	}
}

func TestTmuxBin_NotFound(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	_, err := tmuxBin()
	if err == nil {
		t.Fatal("expected error when tmux is not on PATH")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefault_ReturnsTmux(t *testing.T) {
	for _, env := range []string{"HERDR_ENV", "ZELLIJ_SESSION_NAME", "CMUX_SOCKET", "ORCA_RUNTIME"} {
		t.Setenv(env, "")
		os.Unsetenv(env)
	}
	b := Default()
	if _, ok := b.(*TmuxBackend); !ok {
		t.Errorf("Default() returned %T, want *TmuxBackend", b)
	}
}

// TestTmux_NewWindow requires an active tmux server.
func TestTmux_NewWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	// Check there's a tmux server running
	check := exec.Command("tmux", "list-sessions")
	if err := check.Run(); err != nil {
		t.Skip("no tmux server running")
	}

	tk := &TmuxBackend{}

	// Try creating a window in the first available session
	sessionsOut, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		t.Fatal(err)
	}
	sessions := strings.Fields(string(sessionsOut))
	if len(sessions) == 0 {
		t.Skip("no tmux sessions available")
	}

	session := sessions[0]
	wid, err := tk.NewWindow(session, "munsu-test")
	if err != nil {
		t.Fatal(err)
	}
	if wid == "" {
		t.Fatal("NewWindow returned empty window ID")
	}

	// Verify the window is alive
	if !tk.Alive(wid) {
		t.Fatal("NewWindow window not alive after creation")
	}

	// Clean up
	if err := tk.Teardown(wid); err != nil {
		t.Errorf("Teardown failed: %v", err)
	}

	// Verify it's gone
	if tk.Alive(wid) {
		t.Error("window still alive after Teardown")
	}
}

func TestTmux_Alive_UnknownWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	// An unknown window should return false
	if tk.Alive("@99999") {
		t.Error("Alive('@99999') returned true for unknown window")
	}
}

func TestTmux_NewWindow_NoSession(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	_, err := tk.NewWindow("nonexistent-session-12345", "test")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestTmux_SendKeys_NoWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	err := tk.SendKeys("@99999", "echo hello")
	if err == nil {
		t.Fatal("expected error for unknown window")
	}
}

func TestTmux_Capture_NoWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	_, err := tk.Capture("@99999", 10)
	if err == nil {
		t.Fatal("expected error for unknown window")
	}
}

func TestTmux_Alive_TmuxNotOnPath(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	tk := &TmuxBackend{}
	if tk.Alive("@0") {
		t.Error("Alive() returned true when tmux is not on PATH")
	}
}

func TestTmux_Teardown_SuppressesErrors(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	// Teardown on a window that doesn't exist should not error
	if err := tk.Teardown("@99999"); err != nil {
		t.Errorf("Teardown on unknown window should not error: %v", err)
	}
}

// TestTmux_Backend_NotFound tests every method returns an error when tmux is missing.
func TestTmux_Backend_NotFound(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	tk := &TmuxBackend{}

	t.Run("NewWindow", func(t *testing.T) {
		_, err := tk.NewWindow("s", "n")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("SendKeys", func(t *testing.T) {
		err := tk.SendKeys("@0", "text")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Capture", func(t *testing.T) {
		_, err := tk.Capture("@0", 10)
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Alive", func(t *testing.T) {
		if tk.Alive("@0") {
			t.Error("Alive returned true when tmux is not on PATH")
		}
	})

	t.Run("Teardown", func(t *testing.T) {
		err := tk.Teardown("@0")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})
}
