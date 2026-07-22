package session

import (
	"fmt"
	"os/exec"
	"strings"
)

// TmuxBackend implements Backend using the tmux CLI.
type TmuxBackend struct {
	// Tag is retained for compatibility with callers that scope sessions by home.
	Tag string
}

// tmuxBin returns the path to the tmux binary.
func tmuxBin() (string, error) {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return "", fmt.Errorf("tmux: not found on PATH")
	}
	return path, nil
}

// windowName returns the complete caller-provided label.
func (t *TmuxBackend) windowName(name string) string {
	return name
}

// ensureSession checks whether session exists and creates it if missing.
func (t *TmuxBackend) ensureSession(session string) error {
	bin, err := tmuxBin()
	if err != nil {
		return err
	}
	// Check if session exists
	has := exec.Command(bin, "has-session", "-t", session)
	if has.Run() == nil {
		return nil // session exists
	}
	// Create a detached session
	out, err := exec.Command(bin, "new-session", "-d", "-s", session).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-session %q: %s", session, strings.TrimSpace(string(out)))
	}
	return nil
}

// NewWindow creates a new tmux window in the given session.
// If the session does not exist, it is created automatically.
// It uses `tmux new-window -P -F "#{window_id}" -n <name>`.
// The session parameter can be a tmux session selector (e.g. "mysession" or "munsu").
// Returns "<session>:<window_id>" for window handle.
func (t *TmuxBackend) NewWindow(session, name string) (string, error) {
	bin, err := tmuxBin()
	if err != nil {
		return "", err
	}
	// Ensure the session exists (auto-create for cold starts)
	if err := t.ensureSession(session); err != nil {
		return "", err
	}
	wName := t.windowName(name)
	cmd := exec.Command(bin, "new-window", "-P", "-F", "#{window_id}", "-n", wName, "-t", session)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("tmux new-window: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("tmux new-window: %w", err)
	}
	// Return qualified "<session>:<window_id>" for window handle.
	return session + ":" + strings.TrimSpace(string(out)), nil
}

// normalizeTarget converts a window handle to the correct tmux target.
// Tmux window IDs (@N) are server-global; passing "session:@N" is invalid/fragile.
// - "session:@digits" → bare "@digits"
// - "session:name" (no @) → full "session:name"
// - bare "@N" → bare "@N"
func normalizeTarget(windowID string) string {
	if strings.Contains(windowID, ":@") {
		_, pid, _ := strings.Cut(windowID, ":")
		return pid
	}
	return windowID
}

// SendKeys sends literal text followed by Enter to the identified window/pane.
// Uses `tmux send-keys -t <windowID> <text> Enter`.
func (t *TmuxBackend) SendKeys(windowID, text string) error {
	bin, err := tmuxBin()
	if err != nil {
		return err
	}
	target := normalizeTarget(windowID)
	cmd := exec.Command(bin, "send-keys", "-t", target, text, "Enter")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Capture captures the last N lines of output from the identified window/pane.
// Uses `tmux capture-pane -t <windowID> -p -S -<lines>`.
func (t *TmuxBackend) Capture(windowID string, lines int) (string, error) {
	bin, err := tmuxBin()
	if err != nil {
		return "", err
	}
	target := normalizeTarget(windowID)
	start := fmt.Sprintf("-%d", lines)
	cmd := exec.Command(bin, "capture-pane", "-t", target, "-p", "-S", start)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("tmux capture-pane: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

// CheckAlive checks whether the identified window/pane still exists by running
// `tmux list-panes -t <windowID>`.
// Returns (true, nil) if confirmed alive.
// Returns (false, ErrPaneNotFound) if confirmed dead / absent.
// Returns (false, err) if tmux binary or execution failed.
func (t *TmuxBackend) CheckAlive(windowID string) (bool, error) {
	bin, err := tmuxBin()
	if err != nil {
		return false, err
	}
	target := normalizeTarget(windowID)
	cmd := exec.Command(bin, "list-panes", "-t", target)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	msg := strings.TrimSpace(string(out))
	if strings.Contains(msg, "can't find") || strings.Contains(msg, "not found") || strings.Contains(msg, "no server running") {
		return false, ErrPaneNotFound
	}
	return false, fmt.Errorf("tmux list-panes: %s", msg)
}

// Alive checks whether the identified window/pane still exists by running
// `tmux list-panes -t <windowID>`. Returns true ONLY when confirmed alive.
func (t *TmuxBackend) Alive(windowID string) bool {
	alive, err := t.CheckAlive(windowID)
	return err == nil && alive
}

// Teardown kills the identified window via `tmux kill-window -t <windowID>`.
// Errors are silently ignored if the window is already gone.
func (t *TmuxBackend) Teardown(windowID string) error {
	bin, err := tmuxBin()
	if err != nil {
		return err
	}
	target := normalizeTarget(windowID)
	cmd := exec.Command(bin, "kill-window", "-t", target)
	_ = cmd.Run() // ignore errors — window may already be gone
	return nil
}
