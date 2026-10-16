package backend

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
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

// ForegroundProcess reports the pane's foreground command via
// `tmux display-message -p -t <target> '#{pane_pid} #{pane_current_command}'`,
// or, when that command is a munsu captain seat, the command of the captain
// the seat runs. Empty output is unreadable evidence, not a process name.
func (t *TmuxBackend) ForegroundProcess(windowID string) (string, error) {
	bin, err := lookBackendBin("tmux")
	if err != nil {
		return "", err
	}
	target := normalizeTarget(windowID)
	out, stderr, err := runBackendCommand(bin, []string{"display-message", "-p", "-t", target, "#{pane_pid} #{pane_current_command}"}, "", nil)
	if err != nil {
		return "", wrapBackendCommandError("tmux display-message", out, stderr, err)
	}
	panePID, name, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	if name == "" {
		return "", fmt.Errorf("tmux display-message: empty pane_current_command for target %q", target)
	}
	if captain, ok := captainSeatChild(panePID); ok {
		return captain, nil
	}
	return name, nil
}

// captainSeatChild returns the command of the captain a `munsu captain seat`
// in the foreground of the pane runs: the seat's child in the seat's own
// process group (the seat runs captain requests in groups of their own). It
// is false when the foreground is not a seat, the seat has no captain yet, or
// ps is unreadable.
func captainSeatChild(panePID string) (string, bool) {
	ps, err := lookBackendBin("ps")
	if err != nil {
		return "", false
	}
	out, _, err := runBackendCommand(ps, []string{"-A", "-o", "pid=,ppid=,pgid=,tpgid=,comm="}, "", nil)
	if err != nil {
		return "", false
	}
	type proc struct{ pid, ppid, pgid, tpgid, comm string }
	var procs []proc
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 {
			procs = append(procs, proc{f[0], f[1], f[2], f[3], strings.Join(f[4:], " ")})
		}
	}
	for _, seat := range procs {
		if (seat.pid != panePID && seat.ppid != panePID) || seat.pid != seat.pgid || seat.pgid != seat.tpgid {
			continue
		}
		args, _, err := runBackendCommand(ps, []string{"-o", "args=", "-p", seat.pid}, "", nil)
		if err != nil || !strings.Contains(string(args), " captain seat -- ") {
			return "", false
		}
		for _, c := range procs {
			if c.ppid == seat.pid && c.pgid == seat.pid {
				return filepath.Base(c.comm), true
			}
		}
		return "", false
	}
	return "", false
}

// Teardown kills the identified window via `tmux kill-window -t <windowID>`.
// A kill that fails because the target is already gone settles as done; any
// other failure (server, socket, timeout, permission) is returned.
func (t *TmuxBackend) Teardown(windowID string) error {
	bin, err := lookBackendBin("tmux")
	if err != nil {
		return err
	}
	target := normalizeTarget(windowID)
	out, stderr, err := runBackendCommand(bin, []string{"kill-window", "-t", target}, "", nil)
	if err != nil && (isBackendCommandTimeout(err) || !isTmuxTargetAbsent(commandOutput(out, stderr))) {
		return wrapBackendCommandError("tmux kill-window", out, stderr, err)
	}
	return nil
}

// tmuxShells are foreground commands of a pane with no agent in it. A prompt
// is never typed into one (G1085: no input into a session other than its own).
var tmuxShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
	"tcsh": true, "csh": true, "nu": true, "login": true, "tmux": true,
}

// tmuxPromptSettle is the pause between the paste and Enter, and between Enter
// and the capture that looks for the turn start.
var tmuxPromptSettle = 200 * time.Millisecond

// tmuxPromptStartPolls bounds how many settle intervals AgentPrompt waits for
// the pane to change after Enter.
const tmuxPromptStartPolls = 25

// AgentPrompt pastes text into the pane as one bracketed paste (the payload
// never reaches a shell argument) and presses Enter. tmux reports no agent
// status, so a turn start is taken as the pane redrawing after Enter; a pane
// that does not change within the window reports PromptStalled.
func (t *TmuxBackend) AgentPrompt(windowID, text string) PromptResult {
	bin, err := lookBackendBin("tmux")
	if err != nil {
		return PromptResult{Status: PromptBackendFailed, Detail: "tmux not available", Err: err}
	}
	alive, err := t.CheckAlive(windowID)
	if errors.Is(err, ErrPaneNotFound) || (err == nil && !alive) {
		return PromptResult{Status: PromptEndpointDead, Detail: "pane not found"}
	}
	if err != nil {
		return PromptResult{Status: PromptBackendFailed, Detail: "pane liveness check failed", Err: err}
	}
	process, err := t.ForegroundProcess(windowID)
	if err != nil {
		return PromptResult{Status: PromptBackendFailed, Detail: "reading the pane's foreground process failed", Err: err}
	}
	if tmuxShells[filepath.Base(process)] {
		return PromptResult{Status: PromptUnsupported, Detail: "pane runs " + process + ", not an agent"}
	}
	target := normalizeTarget(windowID)
	before, err := t.Capture(windowID, 30)
	if err != nil {
		return PromptResult{Status: PromptBackendFailed, Detail: "capturing the pane failed", Err: err}
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return PromptResult{Status: PromptBackendFailed, Detail: "naming the paste buffer failed", Err: err}
	}
	buffer := "munsu-prompt-" + hex.EncodeToString(nonce[:])
	if out, stderr, err := runBackendCommand(bin, []string{"set-buffer", "-b", buffer, "--", text}, "", nil); err != nil {
		return PromptResult{Status: PromptBackendFailed, Detail: "nothing was typed", Err: wrapBackendCommandError("tmux set-buffer", out, stderr, err)}
	}
	if out, stderr, err := runBackendCommand(bin, []string{"paste-buffer", "-d", "-r", "-p", "-b", buffer, "-t", target}, "", nil); err != nil {
		_, _, _ = runBackendCommand(bin, []string{"delete-buffer", "-b", buffer}, "", nil)
		return PromptResult{Status: PromptBackendFailed, Detail: "paste state unknown", Err: wrapBackendCommandError("tmux paste-buffer", out, stderr, err)}
	}
	time.Sleep(tmuxPromptSettle)
	staged, err := t.Capture(windowID, 30)
	if err != nil {
		return PromptResult{Status: PromptBackendFailed, Detail: "text pasted, Enter not sent", Err: err}
	}
	if staged == before {
		return PromptResult{Status: PromptStalled, Detail: "the paste did not render; Enter not sent"}
	}
	if out, stderr, err := runBackendCommand(bin, []string{"send-keys", "-t", target, "Enter"}, "", nil); err != nil {
		return PromptResult{Status: PromptStalled, Detail: "text pasted, Enter failed", Err: wrapBackendCommandError("tmux send-keys", out, stderr, err)}
	}
	for range tmuxPromptStartPolls {
		time.Sleep(tmuxPromptSettle)
		after, err := t.Capture(windowID, 30)
		if err != nil {
			return PromptResult{Status: PromptBackendFailed, Detail: "Enter sent, turn start unreadable", Err: err}
		}
		if after != staged {
			return PromptResult{Status: PromptSubmitted}
		}
	}
	return PromptResult{Status: PromptStalled, Detail: "no turn start observed after Enter"}
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
