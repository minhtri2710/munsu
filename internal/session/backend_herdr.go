package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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

	// protocolCache caches the detected API protocol version. -1 = not checked.
	protocolCache int

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
// For handles with 2+ colons (e.g. "default:w6E:p3"), the session prefix is extracted.
// For handles with 0-1 colons (e.g. "w6E:p3"), the backend's configured session is used.
func (h *HerdrBackend) effectiveSession(windowID string) string {
	idx := strings.Index(windowID, ":")
	if idx >= 0 && strings.Contains(windowID[idx+1:], ":") {
		return windowID[:idx]
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
			combined := strings.TrimSpace(string(ee.Stderr))
			if combined == "" {
				combined = strings.TrimSpace(string(out))
			}
			if combined != "" {
				return "", fmt.Errorf("herdr %v: %s", fullArgs, combined)
			}
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

// isNotFoundErr returns true for 'not found' / 'not_found' / 'pane_not_found' herdr errors.
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "not_found") || strings.Contains(msg, "pane_not_found")
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

// herdrPaneID extracts the pane ID part from a window handle for Herdr CLI calls.
// For handles with 2+ colons (e.g. "default:w6E:p3"), the pane ID is "w6E:p3".
// For handles with 0-1 colons (e.g. "w6E:p3"), the full handle is the pane ID.
func herdrPaneID(windowID string) string {
	idx := strings.Index(windowID, ":")
	if idx >= 0 && strings.Contains(windowID[idx+1:], ":") {
		return windowID[idx+1:]
	}
	return windowID
}

// SendKeys sends text followed by Enter to the pane identified by windowID.
// windowID may be "<session>:<pane_id>" or a bare pane ID.
func (h *HerdrBackend) SendKeys(windowID, text string) error {
	pid := herdrPaneID(windowID)
	if _, err := h.herdrForWindow(windowID, "pane", "send-text", pid, text); err != nil {
		return err
	}
	_, err := h.herdrForWindow(windowID, "pane", "send-keys", pid, "Enter")
	return err
}

// Capture reads the last N lines of output from the pane.
// windowID may be "<session>:<pane_id>" or a bare pane ID.
func (h *HerdrBackend) Capture(windowID string, lines int) (string, error) {
	return h.herdrForWindow(windowID, "pane", "read", herdrPaneID(windowID), "--source", "recent", "--lines", fmt.Sprintf("%d", lines))
}

// CheckAlive checks whether the pane still exists via herdr pane get.
// Returns (true, nil) if confirmed alive.
// Returns (false, ErrPaneNotFound) if confirmed absent (e.g. pane_not_found).
// Returns (false, err) if execution or CLI error occurred (unknown liveness).
func (h *HerdrBackend) CheckAlive(windowID string) (bool, error) {
	_, err := h.herdrForWindow(windowID, "pane", "get", herdrPaneID(windowID))
	if err == nil {
		return true, nil
	}
	if isNotFoundErr(err) {
		return false, ErrPaneNotFound
	}
	return false, err
}

// Alive checks whether the pane still exists via herdr pane get.
// windowID may be "<session>:<pane_id>" or a bare pane ID.
// Returns true ONLY when confirmed alive.
func (h *HerdrBackend) Alive(windowID string) bool {
	alive, err := h.CheckAlive(windowID)
	return err == nil && alive
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
	pid := herdrPaneID(windowID)

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

// --- Typed prompt submission ---

// herdrAPISchemaResponse represents the top-level herdr api schema response.
type herdrAPISchemaResponse struct {
	Protocol int `json:"protocol"`
}

// protocolVersion probes the herdr API protocol version once and caches it.
// Returns 0 if the probe fails (no server running, CLI not found, etc.).
func (h *HerdrBackend) protocolVersion() int {
	if h.protocolCache != 0 {
		return h.protocolCache
	}

	out, err := h.herdr("api", "schema")
	if err != nil {
		h.protocolCache = -1
		return 0
	}

	// The text output contains "protocol: <N>" on the second line.
	// Parse it from the text format (not JSON).
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "protocol:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "protocol:"))
			p, err := strconv.Atoi(v)
			if err != nil {
				h.protocolCache = -1
				return 0
			}
			h.protocolCache = p
			return p
		}
	}

	h.protocolCache = -1
	return 0
}

// herdrAgentGetResponse represents the JSON response from herdr agent get.
type herdrAgentGetResponse struct {
	Result *herdrAgentGetResult `json:"result,omitempty"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type herdrAgentGetResult struct {
	Agent struct {
		AgentStatus string `json:"agent_status"`
	} `json:"agent"`
}

// herdrAgentPromptResponse represents the JSON response from herdr agent prompt.
type herdrAgentPromptResponse struct {
	Result *herdrAgentPromptResult `json:"result,omitempty"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type herdrAgentPromptResult struct {
	Type  string          `json:"type"`
	Agent *herdrAgentInfo `json:"agent,omitempty"`
}

type herdrAgentInfo struct {
	AgentStatus string `json:"agent_status"`
}

// IsRecognizedAgent checks whether the target pane ID is a recognized
// live agent via `herdr agent get`. Returns (true, status) for recognized
// agents, (false, "") if not found or unrecognized, and (false, "") on
// probe error (falls closed to unrecognized).
func (h *HerdrBackend) IsRecognizedAgent(windowID string) (bool, string) {
	pid := herdrPaneID(windowID)
	out, err := h.herdrForWindow(windowID, "agent", "get", pid)
	if err != nil {
		return false, ""
	}

	var resp herdrAgentGetResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return false, ""
	}

	if resp.Error != nil && resp.Error.Code == "agent_not_found" {
		return false, ""
	}
	if resp.Error != nil {
		return false, ""
	}

	if resp.Result == nil || resp.Result.Agent.AgentStatus == "" {
		return false, ""
	}

	return true, resp.Result.Agent.AgentStatus
}

