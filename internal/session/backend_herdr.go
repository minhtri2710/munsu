package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// HerdrBackend implements Backend using the herdr CLI.
// Every CLI invocation passes --session <name> and sets HERDR_SESSION env.
type HerdrBackend struct {
	// Session is the herdr session name. Default from HERDR_SESSION env or "default".
	Session string
	// Cwd is the working directory for the new tab (set by spawn before NewWindow).
	Cwd string

	// TeardownTabID is a durable tab ID set from task metadata for teardown,
	// used when the backend is reconstructed post-spawn (no in-memory lastCreate).
	TeardownTabID string

	// TeardownWorkspaceID is a durable workspace ID set from task metadata
	// for teardown workspace/husk cleanup.
	TeardownWorkspaceID string

	// Hometag is the hometag of the munsu home that owns this backend.
	// Set during BackendForTask to enable label-safe workspace close.
	Hometag string

	// DenyCloseWorkspaceIDs lists workspace IDs that must NOT be closed
	// during teardown, even if label matches hometag. Populated by teardown.Run
	// when other tasks still reference the same workspace.
	DenyCloseWorkspaceIDs []string

	// lastCreate stores the last tab-creation result for MetaExtras.
	lastCreate *herdrLastCreate
}

// herdrLastCreate captures the IDs from the most recent NewWindow call.
type herdrLastCreate struct {
	Session     string
	WorkspaceID string
	TabID       string
	PaneID      string
}

// NewHerdrBackend creates a HerdrBackend with the given session name.
// If session is empty, it defaults to os.Getenv("HERDR_SESSION") then "default".
func NewHerdrBackend(session string) *HerdrBackend {
	if session == "" {
		session = os.Getenv("HERDR_SESSION")
	}
	if session == "" {
		session = "default"
	}
	return &HerdrBackend{Session: session}
}

// SeedTeardownTab sets a durable tab ID for teardown from task metadata.
// Used when the backend is reconstructed post-spawn (no in-memory lastCreate).
func (h *HerdrBackend) SeedTeardownTab(tabID string) {
	h.TeardownTabID = tabID
}

// workspaceIDToClose returns the workspace ID to close during teardown.
// Checks lastCreate first, then TeardownWorkspaceID (from meta).
func (h *HerdrBackend) workspaceIDToClose() string {
	if h.lastCreate != nil && h.lastCreate.WorkspaceID != "" {
		return h.lastCreate.WorkspaceID
	}
	return h.TeardownWorkspaceID
}

// effectiveSession returns the session for a given window handle.
// If the window handle has a session prefix that differs from h.Session,
// it uses the window's session (belt-and-suspenders).
func (h *HerdrBackend) effectiveSession(windowID string) string {
	sess, _ := ParseWindow(windowID)
	if sess != "" && sess != h.Session {
		return sess
	}
	return h.Session
}

// sessionArgs returns the --session flag argument for a herdr CLI call.
func (h *HerdrBackend) sessionArgs() []string {
	return []string{"--session", h.Session}
}

