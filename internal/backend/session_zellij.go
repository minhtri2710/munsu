package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ZellijBackend implements Backend using the zellij CLI.
// Experimental: behavior may change in future releases.
type ZellijBackend struct {
	// Session is the zellij session name (default: "default").
	Session string
	// Cwd is the working directory for new panes.
	Cwd string
}

// NewZellijBackend creates a ZellijBackend with the given session name.
// If session is empty, it defaults to os.Getenv("ZELLIJ_SESSION") then "default".
func NewZellijBackend(session string) *ZellijBackend {
	if session == "" {
		session = os.Getenv("ZELLIJ_SESSION")
	}
	if session == "" {
		session = "default"
	}
	return &ZellijBackend{Session: session}
}

// zellijBin returns the path to the zellij binary.
func zellijBin() (string, error) {
	path, err := exec.LookPath("zellij")
	if err != nil {
		return "", fmt.Errorf("zellij: not found on PATH")
	}
	return path, nil
}

func runZellijCommand(bin string, args ...string) ([]byte, []byte, error) {
	return runBackendCommand(bin, args, "", nil)
}

// zellijOutput runs a zellij action and returns stdout.
func (z *ZellijBackend) zellijOutput(args ...string) (string, error) {
	bin, err := zellijBin()
	if err != nil {
		return "", err
	}
	fullArgs := append([]string{"--session", z.Session, "action"}, args...)
	out, stderr, err := runZellijCommand(bin, fullArgs...)
	if err != nil {
		return "", wrapBackendCommandError(fmt.Sprintf("zellij %v", fullArgs), out, stderr, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// zellijNoOutput runs a zellij action and discards stdout, checking only for errors.
func (z *ZellijBackend) zellijRun(args ...string) error {
	bin, err := zellijBin()
	if err != nil {
		return err
	}
	fullArgs := append([]string{"--session", z.Session, "action"}, args...)
	out, stderr, err := runZellijCommand(bin, fullArgs...)
	if err != nil {
		return wrapBackendCommandError(fmt.Sprintf("zellij %v", fullArgs), out, stderr, err)
	}
	return nil
}

// ensureSession creates the zellij background session if it does not exist.
func (z *ZellijBackend) ensureSession() error {
	bin, err := zellijBin()
	if err != nil {
		return err
	}

	// Check if session already exists via list-sessions.
	listOut, _, listErr := runZellijCommand(bin, "list-sessions", "--short")
	if listErr == nil {
		for _, s := range strings.Split(string(listOut), "\n") {
			if strings.TrimSpace(s) == z.Session {
				return nil // session exists
			}
		}
	}

	// Create the background session.
	out, stderr, err := runZellijCommand(bin, "attach", "--create-background", z.Session)
	if err != nil {
		return wrapBackendCommandError(fmt.Sprintf("zellij attach --create-background %q", z.Session), out, stderr, err)
	}
	return nil
}

// zellijListPanesJSON represents the JSON output of list-panes --json.
type zellijPaneEntry struct {
	ID       int    `json:"id"`
	IsPlugin bool   `json:"is_plugin"`
	Title    string `json:"title"`
	TabID    int    `json:"tab_id"`
	Exited   bool   `json:"exited"`
}

// paneID extracts the pane ID part from a window handle (session:pane or bare pane).
func (z *ZellijBackend) paneID(windowID string) string {
	_, p := ParseWindow(windowID)
	return p
}

// NewWindow ensures the zellij session exists and creates a new pane.
// The session parameter is the zellij session name.
// The name parameter is the pane name/label.
// Returns the window handle in "<session>:<pane_id>" format.
func (z *ZellijBackend) NewWindow(session, name string) (string, error) {
	// Use the provided session name, falling back to the backend's session.
	sess := session
	if sess == "" {
		sess = z.Session
	}

	// Ensure the session exists (auto-create for cold starts).
	if err := z.ensureSession(); err != nil {
		return "", err
	}

	// Build new-pane args.
	args := []string{"new-pane", "--name", name}
	if z.Cwd != "" {
		args = append(args, "--cwd", z.Cwd)
	}

	out, err := z.zellijOutput(args...)
	if err != nil {
		return "", fmt.Errorf("creating zellij pane: %w", err)
	}

	// zellij new-pane returns the pane ID like "terminal_3" on stdout.
	paneID := strings.TrimSpace(out)
	if paneID == "" {
		// Some zellij versions may return the ID on stderr or not at all.
		// Fall back to a best-effort session-only handle.
		return sess + ":unknown", nil
	}

	return sess + ":" + paneID, nil
}

// SendKeys sends text followed by Enter to the pane identified by windowID.
// windowID may be "<session>:<pane_id>" or a bare pane ID.
func (z *ZellijBackend) SendKeys(windowID, text string) error {
	pid := z.paneID(windowID)

	// Write the text characters.
	if err := z.zellijRun("write-chars", "--pane-id", pid, text); err != nil {
		return err
	}

	// Send Enter key.
	return z.zellijRun("send-keys", "--pane-id", pid, "Enter")
}

// Capture reads the full scrollback from the pane identified by windowID.
// windowID may be "<session>:<pane_id>" or a bare pane ID.
// Returns the full output, trimmed of trailing whitespace.
func (z *ZellijBackend) Capture(windowID string, lines int) (string, error) {
	pid := z.paneID(windowID)

	out, err := z.zellijOutput("dump-screen", "--pane-id", pid, "--full")
	if err != nil {
		return "", err
	}

	// If lines is positive, take only the last N lines.
	if lines > 0 {
		allLines := strings.Split(out, "\n")
		if len(allLines) > lines {
			allLines = allLines[len(allLines)-lines:]
		}
		out = strings.Join(allLines, "\n")
	}

	return out, nil
}

// CheckAlive is the structured probe for the typed observation contract. It
// returns (true, nil) when the exact pane exists, (false, ErrPaneNotFound)
// when the parsed authoritative pane list confirms the pane is absent, and an
// operational error for every other failure (command failure, malformed JSON).
// An operational failure is never authoritative absence.
func (z *ZellijBackend) CheckAlive(windowID string) (bool, error) {
	pid := z.paneID(windowID)

	bin, err := zellijBin()
	if err != nil {
		return false, err
	}

	out, stderr, err := runZellijCommand(bin, "--session", z.Session, "action", "list-panes", "--json")
	if err != nil {
		return false, wrapBackendCommandError("zellij: listing panes", out, stderr, err)
	}

	var panes []zellijPaneEntry
	if err := json.Unmarshal(out, &panes); err != nil {
		return false, fmt.Errorf("zellij: malformed pane list: %w", err)
	}

	// Normalize the pane ID to a numeric value (strip "terminal_" prefix).
	targetID := normalizePaneID(pid)
	for _, p := range panes {
		if !p.IsPlugin && p.Exited {
			continue
		}
		if fmt.Sprintf("terminal_%d", p.ID) == pid || p.ID == targetID {
			return !p.Exited, nil
		}
	}
	// The parsed authoritative list lacks the exact pane: confirmed absent.
	return false, ErrPaneNotFound
}

// normalizePaneID extracts the numeric ID from a pane identifier like "terminal_3" -> 3.
// Returns -1 if the ID cannot be parsed.
func normalizePaneID(pid string) int {
	var n int
	if _, err := fmt.Sscanf(pid, "terminal_%d", &n); err == nil {
		return n
	}
	if _, err := fmt.Sscanf(pid, "%d", &n); err == nil {
		return n
	}
	return -1
}

// Teardown closes the pane identified by windowID.
// Errors from zellij close-pane are silently ignored if the pane is already gone.
// windowID may be "<session>:<pane_id>" or a bare pane ID.
func (z *ZellijBackend) Teardown(windowID string) error {
	pid := z.paneID(windowID)

	_ = z.zellijRun("close-pane", "--pane-id", pid)
	return nil
}
