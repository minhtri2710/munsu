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
// paths must NOT treat this as authoritative absence: pane-present/no-agent is
// not dead and never authorizes relaunch — it fails closed.
var ErrAgentNotFound = errors.New("agent not found in pane")

// Backend defines the operations for managing agent session windows. There is
// no boolean liveness surface: every session adapter provides a structured
// probe (CheckAlive or CheckAgentAlive) that distinguishes authoritative
// absence (ErrPaneNotFound) from operational failure (BEO-16/P1a clean cutover
// from the former Backend.Alive bool).
type Backend interface {
	NewWindow(session, name string) (string, error)
	SendKeys(windowID, text string) error
	Capture(windowID string, lines int) (string, error)
	Teardown(windowID string) error
}

// AgentAwareBackend is an optional interface that extends Backend with
// agent-aware liveness checking. Backends that support agent registration
// (e.g., Herdr) should implement this to provide recovery-grade ground truth.
//
// CheckAgentAlive returns:
//
//	(true, true, nil)  — pane exists and agent is registered (recovery: alive)
//	(true, false, nil) — pane exists but no agent (recovery: NOT dead; not
//	  authoritative absence — recovery fails closed, never auto-recover)
//	(false, false, ErrPaneNotFound) — pane confirmed absent (recovery: dead)
//	(false, false, err) — backend resolution failure (recovery: fail closed)
type AgentAwareBackend interface {
	CheckAgentAlive(windowID string) (alive bool, agentAlive bool, err error)
}

// BackendMetaExtras is an optional interface that a Backend can implement
// to provide extra metadata fields to write into task meta after NewWindow.
type BackendMetaExtras interface {
	MetaExtras() map[string]string
}

// constructBackend is the ONE private construction path for session adapters:
// it verifies the REQUESTED capability health (binary present on PATH) and
// constructs the matching base adapter. Select, Resolve, and BackendForTask
// all route through this function, so an absent or unhealthy requested
// capability FAILS CLOSED at resolution with a typed failure — never deferred
// to first exec.
//
// Supported: "tmux", "herdr", "zellij", "cmux", "orca" (experimental).
// Distinguishable failures: unknown identity ("unknown session backend: …")
// and missing binary ("<name>: not found on PATH").
func constructBackend(name string) (Backend, error) {
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

// Select verifies the REQUESTED capability health and returns the named
// backend (shared construction path: see constructBackend). Supported:
// "tmux", "herdr", "zellij", "cmux", "orca" (experimental). The requested
// binary must be present on PATH (verify-requested-adapter, mirroring
// ProbeHerdrCapability); an absent or unknown adapter is a typed failure —
// no alternate selection, no implicit switch.
func Select(name string) (Backend, error) {
	return constructBackend(name)
}

// Resolve consumes ONE explicitly requested backend identity. The homeDir is
// used ONLY for workspace labeling (e.g. tmux Hometag), NEVER for selection.
// An empty requested identity is a typed failure — no config-file read, no
// env marker, no PATH auto-detect. Capability health is verified through the
// same construction path as Select: an absent binary FAILS CLOSED here
// instead of deferring to first exec.
//
// Returns the backend, its resolved name, and any error.
// Important: for the "herdr" backend, Session is set to "" (→ HERDR_SESSION or "default"),
// NOT the home-derived hometag. The hometag is the workspace label, passed separately
// by spawn to NewWindow. See BackendForTask for session binding from task metadata.
func Resolve(homeDir string, name string) (Backend, string, error) {
	if name == "" || name == "auto" {
		return nil, "", fmt.Errorf("no session backend identity: %q is not an explicit backend (tmux, herdr, zellij, cmux, orca); no auto-detection", name)
	}

	bk, err := constructBackend(name)
	if err != nil {
		return nil, "", err
	}
	// Workspace labeling is layered on AFTER verified construction: homeDir is
	// a label source for tmux, never a selection input.
	if tb, ok := bk.(*TmuxBackend); ok {
		tb.Tag = Hometag(homeDir)
	}
	return bk, name, nil
}

// BackendForTask resolves the session backend durably bound in task metadata.
// ONLY meta["backend"] is consumed: if the bound identity is MISSING or empty,
// resolution FAILS CLOSED — it NEVER falls through to Resolve(config/env/PATH).
//
// The bound identity goes through the SAME verified construction path as
// Select/Resolve: an absent or unhealthy capability FAILS CLOSED at
// resolution — session binding can never bypass capability verification.
//
// For the "herdr" backend, the session name is bound from task metadata
// AFTER verification, resolved from (in order):
//  1. meta["herdr_session"]
//  2. ParseWindow(meta["window"]).session
//  3. HERDR_SESSION env or "default" (from the verified base construction)
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
		// Capability verification FIRST via the shared path; session binding
		// is layered on AFTER verification and can never bypass it.
		bk, err := constructBackend("herdr")
		if err != nil {
			return nil, "", err
		}
		hb := bk.(*HerdrBackend)
		if meta != nil {
			// Session binding: meta herdr_session > ParseWindow(window).session
			// > verified base (HERDR_SESSION env or "default").
			if sess := meta["herdr_session"]; sess != "" {
				hb.Session = sess
			} else if wsess, _ := ParseWindow(meta["window"]); wsess != "" {
				hb.Session = wsess
			}
			// Seed durable teardown tab ID from meta
			if tabID := meta["herdr_tab_id"]; tabID != "" {
				hb.SeedTeardownTab(tabID)
			}
			// Seed durable workspace ID from meta for husk cleanup
			if wsID := meta["herdr_workspace_id"]; wsID != "" {
				hb.TeardownWorkspaceID = wsID
			}
			// Set workspace label for label-safe close. Prefer the task's own home
			// (captain homes use captain-<id>-<hash>, not the parent primary tag).
			labelHome := homeDir
			if mh := meta["home"]; mh != "" {
				labelHome = mh
			}
			hb.Hometag = WorkspaceTag(labelHome)
		}
		return bk, "herdr", nil
	case "tmux":
		bk, err := constructBackend("tmux")
		return bk, "tmux", err
	case "zellij":
		bk, err := constructBackend("zellij")
		return bk, "zellij", err
	case "cmux":
		bk, err := constructBackend("cmux")
		return bk, "cmux", err
	case "orca":
		bk, err := constructBackend("orca")
		return bk, "orca", err
	default:
		return nil, "", fmt.Errorf("unknown bound session backend: %q (supported: tmux, herdr, zellij, cmux, orca)", bkName)
	}
}
