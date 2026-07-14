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

// ErrNotImplemented is returned when a backend operation is not available.
var ErrNotImplemented = fmt.Errorf("not yet implemented")

// Select returns the named backend. Supported: "tmux", "herdr", "zellij",
// "cmux", "orca". Falls back to tmux on unknown name.
func Select(name string) Backend {
	switch name {
	case "herdr":
		return &HerdrBackend{}
	case "zellij":
		return &ZellijBackend{}
	case "cmux":
		return &CmuxBackend{}
	case "orca":
		return &OrcaBackend{}
	default:
		return &TmuxBackend{}
	}
}

// Default returns the auto-detected backend based on environment.
// Detection order: HERDR_ENV > ZELLIJ > CMUX_SOCKET > ORCA_RUNTIME > tmux.
func Default() Backend {
	if os.Getenv("HERDR_ENV") != "" {
		return &HerdrBackend{}
	}
	if os.Getenv("ZELLIJ_SESSION_NAME") != "" {
		return &ZellijBackend{}
	}
	if os.Getenv("CMUX_SOCKET") != "" {
		return &CmuxBackend{}
	}
	if os.Getenv("ORCA_RUNTIME") != "" {
		return &OrcaBackend{}
	}
	return &TmuxBackend{}
}

// Resolve returns the configured backend for homeDir.
// Resolution order:
//  1. homeDir/config/backend file (first whitespace-delimited word)
//  2. Runtime env markers
//  3. tmux (fallback)
func Resolve(homeDir string) Backend {
	if name := readConfigBackend(homeDir); name != "" {
		tag := hometag.Tag(homeDir)
		switch name {
		case "tmux":
			return &TmuxBackend{Tag: tag}
		case "herdr":
			return &HerdrBackend{}
		default:
			return Select(name)
		}
	}
	return Default()
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
