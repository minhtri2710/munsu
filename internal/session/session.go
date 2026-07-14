// Package session provides the session backend interface and resolution.
//
// The Backend interface abstracts session management operations.
// Default() returns auto-detected backend; fallback is tmux.
package session

import (
	"fmt"
	"os"
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

// Default returns the auto-detected backend based on environment.
// Detection order: HERDR_ENV > tmux (fallback).
func Default() Backend {
	if os.Getenv("HERDR_ENV") != "" {
		return &HerdrBackend{}
	}
	return &TmuxBackend{}
}

// Resolve returns the configured backend for homeDir with optional override.
// Resolution order:
//  1. backendOverride (from --backend flag, when non-empty)
//  2. homeDir/config/backend file (first whitespace-delimited word; unknown name = error)
//  3. Runtime env markers (HERDR_ENV)
//  4. tmux (fallback)
// Returns the backend, its resolved name, and any error.
func Resolve(homeDir string, backendOverride string) (Backend, string, error) {
	name := backendOverride
	if name == "" {
		name = readConfigBackend(homeDir)
	}
	if name == "" {
		bk := Default()
		if _, ok := bk.(*HerdrBackend); ok {
			return bk, "herdr", nil
		}
		return bk, "tmux", nil
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
