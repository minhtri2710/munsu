// Package session provides the session backend interface and resolution.
//
// The Backend interface abstracts session management operations.
// Default() returns auto-detected backend; fallback is tmux.
package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/hometag"
)

// Backend defines the operations for managing agent session windows.
type Backend interface {
	NewWindow(session, name string) (string, error)
	SendKeys(windowID, text string) error
	Capture(windowID string, lines int) (string, error)
	Alive(windowID string) bool
	Teardown(windowID string) error
}

// Select returns the named backend. Supported: "tmux", "herdr".
// Returns an error for unknown backend names.
func Select(name string) (Backend, error) {
	switch name {
	case "herdr":
		return &HerdrBackend{}, nil
	case "tmux":
		return &TmuxBackend{}, nil
	default:
		return nil, fmt.Errorf("unknown session backend: %q (supported: tmux, herdr)", name)
	}
}

// Default returns the auto-detected backend based on environment and PATH.
// Detection order:
//  1. $TMUX is set → tmux
//  2. HERDR_ENV truthy → herdr
//  3. herdr on PATH → herdr
//  4. tmux on PATH → tmux
//  5. nil (nothing found; caller handles error)
func Default() Backend {
	if os.Getenv("TMUX") != "" {
		return &TmuxBackend{}
	}
	if os.Getenv("HERDR_ENV") != "" {
		return &HerdrBackend{}
	}
	if _, err := exec.LookPath("herdr"); err == nil {
		return &HerdrBackend{}
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
//  3. Auto-detection: $TMUX > HERDR_ENV > herdr PATH > tmux PATH
//  4. Error if nothing found
// Returns the backend, its resolved name, and any error.
func Resolve(homeDir string, backendOverride string) (Backend, string, error) {
	name := backendOverride
	if name == "" || name == "auto" {
		name = readConfigBackend(homeDir)
	}
	if name == "" || name == "auto" {
		bk := Default()
		if bk == nil {
			return nil, "", fmt.Errorf("no session backend detected: set $TMUX, set $HERDR_ENV, or ensure tmux/herdr is on PATH")
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
		return &HerdrBackend{}, "herdr", nil
	default:
		return nil, "", fmt.Errorf("unknown session backend: %q (supported: tmux, herdr)", name)
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
