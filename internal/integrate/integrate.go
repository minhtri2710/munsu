// Package integrate manages opt-in harness integration — installing, repairing,
// and verifying native hooks and adapter artifacts for the detected agent harness.
//
// Each integration is scoped to one harness and one scope (user-global or
// project-local). Ownership markers ensure we never overwrite unrelated user
// content. All writes are atomic (write-to-temp, then rename). Backups are
// taken before owned content is replaced.
package integrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/harness"
)

// Scope identifies where integration artifacts are installed.
type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// IntegrationResult is returned by Install, Repair, and Status operations.
type IntegrationResult struct {
	Harness     string `json:"harness"`
	Scope       Scope  `json:"scope"`
	State       string `json:"state"` // "installed", "drifted", "repairable", "unsupported", "fresh", "absent"
	Version     string `json:"version,omitempty"`
	Message     string `json:"message,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
	Drifted     bool   `json:"drifted,omitempty"`
}

// IntegrationBundle contains all artifacts for one harness integration.
type IntegrationBundle struct {
	// Pi extension files (template content keyed by relative path)
	PiExtensions map[string]string
	// Manifest records metadata about the installation
	Manifest Manifest
}

// Manifest is the durable integration metadata stored under the munsu home.
type Manifest struct {
	SchemaVersion string `json:"schema_version"`
	Harness       string `json:"harness"`
	Version       string `json:"version"`
	Scope         string `json:"scope"`
	InstalledAt   string `json:"installed_at"`
	TargetPaths   []string `json:"target_paths"`
	BackupPaths   []string `json:"backup_paths,omitempty"`
	Capabilities  []string `json:"capabilities"`
}

// Capability describes a supported integration capability.
type Capability string

const (
	CapSessionStart Capability = "session-start"
	CapWakeFollowUp Capability = "wake-followup"
	CapTurnEndGuard Capability = "turnend-guard"
	CapPreToolCheck Capability = "pretool-check"
	CapScopeGate    Capability = "scope-gate"
)

// EnabledCapabilities returns the bundle of capabilities for a given harness.
// Falls back from harness-specific to common defaults.
func EnabledCapabilities(name string) []Capability {
	switch name {
	case harness.Pi:
		return []Capability{
			CapSessionStart,
			CapWakeFollowUp,
			CapTurnEndGuard,
			CapPreToolCheck,
			CapScopeGate,
		}
	default:
		return nil
	}
}

// MunsuHomeArtifactsDir returns the path under the munsu home where
// integration manifests and metadata are stored.
func MunsuHomeArtifactsDir(homeDir string) string {
	return filepath.Join(homeDir, "integrate")
}

// UserExtensionsDir returns the Pi user extensions directory.
func UserExtensionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent", "extensions")
}

// ProjectExtensionsDir returns the Pi project extensions directory for cwd.
func ProjectExtensionsDir(cwd string) string {
	return filepath.Join(cwd, ".pi", "extensions")
}

// MarkerDelimiter is written as the first and last line of owned files
// so we can distinguish munsu-managed content from user content.
const MarkerDelimiter = "// munsu-integrate -- do not edit this section"

// AssertSupportedHarness checks that name is a known harness with supported
// integration capabilities. Returns a user-facing error for unknown harnesses.
func AssertSupportedHarness(name string) error {
	if name == "" {
		return fmt.Errorf("no harness specified and automatic detection failed")
	}
	if !harness.IsKnownHarness(name) {
		return fmt.Errorf("unknown harness %q: must be one of %v", name, harness.KnownHarnesses)
	}
	caps := EnabledCapabilities(name)
	if len(caps) == 0 {
		return fmt.Errorf("harness %q is recognised but has no integration capabilities yet", name)
	}
	return nil
}

// homePathForScope resolves the filesystem location for munsu home artifacts
// based on scope. For user scope this goes in the munsu home directory.
func homePathForScope(homeDir string, _ Scope) string {
	return MunsuHomeArtifactsDir(homeDir)
}

// writeAtomic writes content to a temp file then renames it atomically.
func writeAtomic(path, content string, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create parent dirs for %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".integrate-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	tmp.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}

// FileContainsOwnershipMarker reports whether the file at path contains
// the munsu ownership marker.
func FileContainsOwnershipMarker(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	return containsOwnershipMarker(content)
}

// containsOwnershipMarker checks if the content has our ownership delimiter.
func containsOwnershipMarker(content string) bool {
	const marker = "munsu-integrate"
	return strings.Contains(content, marker)
}
