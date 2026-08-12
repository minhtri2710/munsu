// Package session provides the session backend interface and resolution.
//
// OrcaBackend is experimental: it uses the orca CLI terminal for session
// management. Orca is an experimental terminal app with optional worktree
// ownership (see OrcaWorktreeProvider).

package backend

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// OrcaBackend implements Backend using the orca CLI (experimental terminal app).
//
// Experimental:
//   - NewWindow creates a dedicated orca terminal (container+terminal pair).
//   - Alive checks terminal existence via orca terminal list.
//   - Capture reads terminal scrollback.
//   - SendKeys sends text + Enter to the terminal.
//   - Teardown closes the terminal.
//   - Never auto-detected by Default(); opt-in only via config/backend or --backend.
type OrcaBackend struct{}

// NewOrcaBackend creates an OrcaBackend.
func NewOrcaBackend() *OrcaBackend {
	return &OrcaBackend{}
}

// orcaBin returns the path to the orca binary.
func orcaBin() (string, error) {
	path, err := exec.LookPath("orca")
	if err != nil {
		return "", fmt.Errorf("orca: not found on PATH")
	}
	return path, nil
}

// orcaTerminalCreateResponse parses JSON from `orca terminal create --json`.
type orcaTerminalCreateResponse struct {
	ContainerID string `json:"container_id"`
	TerminalID  string `json:"terminal_id"`
}

// orcaTerminalListResponse parses JSON from `orca terminal list --json`.
type orcaTerminalListResponse struct {
	Terminals []orcaTerminalEntry `json:"terminals"`
}

type orcaTerminalEntry struct {
	ContainerID string `json:"container_id"`
	TerminalID  string `json:"terminal_id"`
}

// orcaOutput runs orca with the given args and returns stdout as a trimmed string.
func orcaOutput(args ...string) (string, error) {
	bin, err := orcaBin()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("orca %v: %s", args, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("orca %v: %w", args, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ParseOrcaWindow splits an orca window handle ("container_id|terminal_id").
func ParseOrcaWindow(handle string) (containerID, terminalID string) {
	idx := strings.LastIndex(handle, "|")
	if idx < 0 {
		return "", handle
	}
	return handle[:idx], handle[idx+1:]
}

// NewWindow creates a new orca terminal. The session parameter is reserved
// for future use (container grouping). The name parameter is the terminal label.
//
// Returns the window handle in "<container_id>|<terminal_id>" format.
func (o *OrcaBackend) NewWindow(session, name string) (string, error) {
	if _, err := orcaBin(); err != nil {
		return "", err
	}

	args := []string{"terminal", "create", "--json"}
	if name != "" {
		args = append(args, "--name", name)
	}

	out, err := orcaOutput(args...)
	if err != nil {
		return "", fmt.Errorf("orca terminal create: %w", err)
	}

	var resp orcaTerminalCreateResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("parsing orca terminal create: %w", err)
	}

	if resp.ContainerID == "" {
		return "", fmt.Errorf("orca terminal create returned empty container_id")
	}
	if resp.TerminalID == "" {
		return "", fmt.Errorf("orca terminal create returned empty terminal_id")
	}

	return resp.ContainerID + "|" + resp.TerminalID, nil
}

// SendKeys sends text followed by Enter to the orca terminal identified by windowID.
func (o *OrcaBackend) SendKeys(windowID, text string) error {
	_, terminalID := ParseOrcaWindow(windowID)
	if terminalID == "" {
		return fmt.Errorf("invalid orca window handle: %q", windowID)
	}

	if _, err := orcaBin(); err != nil {
		return err
	}

	if _, err := orcaOutput("terminal", "send", "--terminal", terminalID, text); err != nil {
		return err
	}

	// Send Enter key.
	if _, err := orcaOutput("terminal", "send-key", "--terminal", terminalID, "Enter"); err != nil {
		return err
	}
	return nil
}

// Capture reads the scrollback from the orca terminal identified by windowID.
func (o *OrcaBackend) Capture(windowID string, lines int) (string, error) {
	_, terminalID := ParseOrcaWindow(windowID)
	if terminalID == "" {
		return "", fmt.Errorf("invalid orca window handle: %q", windowID)
	}

	if _, err := orcaBin(); err != nil {
		return "", err
	}

	args := []string{"terminal", "capture", "--terminal", terminalID}
	if lines > 0 {
		args = append(args, "--lines", fmt.Sprintf("%d", lines))
	}

	out, err := orcaOutput(args...)
	if err != nil {
		return "", fmt.Errorf("orca terminal capture: %w", err)
	}
	return out, nil
}

// Alive checks whether the terminal identified by windowID still exists.
func (o *OrcaBackend) Alive(windowID string) bool {
	alive, err := o.CheckAlive(windowID)
	if err != nil {
		return false
	}
	return alive
}

// CheckAlive is the structured probe for the typed observation contract. It
// returns (true, nil) when the exact terminal exists, (false, ErrPaneNotFound)
// when the parsed authoritative terminal list confirms the terminal is absent,
// and an operational error for every other failure (command failure, malformed
// JSON). An operational failure is never authoritative absence.
func (o *OrcaBackend) CheckAlive(windowID string) (bool, error) {
	_, terminalID := ParseOrcaWindow(windowID)
	if terminalID == "" {
		return false, fmt.Errorf("orca: invalid window handle %q", windowID)
	}

	if _, err := orcaBin(); err != nil {
		return false, err
	}

	out, err := orcaOutput("terminal", "list", "--json")
	if err != nil {
		return false, fmt.Errorf("orca: listing terminals: %w", err)
	}

	var resp orcaTerminalListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return false, fmt.Errorf("orca: malformed terminal list: %w", err)
	}

	for _, t := range resp.Terminals {
		if t.TerminalID == terminalID {
			return true, nil
		}
	}
	// The parsed authoritative list lacks the exact terminal: confirmed absent.
	return false, ErrPaneNotFound
}

// Teardown closes the terminal identified by windowID.
// Errors are silently ignored if the terminal is already gone.
func (o *OrcaBackend) Teardown(windowID string) error {
	_, terminalID := ParseOrcaWindow(windowID)
	if terminalID == "" {
		return fmt.Errorf("invalid orca window handle: %q", windowID)
	}

	if _, err := orcaBin(); err != nil {
		return err
	}

	_, _ = orcaOutput("terminal", "close", "--terminal", terminalID)
	return nil
}
