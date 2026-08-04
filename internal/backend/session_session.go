// Package session provides the session backend interface and resolution.
//
// The Backend interface abstracts session management operations. Resolution
// consumes ONE explicitly requested backend identity; an absent or unhealthy
// requested adapter fails closed with a typed error. No auto-detection.
package backend

import (
	"errors"
	"fmt"
	"os/exec"
)

// ErrPaneNotFound is returned by session backends when a pane is confirmed not found or dead.
var ErrPaneNotFound = errors.New("pane not found")

// ErrAgentNotFound is returned by agent-aware backends when a pane exists but
// has no registered agent (bare shell or agent process missing). Recovery-critical
// paths should treat this as "not alive for agent purposes" to trigger auto-recover.
var ErrAgentNotFound = errors.New("agent not found in pane")

// Backend defines the operations for managing agent session windows.
type Backend interface {
	NewWindow(session, name string) (string, error)
	SendKeys(windowID, text string) error
	Capture(windowID string, lines int) (string, error)
	Alive(windowID string) bool
	Teardown(windowID string) error
}

// AgentAwareBackend is an optional interface that extends Backend with
// agent-aware liveness checking. Backends that support agent registration
// (e.g., Herdr) should implement this to provide recovery-grade ground truth.
//
// CheckAgentAlive returns:
//
//	(true, true, nil)  — pane exists and agent is registered (recovery: alive)
//	(true, false, nil) — pane exists but no agent (recovery: dead agent → auto-recover)
//	(false, false, ErrPaneNotFound) — pane confirmed absent
//	(false, false, err) — backend resolution failure (recovery: fail closed)
type AgentAwareBackend interface {
	CheckAgentAlive(windowID string) (alive bool, agentAlive bool, err error)
}

// BackendMetaExtras is an optional interface that a Backend can implement
// to provide extra metadata fields to write into task meta after NewWindow.
type BackendMetaExtras interface {
	MetaExtras() map[string]string
}

// Select verifies the REQUESTED capability health and returns the named
// backend. Supported: "tmux", "herdr", "zellij", "cmux", "orca" (experimental).
// The requested binary must be present on PATH (verify-requested-adapter,
// mirroring ProbeHerdrCapability); an absent or unknown adapter is a typed
// failure — no alternate selection, no implicit switch.
func Select(name string) (Backend, error) {
	switch name {
	case "herdr":
		if _, err := exec.LookPath("herdr"); err != nil {
			return nil, fmt.Errorf("herdr: not found on PATH")
		}
		return NewHerdrBackend(""), nil
	case "tmux":
		if _, err := tmuxBin(); err != nil {
			return nil, err
		}
		return &TmuxBackend{}, nil
	case "zellij":
		if _, err := zellijBin(); err != nil {
			return nil, err
		}
		return NewZellijBackend(""), nil
	case "cmux":
		if _, err := cmuxBin(); err != nil {
			return nil, err
		}
		return newCmuxBackend(), nil
	case "orca":
		if _, err := orcaBin(); err != nil {
			return nil, err
		}
		return NewOrcaBackend(), nil
	default:
		return nil, fmt.Errorf("unknown session backend: %q (supported: tmux, herdr, zellij, cmux, orca)", name)
	}
}

// Resolve consumes ONE explicitly requested backend identity. The homeDir is
// used ONLY for workspace labeling (e.g. tmux Hometag), NEVER for selection.
// An empty requested identity is a typed failure — no config-file read, no
// env marker, no PATH auto-detect.
//
// Returns the backend, its resolved name, and any error.
// Important: for the "herdr" backend, Session is set to "" (→ HERDR_SESSION or "default"),
// NOT the home-derived hometag. The hometag is the workspace label, passed separately
// by spawn to NewWindow. See BackendForTask for session binding from task metadata.
func Resolve(homeDir string, name string) (Backend, string, error) {
	if name == "" || name == "auto" {
		return nil, "", fmt.Errorf("no session backend identity: %q is not an explicit backend (tmux, herdr, zellij, cmux, orca); no auto-detection", name)
	}

	tag := Hometag(homeDir)
	switch name {
	case "tmux":
		return &TmuxBackend{Tag: tag}, "tmux", nil
	case "herdr":
		// NEVER pass hometag as Herdr session. Hometag is the workspace label,
		// passed separately by spawn to NewWindow. The Herdr session is the server
		// name (HERDR_SESSION or "default").
		return NewHerdrBackend(""), "herdr", nil
	case "zellij":
		return NewZellijBackend(""), "zellij", nil
	case "cmux":
		return newCmuxBackend(), "cmux", nil
	case "orca":
		return NewOrcaBackend(), "orca", nil
	default:
		return nil, "", fmt.Errorf("unknown session backend: %q (supported: tmux, herdr, zellij, cmux, orca)", name)
	}
}

// BackendForTask resolves the session backend durably bound in task metadata.
// ONLY meta["backend"] is consumed: if the bound identity is MISSING or empty,
// resolution FAILS CLOSED — it NEVER falls through to Resolve(config/env/PATH).
//
// For the "herdr" backend, the session name is resolved from (in order):
//  1. meta["herdr_session"]
//  2. ParseWindow(meta["window"]).session
//  3. HERDR_SESSION env or "default"
//
// This is session BINDING from task metadata, not selection. It ensures
// post-spawn lifecycle (peek/send/teardown) uses the correct session,
// not the hometag.
func BackendForTask(homeDir string, meta map[string]string) (Backend, string, error) {
	var bkName string
	if meta != nil {
		bkName = meta["backend"]
	}
	if bkName == "" {
		return nil, "", fmt.Errorf("no session backend identity bound in task metadata (meta[\"backend\"] missing or empty); explicit identity required — no fallback")
	}
	switch bkName {
	case "herdr":
		// Resolve session: meta herdr_session > ParseWindow(window) > env > "default"
		sess := ""
		if meta != nil {
			sess = meta["herdr_session"]
		}
		if sess == "" && meta != nil {
			wsess, _ := ParseWindow(meta["window"])
			sess = wsess
		}
		bk := NewHerdrBackend(sess)
		if meta != nil {
			// Seed durable teardown tab ID from meta
			if tabID := meta["herdr_tab_id"]; tabID != "" {
				bk.SeedTeardownTab(tabID)
			}
			// Seed durable workspace ID from meta for husk cleanup
			if wsID := meta["herdr_workspace_id"]; wsID != "" {
				bk.TeardownWorkspaceID = wsID
			}
			// Set workspace label for label-safe close. Prefer the task's own home
			// (captain homes use captain-<id>-<hash>, not the parent primary tag).
			labelHome := homeDir
			if mh := meta["home"]; mh != "" {
				labelHome = mh
			}
			bk.Hometag = WorkspaceTag(labelHome)
		}
		return bk, "herdr", nil
	case "tmux":
		bk, err := Select("tmux")
		return bk, "tmux", err
	case "zellij":
		bk, err := Select("zellij")
		return bk, "zellij", err
	case "cmux":
		bk, err := Select("cmux")
		return bk, "cmux", err
	case "orca":
		bk, err := Select("orca")
		return bk, "orca", err
	default:
		return nil, "", fmt.Errorf("unknown bound session backend: %q (supported: tmux, herdr, zellij, cmux, orca)", bkName)
	}
}
