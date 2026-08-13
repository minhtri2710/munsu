// Package session provides the session backend interface and resolution.
//
// CmuxBackend is experimental: it uses the cmux CLI (macOS terminal) for
// session management. Capture is not supported (cmux has no capture-pane
// equivalent).

package backend

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// CmuxBackend implements Backend using the cmux CLI (macOS terminal).
//
// Experimental:
//   - Capture is not supported; cmux does not provide a capture-pane equivalent.
//   - NewWindow creates a dedicated workspace per call.
//   - Alive checks workspace existence (not surface-level liveness).
//   - Teardown closes the workspace associated with the window.
type CmuxBackend struct {
	mu sync.Mutex
	// sessions maps session name → workspace_id for workspace reuse.
	sessions map[string]string
}

func newCmuxBackend() *CmuxBackend {
	return &CmuxBackend{
		sessions: make(map[string]string),
	}
}

// cmuxBin returns the path to the cmux binary.
func cmuxBin() (string, error) {
	path, err := exec.LookPath("cmux")
	if err != nil {
		return "", fmt.Errorf("cmux: not found on PATH")
	}
	return path, nil
}

// cmuxIdentifyResponse parses the JSON output from `cmux identify --json`.
type cmuxIdentifyResponse struct {
	Result cmuxIdentifyResult `json:"result"`
}

type cmuxIdentifyResult struct {
	WorkspaceID string `json:"workspace_id"`
	SurfaceID   string `json:"surface_id"`
}

// cmuxWorkspaceListResponse parses the JSON output from `cmux list-workspaces --json`.
type cmuxWorkspaceListResponse struct {
	Result cmuxWorkspaceListResult `json:"result"`
}

type cmuxWorkspaceListResult struct {
	Workspaces []cmuxWorkspaceEntry `json:"workspaces"`
}

type cmuxWorkspaceEntry struct {
	WorkspaceID string `json:"workspace_id"`
}

