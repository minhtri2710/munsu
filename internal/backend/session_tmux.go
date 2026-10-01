package backend

import (
	"fmt"
	"strings"
)

// TmuxBackend implements Backend using the tmux CLI.
type TmuxBackend struct{}

// windowName returns the complete caller-provided label.
func (t *TmuxBackend) windowName(name string) string {
	return name
}

// ensureSession checks whether session exists and creates it if missing.
func (t *TmuxBackend) ensureSession(session string) error {
	bin, err := lookBackendBin("tmux")
	if err != nil {
		return err
	}
	// Check if session exists
	_, _, hasErr := runBackendCommand(bin, []string{"has-session", "-t", session}, "", nil)
	if hasErr == nil {
		return nil // session exists
	}
	// Create a detached session
	stdout, stderr, err := runBackendCommand(bin, []string{"new-session", "-d", "-s", session}, "", nil)
	if err != nil {
		return wrapBackendCommandError(fmt.Sprintf("tmux new-session %q", session), stdout, stderr, err)
	}
	return nil
}

// FindOrCreateWindow returns the existing window with the given name in the
// session, or creates it. It is the reservation-aware find-or-create contract
// for reentrant endpoint recovery: the launch intent scopes the window label
// per generation, so repeating the same launch finds the SAME window (created
// by an earlier attempt) and a different launch never reuses it. Multiple
// windows with the same name fail closed as ambiguous.
func (t *TmuxBackend) FindOrCreateWindow(session, name string) (string, error) {
	bin, err := lookBackendBin("tmux")
	if err != nil {
		return "", err
	}
	if err := t.ensureSession(session); err != nil {
		return "", err
	}
	out, stderr, err := runBackendCommand(bin, []string{"list-windows", "-t", session, "-F", "#{window_name}\t#{window_id}"}, "", nil)
	if err != nil {
		return "", wrapBackendCommandError("tmux list-windows", out, stderr, err)
	}
	var matches []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) == 2 && fields[0] == name {
			matches = append(matches, fields[1])
		}
	}
	switch len(matches) {
	case 1:
		return session + ":" + matches[0], nil
	case 0:
		return t.NewWindow(session, name)
	default:
		return "", fmt.Errorf("tmux find-or-create: %d windows named %q in session %q; ambiguous", len(matches), name, session)
	}
}

// NewWindow creates a new tmux window in the given session.
// If the session does not exist, it is created automatically.
// It uses `tmux new-window -P -F "#{window_id}" -n <name>`.
// The session parameter can be a tmux session selector (e.g. "mysession" or "munsu").
// Returns "<session>:<window_id>" for window handle.
func (t *TmuxBackend) NewWindow(session, name string) (string, error) {
	bin, err := lookBackendBin("tmux")
	if err != nil {
		return "", err
	}
	// Ensure the session exists (auto-create for cold starts)
	if err := t.ensureSession(session); err != nil {
		return "", err
	}
	wName := t.windowName(name)
	out, stderr, err := runBackendCommand(bin, []string{"new-window", "-P", "-F", "#{window_id}", "-n", wName, "-t", session}, "", nil)
	if err != nil {
		return "", wrapBackendCommandError("tmux new-window", out, stderr, err)
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
// It takes two calls: `tmux send-keys -t <windowID> -l -- <text>` types text
// literally (without -l, tmux reads words like "Enter" or "C-c" as key names),
// and because -l makes every argument after it literal, the submit is a second
// `tmux send-keys -t <windowID> Enter`.
func (t *TmuxBackend) SendKeys(windowID, text string) error {
	bin, err := lookBackendBin("tmux")
	if err != nil {
		return err
	}
	target := normalizeTarget(windowID)
	out, stderr, err := runBackendCommand(bin, []string{"send-keys", "-t", target, "-l", "--", text}, "", nil)
	if err != nil {
		return wrapBackendCommandError("tmux send-keys", out, stderr, err)
	}
	out, stderr, err = runBackendCommand(bin, []string{"send-keys", "-t", target, "Enter"}, "", nil)
	if err != nil {
		return wrapBackendCommandError("tmux send-keys", out, stderr, err)
	}
	return nil
}

// Capture captures the last N lines of output from the identified window/pane.
// Uses `tmux capture-pane -t <windowID> -p -S -<lines>`.
func (t *TmuxBackend) Capture(windowID string, lines int) (string, error) {
	bin, err := lookBackendBin("tmux")
	if err != nil {
		return "", err
	}
	target := normalizeTarget(windowID)
	start := fmt.Sprintf("-%d", lines)
	out, stderr, err := runBackendCommand(bin, []string{"capture-pane", "-t", target, "-p", "-S", start}, "", nil)
	if err != nil {
		return "", wrapBackendCommandError("tmux capture-pane", out, stderr, err)
	}
	return string(out), nil
}

// CheckAlive checks whether the identified window/pane still exists by running
// CheckAlive checks whether the identified window/pane still exists by running
// `tmux list-panes -t <windowID>`.
// Returns (true, nil) if confirmed alive.
// Returns (false, ErrPaneNotFound) only for an exact, per-target absence
// (e.g. "can't find window/pane", "no such window"); server/socket, timeout,
// permission, missing-binary and generic/broad "not found" failures are
// operational errors and never authoritative absence.
func (t *TmuxBackend) CheckAlive(windowID string) (bool, error) {
	bin, err := lookBackendBin("tmux")
	if err != nil {
		return false, err
	}
	target := normalizeTarget(windowID)
	out, stderr, err := runBackendCommand(bin, []string{"list-panes", "-t", target}, "", nil)
	if err == nil {
		// A successful list-panes must return non-empty target output; empty
		// successful output is not authoritative presence.
		if strings.TrimSpace(string(out)) != "" {
			return true, nil
		}
		return false, fmt.Errorf("tmux list-panes: empty output for target %q", target)
	}
	msg := commandOutput(out, stderr)
	if !isBackendCommandTimeout(err) && isTmuxTargetAbsent(msg) {
		return false, ErrPaneNotFound
	}
	return false, wrapBackendCommandError("tmux list-panes", out, stderr, err)
}

// Teardown kills the identified window via `tmux kill-window -t <windowID>`.
// Errors are silently ignored if the window is already gone.
func (t *TmuxBackend) Teardown(windowID string) error {
	bin, err := lookBackendBin("tmux")
	if err != nil {
		return err
	}
	target := normalizeTarget(windowID)
	_, _, _ = runBackendCommand(bin, []string{"kill-window", "-t", target}, "", nil) // ignore errors — window may already be gone
	return nil
}

// isTmuxTargetAbsent reports whether a tmux list-panes error is an exact,
// per-target absence of the requested window/pane (e.g. "can't find window:
// <target>" or "no such window"). Server/socket availability errors ("no server
// running", connection refused), timeouts, permission errors and generic/broad
// "not found" are NOT per-target absence and return false so CheckAlive keeps
// them as operational errors (never ErrPaneNotFound).
func isTmuxTargetAbsent(msg string) bool {
	m := strings.ToLower(strings.TrimSpace(msg))
	for _, tok := range []string{
		"can't find window", "can't find pane", "can't find session",
		"no such window", "no such pane", "no such session",
	} {
		if strings.Contains(m, tok) {
			return true
		}
	}
	return false
}
