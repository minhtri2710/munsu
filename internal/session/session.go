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

// Resolve returns the configured backend for homeDir.
// Resolution order:
//  1. homeDir/config/backend file (first whitespace-delimited word; unknown name = error)
//  2. Runtime env markers
//  3. tmux (fallback)
func Resolve(homeDir string) (Backend, error) {
	if name := readConfigBackend(homeDir); name != "" {
		tag := hometag.Tag(homeDir)
		switch name {
		case "tmux":
			return &TmuxBackend{Tag: tag}, nil
		case "herdr":
			return &HerdrBackend{}, nil
		default:
			return Select(name)
		}
	}
	return Default(), nil
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