// cmuxOutput runs cmux with the given args and returns stdout as a trimmed string.
func cmuxOutput(args ...string) (string, error) {
	bin, err := cmuxBin()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("cmux %v: %s", args, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("cmux %v: %w", args, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// workspaceExists checks whether a workspace with the given ID exists.
func (c *CmuxBackend) workspaceExists(workspaceID string) bool {
	alive, err := c.checkWorkspaceAlive(workspaceID)
	if err != nil {
		return false
	}
	return alive
}

// checkWorkspaceAlive returns (true, nil) when the exact workspace exists,
// (false, ErrPaneNotFound) when the workspace is confirmed absent, and an
// operational error for every other failure (command failure, malformed JSON).
// This is the structured probe surface used by the typed observation contract:
// an operational failure is never authoritative absence.
func (c *CmuxBackend) checkWorkspaceAlive(workspaceID string) (bool, error) {
	if workspaceID == "" {
		return false, fmt.Errorf("cmux: empty workspace identity")
	}
	if _, err := cmuxBin(); err != nil {
		return false, err
	}
	out, err := cmuxOutput("list-workspaces", "--json")
	if err != nil {
		return false, fmt.Errorf("cmux: listing workspaces: %w", err)
	}
	var resp cmuxWorkspaceListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return false, fmt.Errorf("cmux: malformed workspace list: %w", err)
	}
	for _, ws := range resp.Result.Workspaces {
		if ws.WorkspaceID == workspaceID {
			return true, nil
		}
	}
	// The parsed authoritative list lacks the exact workspace: confirmed absent.
	return false, ErrPaneNotFound
}

// CheckAlive is the structured probe for the typed observation contract.
func (c *CmuxBackend) CheckAlive(windowID string) (bool, error) {
	wid, _ := ParseCmuxWindow(windowID)
	if wid == "" {
		return false, fmt.Errorf("cmux: invalid window handle %q", windowID)
	}
	return c.checkWorkspaceAlive(wid)
}

// NewWindow creates a new cmux workspace. The session parameter is used as
// a lookup key for workspace reuse (same session name reuses the same workspace).
// The name parameter is recorded but cmux does not support surface labels in CLI.
//
// Returns the window handle in "<workspace_id>|<surface_id>" format.
func (c *CmuxBackend) NewWindow(session, name string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, err := cmuxBin(); err != nil {
		return "", err
	}

	// Check if we already have a workspace for this session name.
	if wid, ok := c.sessions[session]; ok {
		// Workspace exists — create a new split surface in it.
		// cmux must be on the correct workspace for new-split to work.
		if _, err := cmuxOutput("select-workspace", "--workspace", wid); err != nil {
			// Workspace may have been closed externally; fall through to create new.
			delete(c.sessions, session)
		} else {
			if _, err := cmuxOutput("new-split", "right"); err != nil {
				return "", fmt.Errorf("cmux new-split in workspace %s: %w", wid, err)
			}
			ident, err := cmuxOutput("identify", "--json")
			if err != nil {
				return "", fmt.Errorf("cmux identify after split: %w", err)
			}
			var resp cmuxIdentifyResponse
			if err := json.Unmarshal([]byte(ident), &resp); err != nil {
				return "", fmt.Errorf("parsing cmux identify: %w", err)
			}
			surfaceID := resp.Result.SurfaceID
			if surfaceID == "" {
				return "", fmt.Errorf("cmux identify returned empty surface_id")
			}
			return wid + "|" + surfaceID, nil
		}
	}

	// Create a new workspace.
	if _, err := cmuxOutput("new-workspace"); err != nil {
		return "", fmt.Errorf("cmux new-workspace: %w", err)
	}

	// Get the new workspace and surface IDs.
	ident, err := cmuxOutput("identify", "--json")
	if err != nil {
		return "", fmt.Errorf("cmux identify after new workspace: %w", err)
	}
	var resp cmuxIdentifyResponse
	if err := json.Unmarshal([]byte(ident), &resp); err != nil {
		return "", fmt.Errorf("parsing cmux identify: %w", err)
	}
	wid := resp.Result.WorkspaceID
	surfaceID := resp.Result.SurfaceID
	if wid == "" {
		return "", fmt.Errorf("cmux identify returned empty workspace_id")
	}
	if surfaceID == "" {
		return "", fmt.Errorf("cmux identify returned empty surface_id")
	}

	c.sessions[session] = wid
	return wid + "|" + surfaceID, nil
}

// ParseCmuxWindow splits a cmux window handle ("workspace_id|surface_id").
func ParseCmuxWindow(handle string) (workspaceID, surfaceID string) {
	idx := strings.LastIndex(handle, "|")
	if idx < 0 {
		return "", handle
	}
	return handle[:idx], handle[idx+1:]
}

// SendKeys sends text followed by Enter to the cmux surface.
func (c *CmuxBackend) SendKeys(windowID, text string) error {
	_, surfaceID := ParseCmuxWindow(windowID)
	if surfaceID == "" {
		return fmt.Errorf("invalid cmux window handle: %q", windowID)
	}

	if _, err := cmuxBin(); err != nil {
		return err
	}

	// Send text to the specific surface.
	if _, err := cmuxOutput("send", "--surface", surfaceID, text); err != nil {
		return err
	}
	// Send Enter key.
	if _, err := cmuxOutput("send-key", "--surface", surfaceID, "enter"); err != nil {
		return err
	}
	return nil
}

// Capture is not supported by cmux. Returns an error.
func (c *CmuxBackend) Capture(windowID string, lines int) (string, error) {
	return "", fmt.Errorf("cmux: capture not supported (cmux has no capture-pane equivalent)")
}

// Teardown closes the workspace associated with windowID.
// Errors are silently ignored if the workspace is already gone.
func (c *CmuxBackend) Teardown(windowID string) error {
	wid, _ := ParseCmuxWindow(windowID)
	if wid == "" {
		return fmt.Errorf("invalid cmux window handle: %q", windowID)
	}

	if _, err := cmuxBin(); err != nil {
		return err
	}

	c.mu.Lock()
	// Clean up session mapping.
	for session, w := range c.sessions {
		if w == wid {
			delete(c.sessions, session)
			break
		}
	}
	c.mu.Unlock()

	cmuxOutput("close-workspace", "--workspace", wid) // ignore errors — may already be gone
	return nil
}