func (h *HerdrBackend) herdr(args ...string) (string, error) {
	bin, err := exec.LookPath("herdr")
	if err != nil {
		return "", fmt.Errorf("herdr: not found on PATH: %w", err)
	}

	// Prepend --session flags to every call.
	fullArgs := append(h.sessionArgs(), args...)
	cmd := exec.Command(bin, fullArgs...)
	cmd.Env = append(os.Environ(), "HERDR_SESSION="+h.Session)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("herdr %v: %s", fullArgs, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("herdr %v: %w", fullArgs, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (h *HerdrBackend) herdrForWindow(windowID string, args ...string) (string, error) {
	sess := h.effectiveSession(windowID)
	if sess != h.Session {
		tmp := *h
		tmp.Session = sess
		return tmp.herdr(args...)
	}
	return h.herdr(args...)
}

// isNotFoundErr returns true for 'not found' / 'not_found' / '_not_found' herdr errors.
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not found") || strings.Contains(msg, "not_found")
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
	TabCount    int    `json:"tab_count"`
	AgentStatus string `json:"agent_status"`
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

	// Workspace not found — create it with --no-focus.
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
// Returns the window handle in "<session>:<pane_id>" format.
func (h *HerdrBackend) NewWindow(session, name string) (string, error) {
	wsName := session
	if wsName == "" {
		wsName = h.Session
	}

	workspaceID, err := h.findOrCreateWorkspace(wsName)
	if err != nil {
		return "", err
	}

	tabArgs := []string{"tab", "create", "--workspace", workspaceID, "--label", name, "--no-focus"}
	if h.Cwd != "" {
		tabArgs = append(tabArgs, "--cwd", h.Cwd)
	}
	out, err := h.herdr(tabArgs...)
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

	tabID := resp.Result.Tab.TabID

	// Store last create metadata so MetaExtras can return it.
	h.lastCreate = &herdrLastCreate{
		Session:     h.Session,
		WorkspaceID: workspaceID,
		TabID:       tabID,
		PaneID:      paneID,
	}

	// Return "<session>:<pane_id>" for window handle.
	return h.Session + ":" + paneID, nil
}

// ParseWindow splits a window handle ("session:pane_id") on the first colon.
// Returns the session name and the pane ID. If no colon is found, returns "" and the full string.
func ParseWindow(handle string) (session, paneID string) {
	idx := strings.Index(handle, ":")
	if idx < 0 {
		return "", handle
	}
	return handle[:idx], handle[idx+1:]
}

// paneID extracts the pane ID part from a window handle (session:pane or bare pane).
func paneID(windowID string) string {
	_, p := ParseWindow(windowID)
	return p
}

// SendKeys sends text followed by Enter to the pane identified by windowID.
// windowID may be "<session>:<pane_id>" or a bare pane ID.
func (h *HerdrBackend) SendKeys(windowID, text string) error {
	pid := paneID(windowID)
	if _, err := h.herdrForWindow(windowID, "pane", "send-text", pid, text); err != nil {
		return err
	}
	_, err := h.herdrForWindow(windowID, "pane", "send-keys", pid, "Enter")
	return err
}

// Capture reads the last N lines of output from the pane.
// windowID may be "<session>:<pane_id>" or a bare pane ID.
func (h *HerdrBackend) Capture(windowID string, lines int) (string, error) {
	return h.herdrForWindow(windowID, "pane", "read", paneID(windowID), "--source", "recent", "--lines", fmt.Sprintf("%d", lines))
}

// Alive checks whether the pane still exists via herdr pane get.
// windowID may be "<session>:<pane_id>" or a bare pane ID.
func (h *HerdrBackend) Alive(windowID string) bool {
	_, err := h.herdrForWindow(windowID, "pane", "get", paneID(windowID))
	return err == nil
}

// tabIDToClose returns the tab ID to close during teardown.
// Checks lastCreate first, then TeardownTabID (from meta).
func (h *HerdrBackend) tabIDToClose() string {
	if h.lastCreate != nil && h.lastCreate.TabID != "" {
		return h.lastCreate.TabID
	}
	return h.TeardownTabID
}

// Teardown closes the pane. When tab_id is known from last create or seeded from
// meta, it also closes the tab idempotently to ensure no husk remains.
// windowID may be "<session>:<pane_id>" or a bare pane ID.
// Idempotent: "not found" errors are silently ignored.
// Teardown closes the pane and tab. After tab close, if we have a workspace_id
// with matching hometag label and no deny-list entries, we close the workspace
// to prevent husk tabs. Idempotent: 'not found' errors are silently ignored.
func (h *HerdrBackend) Teardown(windowID string) error {
	pid := paneID(windowID)

	// Close pane first (idempotent)
	_, err := h.herdrForWindow(windowID, "pane", "close", pid)
	if err != nil && !isNotFoundErr(err) {
		return err
	}

	// Close tab (idempotent)
	tabID := h.tabIDToClose()
	if tabID != "" {
		_, tabErr := h.herdrForWindow(windowID, "tab", "close", tabID)
		if tabErr != nil && !isNotFoundErr(tabErr) {
			return tabErr
		}
	}

	// Husk cleanup: if workspace ID is known and safe, close the workspace.
	wsID := h.workspaceIDToClose()
	if wsID == "" {
		return nil
	}

	// Check deny list first
	for _, denied := range h.DenyCloseWorkspaceIDs {
		if denied == wsID {
			return nil // another task still references this workspace
		}
	}

	// List workspaces to check label
	out, wsErr := h.herdr("workspace", "list")
	if wsErr != nil {
		return nil // best-effort
	}

	var resp herdrWorkspaceListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil // best-effort
	}

	for _, ws := range resp.Result.Workspaces {
		if ws.WorkspaceID == wsID && ws.Label != "" && ws.Label == h.Hometag {
			// Label matches hometag — safe to close this workspace
			_, closeErr := h.herdr("workspace", "close", wsID)
			if isNotFoundErr(closeErr) {
				return nil // already gone
			}
			return closeErr
		}
	}

	return nil
}

// MetaExtras returns extra metadata fields from the last tab creation.
// Implements the optional BackendMetaExtras interface.
func (h *HerdrBackend) MetaExtras() map[string]string {
	if h.lastCreate == nil {
		return nil
	}
	return map[string]string{
		"herdr_session":      h.lastCreate.Session,
		"herdr_workspace_id": h.lastCreate.WorkspaceID,
		"herdr_tab_id":       h.lastCreate.TabID,
		"herdr_pane_id":      h.lastCreate.PaneID,
	}
}
