package backend

import (
	"context"
	"encoding/json"
	"errors"
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
	// during teardown, even if label matches hometag. Populated by fleet.Run
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
				// Check for protocol_mismatch error code.
				if herr := parseHerdrError(fmt.Errorf("%s", combined)); herr != nil && herr.Code == HerdrErrProtocolMismatch {
					return "", &HerdrCLIError{
						Code:    HerdrErrProtocolMismatch,
						Message: fmt.Sprintf("herdr %v: %s", fullArgs, combined),
					}
				}
				return "", fmt.Errorf("herdr %v: %s", fullArgs, combined)
			}
		}
		// Check for protocol_mismatch in the error message directly.
		if isHerdrProtocolMismatch(err) {
			return "", &HerdrCLIError{
				Code:    HerdrErrProtocolMismatch,
				Message: err.Error(),
			}
		}
		return "", fmt.Errorf("herdr %v: %w", fullArgs, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// herdrCaptureOutput runs herdr and returns stdout/stderr combined output even
// when the command exits non-zero. Needed for parsing structured JSON error
// responses like agent_prompt_stalled or agent_not_found where stdout carries
// the error payload while the exit code is non-zero.
func (h *HerdrBackend) herdrCaptureOutput(args ...string) (string, error) {
	bin, err := exec.LookPath("herdr")
	if err != nil {
		return "", fmt.Errorf("herdr: not found on PATH: %w", err)
	}

	fullArgs := append(h.sessionArgs(), args...)
	cmd := exec.Command(bin, fullArgs...)
	cmd.Env = append(os.Environ(), "HERDR_SESSION="+h.Session)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		return output, err
	}
	return output, nil
}