// AgentPrompt submits a prompt to the target agent using herdr agent prompt
// with --wait and a 60-second timeout. Returns typed PromptResult.
//
// Preconditions checked before submission:
//  1. Protocol version >= 17 (otherwise returns PromptUnsupported)
//  2. Target is a recognized live agent (otherwise returns PromptEndpointDead)
//
// If submission succeeds, the result distinguishes:
//   - submitted: agent accepted and started processing
//   - queued-while-busy: agent was already working, prompt queued
//
// On stalled/error:
//   - agent_prompt_stalled → PromptStalled (NO fallback)
//   - agent_not_found → PromptEndpointDead
//   - other errors → PromptBackendFailed
func (h *HerdrBackend) AgentPrompt(windowID, text string) PromptResult {
	// Precondition: protocol >= 17.
	pv := h.protocolVersion()
	if pv < 17 {
		return PromptResult{
			Status: PromptUnsupported,
			Detail: fmt.Sprintf("herdr protocol v%d < 17", pv),
		}
	}

	pid := herdrPaneID(windowID)

	// Precondition: target is a recognized live agent.
	recognized, status := h.IsRecognizedAgent(windowID)
	if !recognized {
		return PromptResult{
			Status: PromptEndpointDead,
			Detail: "target is not a recognized agent",
		}
	}

	// Check pre-submission status for queued-while-busy detection.
	wasBusy := status == "working"

	// Submit prompt with --wait and 120s timeout.
	out, err := h.herdrForWindow(windowID, "agent", "prompt", pid, text,
		"--wait", "--timeout", "120000")
	if err != nil {
		// Parse JSON error response.
		if out != "" {
			var errResp herdrAgentPromptResponse
			if jsonErr := json.Unmarshal([]byte(out), &errResp); jsonErr == nil && errResp.Error != nil {
				switch errResp.Error.Code {
				case "agent_prompt_stalled":
					return PromptResult{
						Status: PromptStalled,
						Detail: errResp.Error.Message,
					}
				case "agent_not_found":
					return PromptResult{
						Status: PromptEndpointDead,
						Detail: errResp.Error.Message,
					}
				default:
					return PromptResult{
						Status: PromptBackendFailed,
						Detail: fmt.Sprintf("herdr error: %s", errResp.Error.Code),
						Err:    fmt.Errorf("%s: %s", errResp.Error.Code, errResp.Error.Message),
					}
				}
			}
		}
		return PromptResult{
			Status: PromptBackendFailed,
			Detail: "herdr agent prompt call failed",
			Err:    err,
		}
	}

	// Parse success response.
	var successResp herdrAgentPromptResponse
	if jsonErr := json.Unmarshal([]byte(out), &successResp); jsonErr != nil || successResp.Result == nil {
		return PromptResult{
			Status: PromptBackendFailed,
			Detail: "unparseable herdr agent prompt response",
			Err:    fmt.Errorf("json parse error: %v, raw: %s", jsonErr, out),
		}
	}

	// Determine result based on pre-submission status and response.
	if successResp.Result.Agent != nil {
		postStatus := successResp.Result.Agent.AgentStatus
		if wasBusy {
			return PromptResult{
				Status: PromptQueuedWhileBusy,
				Detail: fmt.Sprintf("agent was working, post-status: %s", postStatus),
			}
		}
		return PromptResult{
			Status: PromptSubmitted,
			Detail: fmt.Sprintf("agent-status: %s", postStatus),
		}
	}

	// Success but no agent info — assume submitted.
	return PromptResult{
		Status: PromptSubmitted,
		Detail: "no agent status in response",
	}
}

// LegacyPrompt implements the LegacyPrompt interface for HerdrBackend.
// This is used when the protocol is < 17 and the target does not support
// AgentPrompt. It falls back to raw SendKeys (send-text + send-keys Enter)
// without typed acknowledgment.
func (h *HerdrBackend) LegacyPrompt(windowID, text string) PromptResult {
	// Fallback: use raw pane send-text + send-keys Enter (legacy protocol 16 behavior).
	if err := h.SendKeys(windowID, text); err != nil {
		if isNotFoundErr(err) {
			return PromptResult{
				Status: PromptEndpointDead,
				Detail: err.Error(),
			}
		}
		return PromptResult{
			Status: PromptBackendFailed,
			Detail: "legacy send-keys failed",
			Err:    err,
		}
	}
	return PromptResult{
		Status: PromptSubmitted,
		Detail: "legacy send-keys (protocol 16)",
		Legacy: true,
	}
}
