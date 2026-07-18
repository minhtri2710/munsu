package afk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// captainPaneConfig is the config file path that optionally specifies a hardcoded
// captain pane handle for inject target resolution, relative to homeDir.
const captainPaneConfig = "config/captain-pane"

// TargetSource identifies where a captain pane target was resolved from.
type TargetSource int

const (
	// ConfigSource means the target came from config/captain-pane file.
	ConfigSource TargetSource = iota
	// RuntimeSource means the target was detected from runtime metadata.
	RuntimeSource
	// Unsupported means no target could be resolved.
	Unsupported
)

func (ts TargetSource) String() string {
	switch ts {
	case ConfigSource:
		return "config/captain-pane"
	case RuntimeSource:
		return "runtime-metadata"
	case Unsupported:
		return "unsupported"
	default:
		return "unknown"
	}
}

// TargetResult holds the resolved captain pane handle with source provenance.
type TargetResult struct {
	Source       TargetSource `json:"source"`
	Handle       string       `json:"handle"`
	Session      string       `json:"session"`
	SourceDetail string       `json:"source_detail"`
}

// ValidateTargetOwnership checks that the target belongs to the active runtime session.
func ValidateTargetOwnership(result *TargetResult) error {
	if result == nil {
		return fmt.Errorf("target is nil")
	}
	if result.Handle == "" {
		return fmt.Errorf("target handle is empty")
	}
	if result.Source == Unsupported {
		return fmt.Errorf("target source is unsupported: %s", result.SourceDetail)
	}
	if os.Getenv("TMUX_PANE") != "" {
		return nil
	}
	if os.Getenv("HERDR_ENV") != "" && os.Getenv("HERDR_PANE_ID") != "" {
		expected := os.Getenv("HERDR_SESSION")
		if expected == "" {
			expected = "default"
		}
		if result.Session == "" {
			return fmt.Errorf("target %q has no herdr session; expected %q", result.Handle, expected)
		}
		if result.Session != expected {
			return fmt.Errorf("target session %q does not match active herdr session %q", result.Session, expected)
		}
	}
	return nil
}

// ResolveTarget resolves the captain pane handle from explicit config or runtime metadata.
// Resolution order: config/captain-pane, TMUX_PANE, HERDR_PANE_ID, unsupported.
// Returns (paneHandle, session, error). paneHandle is in "session:pane_id" format
// for herdr, or a bare pane ID for tmux. Returns empty strings with nil error
// when no target is configured.
func ResolveTarget(homeDir string) (string, string, error) {
	result, err := ResolveTargetWithSource(homeDir)
	if err != nil {
		return "", "", err
	}
	return result.Handle, result.Session, nil
}

// ResolveTargetWithSource resolves the captain pane handle with source diagnostics.
func ResolveTargetWithSource(homeDir string) (TargetResult, error) {
	path := filepath.Join(homeDir, captainPaneConfig)
	data, err := os.ReadFile(path)
	if err == nil {
		paneHandle := strings.TrimSpace(string(data))
		if paneHandle == "" {
			return TargetResult{
				Source:       Unsupported,
				SourceDetail: "captain-pane config is empty",
			}, nil
		}
		session, _ := splitTargetHandle(paneHandle)
		return TargetResult{
			Source:       ConfigSource,
			Handle:       paneHandle,
			Session:      session,
			SourceDetail: "explicit config/captain-pane",
		}, nil
	}
	if !os.IsNotExist(err) {
		return TargetResult{}, fmt.Errorf("reading %s: %w", captainPaneConfig, err)
	}

	if pane := strings.TrimSpace(os.Getenv("TMUX_PANE")); pane != "" {
		return TargetResult{
			Source:       RuntimeSource,
			Handle:       pane,
			SourceDetail: "runtime TMUX_PANE",
		}, nil
	}
	if os.Getenv("HERDR_ENV") != "" {
		pane := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
		if pane != "" {
			session := strings.TrimSpace(os.Getenv("HERDR_SESSION"))
			if session == "" {
				session = "default"
			}
			return TargetResult{
				Source:       RuntimeSource,
				Handle:       session + ":" + pane,
				Session:      session,
				SourceDetail: "runtime HERDR_SESSION/HERDR_PANE_ID",
			}, nil
		}
	}

	return TargetResult{
		Source:       Unsupported,
		SourceDetail: "no explicit target or verified runtime pane metadata",
	}, nil
}

// splitTargetHandle splits a pane handle on the first colon.
// Returns (session, paneID). If no colon is found, returns ("", handle).
func splitTargetHandle(handle string) (string, string) {
	idx := strings.Index(handle, ":")
	if idx < 0 {
		return "", handle
	}
	return handle[:idx], handle[idx+1:]
}
