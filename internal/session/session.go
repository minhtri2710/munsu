// Package session provides the session backend interface and resolution.
//
// The Backend interface abstracts session management operations.
// Default() returns auto-detected backend; fallback is tmux.
package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/hometag"
)

// ErrPaneNotFound is returned by session backends when a pane is confirmed not found or dead.
var ErrPaneNotFound = errors.New("pane not found")

// Backend defines the operations for managing agent session windows.
type Backend interface {
	NewWindow(session, name string) (string, error)
	SendKeys(windowID, text string) error
	Capture(windowID string, lines int) (string, error)
	Alive(windowID string) bool
	Teardown(windowID string) error
}

// BackendMetaExtras is an optional interface that a Backend can implement
// to provide extra metadata fields to write into task meta after NewWindow.
type BackendMetaExtras interface {
	MetaExtras() map[string]string
}

// Select returns the named backend. Supported: "tmux", "herdr", "zellij", "cmux", "orca" (experimental).
// Returns an error for unknown backend names.
func Select(name string) (Backend, error) {
	switch name {
	case "herdr":
		return NewHerdrBackend(""), nil
	case "tmux":
		return &TmuxBackend{}, nil
	case "zellij":
		return NewZellijBackend(""), nil
	case "cmux":
		return newCmuxBackend(), nil
	case "orca":
		return NewOrcaBackend(), nil
	default:
		return nil, fmt.Errorf("unknown session backend: %q (supported: tmux, herdr, zellij, cmux, orca)", name)
	}
}

// Default returns the auto-detected backend based on environment and PATH.
// Detection order:
//  1. $TMUX is set → tmux
//  2. HERDR_ENV truthy → herdr
//  3. tmux on PATH → tmux (cold-start default)
//  4. nil (nothing found; caller handles error)
func Default() Backend {
	if os.Getenv("TMUX") != "" {
		return &TmuxBackend{}
	}
	if os.Getenv("HERDR_ENV") != "" {
		return NewHerdrBackend("")
	}
	if _, err := exec.LookPath("tmux"); err == nil {
		return &TmuxBackend{}
	}
	return nil
}

// Resolve returns the configured backend for homeDir with optional override.
// Resolution order:
//  1. backendOverride (from --backend flag, when non-empty and not "auto")
//  2. homeDir/config/backend file (first whitespace-delimited word; "auto" = detect)
//  3. Auto-detection: $TMUX > HERDR_ENV > tmux PATH (cold-start)
//  4. Error if nothing found
//
// Returns the backend, its resolved name, and any error.
// Important: for the "herdr" backend, Session is set to "" (→ HERDR_SESSION or "default"),
// NOT the home-derived hometag. The hometag is the workspace label, passed separately
// by spawn to NewWindow. See BackendForTask for session binding from task metadata.
func Resolve(homeDir string, backendOverride string) (Backend, string, error) {
	name := backendOverride
	if name == "" || name == "auto" {
		name = readConfigBackend(homeDir)
	}
	if name == "" || name == "auto" {
		bk := Default()
		if bk == nil {
			return nil, "", fmt.Errorf("no session backend detected: set $TMUX, set $HERDR_ENV, or ensure tmux/herdr/zellij/cmux is on PATH")
		}
		switch bk.(type) {
		case *HerdrBackend:
			return bk, "herdr", nil
		case *TmuxBackend:
			return bk, "tmux", nil
		}
	}

	tag := hometag.Tag(homeDir)
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

// BackendForTask resolves the session backend for a task using its metadata.
// When the task metadata has a non-empty "backend" field, it is used as the
// exact backend name. Otherwise, resolution falls through to the config file
// (homeDir/config/backend) and then to runtime auto-detection.
//
// For the "herdr" backend, the session name is resolved from (in order):
//  1. meta["herdr_session"]
//  2. ParseWindow(meta["window"]).session
//  3. HERDR_SESSION env or "default"
//
// This ensures post-spawn lifecycle (peek/send/teardown) uses the correct session,
// not the hometag.
func BackendForTask(homeDir string, meta map[string]string) (Backend, string, error) {
	var bkName string
	if meta != nil {
		bkName = meta["backend"]
	}
	if bkName == "" {
		return Resolve(homeDir, "")
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
			bk.Hometag = hometag.WorkspaceTag(labelHome)
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
		// Unknown backend in meta: fall through to Resolve.
		return Resolve(homeDir, "")
	}
}
func readConfigBackend(homeDir string) string {
	data, err := os.ReadFile(filepath.Join(homeDir, "config", "backend"))
	if err != nil {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
