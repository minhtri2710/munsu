package session

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// HerdrBackend implements Backend using the herdr CLI.
// Experimental: herdr is a terminal workspace manager.
type HerdrBackend struct{}

// herdrWorkspaceLabel is the workspace label used for all munsu tabs.
// It is set by the caller (e.g., Resolve with home-derived name).
// Workspaces are created on demand if they do not exist.
const herdrWorkspaceLabel = "munsu"

func (h *HerdrBackend) herdr(args ...string) (string, error) {
	bin, err := exec.LookPath("herdr")
	if err != nil {
		return "", fmt.Errorf("herdr: not found on PATH: %w", err)
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("herdr %v: %s", args, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("herdr %v: %w", args, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// herdrTabCreateResponse represents the JSON response from herdr tab create.
type herdrTabCreateResponse struct {
	Result struct {
		RootPane struct {
			PaneID string `json:"pane_id"`
		} `json:"root_pane"`
		Tab struct {
			TabID       string `json:"tab_id"`
			WorkspaceID string `json:"workspace_id"`
		} `json:"tab"`
	} `json:"result"`
}

// herdrWorkspaceListResponse represents the JSON response from herdr workspace list.
type herdrWorkspaceListResponse struct {
	Result struct {
		Workspaces []herdrWorkspaceEntry `json:"workspaces"`
	} `json:"result"`
}

type herdrWorkspaceEntry struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

// findOrCreateWorkspace finds a workspace by label or creates one.
func (h *HerdrBackend) findOrCreateWorkspace(label string) (string, error) {
	out, err := h.herdr("workspace", "list")
	if err != nil {
		return "", fmt.Errorf("listing workspaces: %w", err)
	}

	var resp herdrWorkspaceListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("parsing workspace list: %w", err)
	}

	for _, ws := range resp.Result.Workspaces {
		if ws.Label == label {
			return ws.WorkspaceID, nil
		}
	}

	// Workspace not found — create it.
	createOut, err := h.herdr("workspace", "create", "--label", label, "--no-focus")
	if err != nil {
		return "", fmt.Errorf("creating workspace %q: %w", label, err)
	}

	var createResp herdrWorkspaceCreateResponse
	if err := json.Unmarshal([]byte(createOut), &createResp); err != nil {
		return "", fmt.Errorf("parsing workspace create response: %w", err)
	}
	return createResp.Result.Workspace.WorkspaceID, nil
}

// herdrWorkspaceCreateResponse represents the JSON response from herdr workspace create.
type herdrWorkspaceCreateResponse struct {
	Result struct {
		Workspace struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"workspace"`
	} `json:"result"`
}

// NewWindow creates a new tab in the herdr workspace identified by session.
// The session parameter is the workspace label (e.g., derived from MUNSU_HOME).
// The name parameter is the tab label (e.g., the task ID).
// Returns the pane ID as the window handle (globally unique).
func (h *HerdrBackend) NewWindow(session, name string) (string, error) {
	wsName := session
	if wsName == "" {
		wsName = herdrWorkspaceLabel
	}

	workspaceID, err := h.findOrCreateWorkspace(wsName)
	if err != nil {
		return "", err
	}

	out, err := h.herdr("tab", "create", "--workspace", workspaceID, "--label", name)
	if err != nil {
		return "", fmt.Errorf("creating tab: %w", err)
	}

	var resp herdrTabCreateResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("parsing tab create response: %w", err)
	}

	paneID := resp.Result.RootPane.PaneID
	if paneID == "" {
		return "", fmt.Errorf("herdr tab create returned empty pane_id")
	}

	return paneID, nil
}

// SendKeys sends text followed by Enter to the pane identified by windowID (pane ID).
func (h *HerdrBackend) SendKeys(windowID, text string) error {
	if _, err := h.herdr("pane", "send-text", windowID, text); err != nil {
		return err
	}
	_, err := h.herdr("pane", "send-keys", windowID, "Enter")
	return err
}

// Capture reads the last N lines of output from the pane.
func (h *HerdrBackend) Capture(windowID string, lines int) (string, error) {
	return h.herdr("pane", "read", windowID, "--source", "recent", "--lines", fmt.Sprintf("%d", lines))
}

// Alive checks whether the pane still exists via herdr pane get.
func (h *HerdrBackend) Alive(windowID string) bool {
	_, err := h.herdr("pane", "get", windowID)
	return err == nil
}

// Teardown closes the pane. Idempotent: "not found" errors are silently ignored.
func (h *HerdrBackend) Teardown(windowID string) error {
	_, err := h.herdr("pane", "close", windowID)
	if err != nil && (strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "pane_not_found")) {
		return nil
	}
	return err
}
