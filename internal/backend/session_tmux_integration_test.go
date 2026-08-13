//go:build integration

package backend

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

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
	if alive, _ := tk.CheckAlive(wid); !alive {
		t.Fatal("NewWindow window not alive after creation")
	}

	// Clean up
	if err := tk.Teardown(wid); err != nil {
		t.Errorf("Teardown failed: %v", err)
	}

	// Verify it's gone
	if alive, _ := tk.CheckAlive(wid); alive {
		t.Error("window still alive after Teardown")
	}
}

func TestTmux_Alive_UnknownWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	// CheckAlive talks to the server, so an unknown window is only
	// distinguishable from a dead server when a server is running.
	if err := exec.Command("tmux", "list-sessions").Run(); err != nil {
		t.Skip("no tmux server running")
	}

	tk := &TmuxBackend{}
	// An unknown window should return false (ErrPaneNotFound)
	if alive, err := tk.CheckAlive("@99999"); alive || !errors.Is(err, ErrPaneNotFound) {
		t.Errorf("CheckAlive('@99999') = %v, %v; want false + ErrPaneNotFound", alive, err)
	}
}

func TestTmux_NewWindow_SessionAutoCreated(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	// With F1.1, a nonexistent session is auto-created
	wid, err := tk.NewWindow("munsu-test-session-12345", "test")
	if err != nil {
		t.Fatalf("NewWindow should auto-create session, got error: %v", err)
	}
	if wid == "" {
		t.Fatal("NewWindow returned empty window ID")
	}
	// Verify the window is alive
	if alive, _ := tk.CheckAlive(wid); !alive {
		t.Fatal("NewWindow window not alive after session auto-create")
	}
	// Clean up
	if err := tk.Teardown(wid); err != nil {
		t.Errorf("Teardown failed: %v", err)
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