func (h *HerdrBackend) herdrCaptureForWindow(windowID string, args ...string) (string, error) {
	sess := h.effectiveSession(windowID)
	if sess != h.Session {
		tmp := *h
		tmp.Session = sess
		return tmp.herdrCaptureOutput(args...)
	}
	return h.herdrCaptureOutput(args...)
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

// isHerdrWaitTimeout returns true when the herdr CLI reported a structured
// 'timeout' error — its own bounded wait elapsed before a status change. This
// is the normal bounded-wait outcome and must be classified as a context
// deadline (poll fallback), never as a generic reader failure.
func isHerdrWaitTimeout(err error) bool {
	if err == nil {
		return false
	}
	if herr := parseHerdrError(err); herr != nil {
		return herr.Code == HerdrErrTimeout
	}
	// No structured envelope: this is not a typed timeout; leave it to the
	// generic reader-failure path (no textual guessing here).
	return false
}

// isNotFoundErr returns true for structured 'not found' / 'pane_not_found' herdr errors.
// Prefers typed error code matching over textual substring for known codes;
// falls back to textual matching for legacy/unknown error formats.
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	// Exec/transport failures (herdr binary missing from PATH, spawn errors)
	// are backend failures — never authoritative pane absence. Classifying
	// them as not-found would let a generic backend failure authorize
	// captain auto-relaunch.
	if errors.Is(err, exec.ErrNotFound) {
		return false
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return false
	}
	// Try structured error code first.
	if herr := parseHerdrError(err); herr != nil {
		switch herr.Code {
		case HerdrErrPaneNotFound, HerdrErrWorkspaceNotFound, HerdrErrTabNotFound:
			return true
		}
		return false
	}
	// Legacy fallback: textual substring matching.
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

// herdrTabListResponse parses the JSON response of `herdr tab list`.
type herdrTabListResponse struct {
	Result struct {
		Tabs []struct {
			Label string `json:"label"`
			TabID string `json:"tab_id"`
		} `json:"tabs"`
	} `json:"result"`
}

// herdrPaneListResponse parses the JSON response of `herdr pane list`.
type herdrPaneListResponse struct {
	Result struct {
		Panes []struct {
			PaneID string `json:"pane_id"`
			TabID  string `json:"tab_id"`
		} `json:"panes"`
	} `json:"result"`
}

// findTabByLabel returns the tab id of the exactly-labeled tab in the
// workspace, or "" when absent. Multiple tabs with the same label fail
// closed as ambiguous (a launch reservation must own exactly one tab).
func (h *HerdrBackend) findTabByLabel(workspaceID, label string) (string, error) {
	out, err := h.herdr("tab", "list", "--workspace", workspaceID)
	if err != nil {
		return "", fmt.Errorf("listing tabs: %w", err)
	}
	var resp herdrTabListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("parsing tab list: %w", err)
	}
	var matches []string
	for _, tab := range resp.Result.Tabs {
		if tab.Label == label {
			matches = append(matches, tab.TabID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", nil
	default:
		return "", fmt.Errorf("herdr find-or-create: %d tabs labeled %q in workspace %s; ambiguous", len(matches), label, workspaceID)
	}
}

// findPaneForTab returns the pane id of the tab in the workspace, or "" when
// the tab has no pane (a tab without a pane cannot be a live endpoint; the
// caller fails closed instead of creating a replacement).
func (h *HerdrBackend) findPaneForTab(workspaceID, tabID string) (string, error) {
	out, err := h.herdr("pane", "list", "--workspace", workspaceID)
	if err != nil {
		return "", fmt.Errorf("listing panes: %w", err)
	}
	var resp herdrPaneListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("parsing pane list: %w", err)
	}
	for _, pane := range resp.Result.Panes {
		if pane.TabID == tabID {
			return pane.PaneID, nil
		}
	}
	return "", nil
}

// FindOrCreateWindow finds-or-creates the tab owned by the launch
// reservation's generation-scoped window label inside the workspace label. It
// is the real reservation-aware find-or-create contract: repeating the same
// label (the same launch reservation) returns the SAME tab — the one created
// by an earlier attempt — never a replacement. The tab's pane id is resolved
// from the live pane list; a labeled tab without a pane, or multiple tabs
// with the same label, fail closed.
func (h *HerdrBackend) FindOrCreateWindow(session, name string) (string, error) {
	wsName := session
	if wsName == "" {
		wsName = h.Session
	}
	workspaceID, err := h.findOrCreateWorkspace(wsName)
	if err != nil {
		return "", err
	}
	tabID, err := h.findTabByLabel(workspaceID, name)
	if err != nil {
		return "", err
	}
	if tabID == "" {
		return h.NewWindow(session, name)
	}
	paneID, err := h.findPaneForTab(workspaceID, tabID)
	if err != nil {
		return "", err
	}
	if paneID == "" {
		return "", fmt.Errorf("herdr find-or-create: tab %q labeled %q in workspace %s has no pane; refuse to create a replacement", tabID, name, workspaceID)
	}
	// Populate lastCreate so MetaExtras carries the recovered endpoint's
	// session/workspace/tab identity exactly like a fresh create.
	h.lastCreate = &herdrLastCreate{
		Session:     h.Session,
		WorkspaceID: workspaceID,
		TabID:       tabID,
		PaneID:      paneID,
	}
	return h.Session + ":" + paneID, nil
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

// CheckAgentAlive implements session.AgentAwareBackend.
// Returns:
//
//	(true, true, nil)  — pane exists and agent is registered
//	(true, false, nil) — pane exists but no agent (bare shell / dead agent;
//	  NOT authoritative absence — recovery must fail closed, never auto-recover)
//	(false, false, ErrPaneNotFound) — pane confirmed absent
//	(false, false, err) — backend resolution failure (fail closed)
func (h *HerdrBackend) CheckAgentAlive(windowID string) (bool, bool, error) {
	// First verify pane exists.
	alive, err := h.CheckAlive(windowID)
	if err != nil {
		return false, false, err
	}
	if !alive {
		return false, false, ErrPaneNotFound
	}

	// Pane exists; now check agent registration.
	pid := herdrPaneID(windowID)
	out, agentErr := h.herdrCaptureForWindow(windowID, "agent", "get", pid)
	if agentErr != nil {
		// Try to parse JSON error from output.
		if out != "" {
			var errResp struct {
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if jsonErr := json.Unmarshal([]byte(out), &errResp); jsonErr == nil && errResp.Error != nil {
				if errResp.Error.Code == "agent_not_found" {
					return true, false, nil
				}
			}
		}
		// Other agent get errors: fail closed.
		return false, false, agentErr
	}

	var resp herdrAgentGetResponse
	if jsonErr := json.Unmarshal([]byte(out), &resp); jsonErr != nil {
		return false, false, fmt.Errorf("parsing agent get response: %w", jsonErr)
	}

	if resp.Error != nil && resp.Error.Code == "agent_not_found" {
		return true, false, nil
	}
	if resp.Error != nil {
		return false, false, fmt.Errorf("agent get error: %s", resp.Error.Message)
	}

	if resp.Result == nil || !isAgentStatusAlive(resp.Result.Agent.AgentStatus) {
		return true, false, nil
	}

	return true, true, nil
}

func isAgentStatusAlive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "idle", "done", "working", "busy", "blocked", "unknown":
		return true
	default:
		return false
	}
}

// AgentStatusReady reports whether a herdr agent status string means the
// agent is ready for a prompt. It is the single owner of readiness
// normalisation: readiness for a prompt must be decided through this
// predicate, never by comparing the raw status string a backend returns.
// "idle" and "done" are ready, after ToLower+TrimSpace exactly like its live
// sibling isAgentStatusAlive. The CLI probe path (probeCaptainBackend) is the
// call site that makes this reachable from the binary (BEO-117): before that
// fix, readiness was computed inline against the unnormalized string, so a
// healthy captain reporting "Idle" or " idle " was rejected permanently.
func AgentStatusReady(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "idle", "done":
		return true
	default:
		return false
	}
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
	// Reject protocol_mismatch (fail closed).
	if isHerdrProtocolMismatch(err) {
		return false, fmt.Errorf("herdr protocol mismatch: %w", err)
	}
	return false, err
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

// Wait implements ObservationEventSource over the herdr agent status-wait
// surface (BEO-17/P1b). The backend's configured session and identity are
// used; wire/protocol detail stays adapter-owned. See HerdrEventSource for
// the capability/version gate and normalization contract.
func (h *HerdrBackend) Wait(ctx context.Context, endpoint EndpointRef, after EventCursor) (ObservationSignal, error) {
	src := &HerdrEventSource{Session: h.Session, CLIPath: ""}
	return src.Wait(ctx, endpoint, after)
}

// After implements the adapter-owned cursor ordering for the herdr surface
// (BEO-17/P1b). See HerdrEventSource.After: cursor semantics stay adapter-
// owned; the orchestrator never parses or compares cursors itself.
func (h *HerdrBackend) After(next, prev EventCursor) bool {
	src := &HerdrEventSource{Session: h.Session, CLIPath: ""}
	return src.After(next, prev)
}

// Capability probes the installed herdr CLI and returns capability info.
// It discovers the CLI path from PATH resolution (not a stored field) to
// reflect actual runtime state. The result is suitable for caching by callers
// that need repeated access within a single operation.
func (h *HerdrBackend) Capability() CapabilityInfo {
	return ProbeHerdrCapability("")
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
// Returns (version, nil) on success. Returns (0, err) on probe failure
// (no server, CLI not found, etc.) — caller should treat this as
// backend-failed, not unsupported.
func (h *HerdrBackend) protocolVersion() (int, error) {
	if h.protocolCache != 0 {
		if h.protocolCache < 0 {
			return 0, fmt.Errorf("protocol probe failed previously")
		}
		return h.protocolCache, nil
	}

	out, err := h.herdrCaptureOutput("api", "schema")
	if err != nil {
		h.protocolCache = -1
		return 0, fmt.Errorf("protocol probe: %w", err)
	}

	// The text output contains "protocol: <N>" on one of the lines.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "protocol:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "protocol:"))
			p, parseErr := strconv.Atoi(v)
			if parseErr != nil {
				h.protocolCache = -1
				return 0, fmt.Errorf("protocol version parse: %w", parseErr)
			}
			h.protocolCache = p
			return p, nil
		}
	}

	h.protocolCache = -1
	return 0, fmt.Errorf("protocol version not found in output: %s", out[:min(len(out), 200)])
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
// live agent via `herdr agent get`. Returns:
//
//	(true, status) — recognized agent with status string
//	(false, "") — agent_not_found (may be alive non-agent pane or dead pane)
//
// Callers should distinguish alive-non-agent from dead by calling CheckAlive
// when IsRecognizedAgent returns (false, "").
func (h *HerdrBackend) IsRecognizedAgent(windowID string) (bool, string) {
	pid := herdrPaneID(windowID)
	out, err := h.herdrCaptureForWindow(windowID, "agent", "get", pid)
	if err != nil {
		// Try to parse JSON error from output.
		if out != "" {
			var errResp struct {
				Error *struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if jsonErr := json.Unmarshal([]byte(out), &errResp); jsonErr == nil && errResp.Error != nil {
				if errResp.Error.Code == "agent_not_found" {
					return false, ""
				}
			}
		}
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
// without --wait, returning immediately on acceptance. Returns typed PromptResult.
//
// Preconditions checked before submission:
//  1. Server protocol version >= 17 (otherwise returns PromptUnsupported).
//     If the protocol probe itself fails (no server), returns PromptBackendFailed.
//  2. Target is a recognized live agent (otherwise returns PromptEndpointDead
//     or PromptUnsupported if pane exists but is not an agent).
//
// On success, returns PromptSubmitted with the agent status in detail.
// Submission acknowledgment means accepted/queued only, never processing
// completion. Do not use AgentPrompt when settled agent lifecycle state is
// needed — use a separate explicit wait operation for that.
//
// On stalled/error:
//   - agent_prompt_stalled → PromptStalled (NO fallback)
//   - agent_not_found → PromptEndpointDead (if pane absent) or PromptUnsupported
//   - other errors → PromptBackendFailed
func (h *HerdrBackend) AgentPrompt(windowID, text string) PromptResult {
	// Precondition: probe protocol version.
	pv, pErr := h.protocolVersion()
	if pErr != nil {
		return PromptResult{
			Status: PromptBackendFailed,
			Detail: fmt.Sprintf("protocol probe failed: %v", pErr),
			Err:    pErr,
		}
	}
	if pv < 17 {
		return PromptResult{
			Status: PromptUnsupported,
			Detail: fmt.Sprintf("herdr protocol v%d < 17", pv),
		}
	}

	pid := herdrPaneID(windowID)

	// Precondition: target is a recognized live agent.
	recognized, _ := h.IsRecognizedAgent(windowID)
	if !recognized {
		// agent_not_found: check if pane exists to distinguish dead from non-agent.
		alive, aliveErr := h.CheckAlive(windowID)
		if aliveErr != nil {
			// If CheckAlive itself fails, we don't know — backend-failed.
			if aliveErr == ErrPaneNotFound {
				return PromptResult{
					Status: PromptEndpointDead,
					Detail: "pane not found",
				}
			}
			return PromptResult{
				Status: PromptEndpointDead,
				Detail: fmt.Sprintf("pane liveness check failed: %v", aliveErr),
			}
		}
		if alive {
			// Pane exists but is not a recognized agent: unsupported.
			return PromptResult{
				Status: PromptUnsupported,
				Detail: "pane exists but is not a recognized agent",
			}
		}
		// Pane confirmed dead.
		return PromptResult{
			Status: PromptEndpointDead,
			Detail: "pane not alive",
		}
	}

	// Submit prompt without --wait. Returns immediately on acceptance;
	// the agent may be idle or still working on a previous prompt.
	// Use herdrCaptureForWindow to preserve stdout even on non-zero exit
	// (stalled returns exit 1 with JSON error in stdout).
	out, err := h.herdrCaptureForWindow(windowID, "agent", "prompt", pid, text)
	if err != nil {
		// If stdout is empty or unparseable, it's a true backend failure.
		if out == "" {
			return PromptResult{
				Status: PromptBackendFailed,
				Detail: "herdr agent prompt: empty response on error",
				Err:    err,
			}
		}

		// Try to parse structured JSON error.
		var errResp struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if jsonErr := json.Unmarshal([]byte(out), &errResp); jsonErr == nil && errResp.Error != nil {
			switch errResp.Error.Code {
			case "agent_prompt_stalled":
				return PromptResult{
					Status: PromptStalled,
					Detail: errResp.Error.Message,
				}
			case "agent_not_found":
				// Could have disappeared between agent get and prompt.
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

		// Unparseable error output: backend failure.
		return PromptResult{
			Status: PromptBackendFailed,
			Detail: fmt.Sprintf("herdr agent prompt: unparseable error: %s", out[:min(len(out), 500)]),
			Err:    err,
		}
	}

	// Parse success response.
	var successResp struct {
		Result *struct {
			Type  string `json:"type"`
			Agent *struct {
				AgentStatus string `json:"agent_status"`
			} `json:"agent,omitempty"`
		} `json:"result"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &successResp); jsonErr != nil || successResp.Result == nil {
		return PromptResult{
			Status: PromptBackendFailed,
			Detail: "unparseable herdr agent prompt response",
			Err:    fmt.Errorf("json parse error: %v, raw: %s", jsonErr, out[:min(len(out), 500)]),
		}
	}

	// Return PromptSubmitted with agent status from response.
	// Without --wait we do not infer queued-while-busy; the submission
	// acknowledgment means accepted/queued only.
	if successResp.Result.Agent != nil {
		return PromptResult{
			Status: PromptSubmitted,
			Detail: fmt.Sprintf("agent-status: %s", successResp.Result.Agent.AgentStatus),
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
