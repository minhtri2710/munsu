package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

// generalPaneConfig is the config file path that optionally specifies a hardcoded
// general pane handle for inject target resolution, relative to homeDir.
const generalPaneConfig = "config/general-pane"

// TargetSource identifies where a general pane target was resolved from.
type TargetSource int

const (
	// ConfigSource means the target came from config/general-pane file.
	ConfigSource TargetSource = iota
	// RuntimeSource means the target was detected from runtime metadata.
	RuntimeSource
	// Unsupported means no target could be resolved.
	Unsupported
	// CaptainSource means a captain home's target: its captain's own pane,
	// from the captain task meta in the parent General home.
	CaptainSource
)

func (ts TargetSource) String() string {
	switch ts {
	case ConfigSource:
		return "config/general-pane"
	case RuntimeSource:
		return "runtime-metadata"
	case Unsupported:
		return "unsupported"
	case CaptainSource:
		return "captain-task-meta"
	default:
		return "unknown"
	}
}

// TargetResult holds the resolved general pane handle with source provenance.
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
		if result.Session != expected {
			return fmt.Errorf("target session %q does not match active herdr session %q", result.Session, expected)
		}
	}
	return nil
}

// ResolveTargetWithSource resolves the general pane handle with source
// diagnostics. A captain home resolves its own captain's pane and never the
// General's config/general-pane or runtime pane.
func ResolveTargetWithSource(homeDir string) (TargetResult, error) {
	if captain, err := home.IsCaptainHome(homeDir); err != nil {
		return TargetResult{}, fmt.Errorf("reading home identity: %w", err)
	} else if captain {
		return captainPaneTarget(homeDir), nil
	}
	path := filepath.Join(homeDir, generalPaneConfig)
	data, err := os.ReadFile(path)
	if err == nil {
		paneHandle := strings.TrimSpace(string(data))
		if paneHandle == "" {
			return TargetResult{
				Source:       Unsupported,
				SourceDetail: "general-pane config is empty",
			}, nil
		}
		session := backend.HandleSession(paneHandle)
		return TargetResult{
			Source:       ConfigSource,
			Handle:       paneHandle,
			Session:      session,
			SourceDetail: "explicit config/general-pane",
		}, nil
	}
	if !os.IsNotExist(err) {
		return TargetResult{}, fmt.Errorf("reading %s: %w", generalPaneConfig, err)
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

// captainPane reads the captain task meta that this captain home's launch
// wrote in its parent General home, bound to this home by marker id and
// canonical path.
func captainPane(homeDir string) (map[string]string, error) {
	id, err := home.ValidateCaptainProvenance(homeDir)
	if err != nil {
		return nil, err
	}
	parent, err := config.Get(homeDir, "parent-home")
	if err != nil {
		return nil, fmt.Errorf("captain home has no parent-home: %w", err)
	}
	meta, err := home.ReadMeta(strings.TrimSpace(parent), "captain:"+id)
	if err != nil {
		return nil, fmt.Errorf("reading captain task meta: %w", err)
	}
	canonical, err := home.CanonicalCaptainHome(homeDir)
	if err != nil {
		return nil, err
	}
	if meta["kind"] != "captain" || meta["sm_id"] != id || meta["home"] != canonical || meta["window"] == "" {
		return nil, fmt.Errorf("captain task meta for %s does not name a pane launched for %s", id, canonical)
	}
	return meta, nil
}

func captainPaneTarget(homeDir string) TargetResult {
	meta, err := captainPane(homeDir)
	if err != nil {
		return TargetResult{Source: Unsupported, SourceDetail: err.Error()}
	}
	if meta["backend"] != "herdr" {
		return TargetResult{Source: Unsupported, SourceDetail: fmt.Sprintf("captain pane backend %q takes no wake push", meta["backend"])}
	}
	session := meta[backend.MetaSessionOwner]
	if session == "" {
		session = backend.HandleSession(meta["window"])
	}
	return TargetResult{Source: CaptainSource, Handle: meta["window"], Session: session, SourceDetail: "captain task meta"}
}
