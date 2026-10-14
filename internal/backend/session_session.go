// The Backend interface abstracts session management operations. Resolution
// consumes ONE explicitly requested backend identity; an absent or unhealthy
// requested adapter fails closed with a typed error. No auto-detection.

package backend

import (
	"errors"
	"fmt"
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
// absence (ErrPaneNotFound) from operational failure.
type Backend interface {
	NewWindow(session, name string) (string, error)
	// FindOrCreateWindow returns the window labeled name in session, creating
	// it when none exists; the same label always resolves to the same window
	// and an ambiguous label fails closed.
	FindOrCreateWindow(session, name string) (string, error)
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

// AgentActivityReader is an optional interface a Backend may implement to
// report the raw agent-status string for an endpoint, used to populate the
// Activity hint axis. Backends that do not implement it leave Activity
// unknown (fail closed).
type AgentActivityReader interface {
	IsRecognizedAgent(windowID string) (recognized bool, status string)
}

// AgentActivityProvider is an optional interface a Backend may implement to
// surface agent liveness and the raw agent-status hint from a single probe.
// ObserveEndpoint prefers it over the separate CheckAgentAlive +
// AgentActivityReader pair; backends without it fall back to those two.
//
// The probe must fail closed: when err is non-nil every observation result is
// zero (paneAlive, agentAlive and recognized false, status empty), so an
// errored probe can never imply pane or agent liveness to a caller that reads
// a value before checking the error.
type AgentActivityProvider interface {
	ObserveAgent(windowID string) (paneAlive bool, agentAlive bool, recognized bool, status string, err error)
}

// HumanNotifier is an optional Backend capability: show the Human a popup
// message. A backend without it has no popup; that is not an error. The call
// is bounded by the package command timeout and is never retried by the
// backend.
type HumanNotifier interface {
	Notify(title, body string) error
}

// BackendMetaExtras is an optional interface that a Backend can implement
// to provide extra metadata fields to write into task meta after NewWindow.
// Its keys are the Meta* constants.
type BackendMetaExtras interface {
	MetaExtras() map[string]string
}

// Task meta keys for the endpoint identity a backend records beyond the
// window handle. They are the .meta projection of the endpoint binding's
// SessionOwner, WorkspaceID and TabID.
const (
	MetaSessionOwner = "session_owner"
	MetaWorkspaceID  = "workspace_id"
	MetaTabID        = "tab_id"
)

// supportedBackends is the one statement of backend support that refusals
// print.
const supportedBackends = "herdr, tmux"

// setBackendHint names the command that sets a backend identity.
const setBackendHint = "set one with: munsu config set backend herdr|tmux"

// constructBackend is the ONE private construction path for session adapters:
// it verifies the REQUESTED capability health (binary present on PATH) and
// constructs the matching base adapter. Resolve and BackendForTask both
// route through this function, so an absent or unhealthy requested
// capability FAILS CLOSED at resolution with a typed failure — never deferred
// to first exec.
//
// Supported: "herdr", "tmux".
// Distinguishable failures: unknown identity ("unknown session backend: …")
// and missing binary ("<name>: not found on PATH").
func constructBackend(name string) (Backend, error) {
	switch name {
	case "herdr":
		if _, err := lookBackendBin("herdr"); err != nil {
			return nil, err
		}
		return NewHerdrBackend(""), nil
	case "tmux":
		if _, err := lookBackendBin("tmux"); err != nil {
			return nil, err
		}
		return &TmuxBackend{}, nil
	default:
		return nil, fmt.Errorf("unknown session backend: %q (supported: %s); %s", name, supportedBackends, setBackendHint)
	}
}

// requireIdentity is the one refusal of a missing backend identity, shared by
// Resolve and BackendForTask: empty and "auto" name no backend.
func requireIdentity(name string) error {
	if name == "" || name == "auto" {
		return fmt.Errorf("no session backend identity: %q is not an explicit backend (supported: %s); no auto-detection", name, supportedBackends)
	}
	return nil
}

// Resolve consumes ONE explicitly requested backend identity.
// An empty requested identity is a typed failure — no config-file read, no
// env marker, no PATH auto-detect. Capability health is verified through the
// same construction path as BackendForTask uses: an absent binary FAILS CLOSED here
// instead of deferring to first exec.
//
// Returns the backend, its resolved name, and any error.
// Important: for the "herdr" backend, Session is set to "" (→ HERDR_SESSION or "default"),
// NOT the home-derived hometag. The hometag is the workspace label, passed separately
// by spawn to NewWindow. See BackendForTask for session binding from task metadata.
func Resolve(name string) (Backend, string, error) {
	if err := requireIdentity(name); err != nil {
		return nil, "", fmt.Errorf("%w; %s", err, setBackendHint)
	}

	bk, err := constructBackend(name)
	if err != nil {
		return nil, "", err
	}
	return bk, name, nil
}

// BackendForTask resolves the session backend durably bound in task metadata.
// ONLY meta["backend"] is consumed: if the bound identity is MISSING or empty,
// resolution FAILS CLOSED — it NEVER falls through to Resolve(config/env/PATH).
//
// The bound identity goes through the SAME verified construction path as
// Resolve: an absent or unhealthy capability FAILS CLOSED at
// resolution — session binding can never bypass capability verification.
//
// For the "herdr" backend, the session name is bound from task metadata
// AFTER verification, resolved from (in order):
//  1. meta[MetaSessionOwner]
//  2. HandleSession(meta["window"])
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
	if err := requireIdentity(bkName); err != nil {
		return nil, "", fmt.Errorf("task metadata meta[\"backend\"]: %w", err)
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
			// Session binding: meta session owner > HandleSession(window)
			// > verified base (HERDR_SESSION env or "default").
			if sess := meta[MetaSessionOwner]; sess != "" {
				hb.Session = sess
			} else if wsess := HandleSession(meta["window"]); wsess != "" {
				hb.Session = wsess
			}
			// Seed durable teardown tab ID from meta
			if tabID := meta[MetaTabID]; tabID != "" {
				hb.SeedTeardownTab(tabID)
			}
			// Seed durable workspace ID from meta for husk cleanup
			if wsID := meta[MetaWorkspaceID]; wsID != "" {
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
	default:
		return nil, "", fmt.Errorf("unknown bound session backend: %q (supported: %s); munsu cannot operate this endpoint: close it in its own multiplexer", bkName, supportedBackends)
	}
}
