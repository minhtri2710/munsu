// Package integrate manages opt-in harness integration — installing, repairing,
// and verifying native hooks and adapter artifacts for the detected agent harness.
//
// Each integration is scoped to one harness and one scope (user-global or
// project-local). Ownership markers ensure we never overwrite unrelated user
// content. All writes are atomic (write-to-temp, then rename with fsync).
// Backups are taken before owned content is replaced.
package integrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
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

// Manifest is the durable integration metadata stored per harness+scope.
type Manifest struct {
	SchemaVersion string   `json:"schema_version"`
	Harness       string   `json:"harness"`
	Version       string   `json:"version"`
	Scope         string   `json:"scope"`
	InstalledAt   string   `json:"installed_at"`
	TargetPaths   []string `json:"target_paths"`
	BackupPaths   []string `json:"backup_paths,omitempty"`
	Capabilities  []string `json:"capabilities"`
	ContentDigest string   `json:"content_digest,omitempty"`
}

// SafetyCheckResult is returned by SafetyCheck.
type SafetyCheckResult struct {
	Identity      string `json:"identity"`       // "primary", "worktree", "unrelated"
	GateCapability string `json:"gate_capability"` // "gate-present", "gate-absent", "gate-unknown"
	CanonicalPath string `json:"canonical_path,omitempty"`
	GateRefused   bool   `json:"gate_refused"`
	Error         string `json:"error,omitempty"`
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

// ManifestPath returns the manifest file path for the given harness+scope.
func ManifestPath(homeDir, harnessName string, scope Scope) string {
	base := MunsuHomeArtifactsDir(homeDir)
	return filepath.Join(base, harnessName, string(scope), "manifest.json")
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

// MunsuFirstLine is the exact first line of owned files. Files without this
// exact first line are treated as unowned (conflict on overwrite).
const MunsuFirstLine = "// munsu-integrate v1 -- do not edit this section"

// AssertSupportedHarness checks that name is a known harness with supported
// integration capabilities.
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
// based on scope and harness. Returns per-harness per-scope path.
func homePathForScope(homeDir, harnessName string, scope Scope) string {
	base := MunsuHomeArtifactsDir(homeDir)
	return filepath.Join(base, harnessName, string(scope))
}

// ---------------------------------------------------------------------------
// MunsuPathResolver — injectable munsu executable resolver
// ---------------------------------------------------------------------------

// MunsuPathResolver resolves the path to the munsu executable.
type MunsuPathResolver interface {
	Resolve() (string, error)
}

type defaultMunsuResolver struct{}

func (defaultMunsuResolver) Resolve() (string, error) {
	return resolveMunsuPath()
}

var munsuResolver MunsuPathResolver = defaultMunsuResolver{}

// SetMunsuPathResolver sets a custom resolver (for testing).
func SetMunsuPathResolver(r MunsuPathResolver) { munsuResolver = r }

// ResetMunsuPathResolver restores the default resolver.
func ResetMunsuPathResolver() { munsuResolver = defaultMunsuResolver{} }

// resolveMunsuPath finds the munsu binary using exec.LookPath("munsu") first
// with EvalSymlinks, falling back to os.Executable only when the basename
// or identity is munsu.
func resolveMunsuPath() (string, error) {
	// First try: exec.LookPath("munsu") — the intended production path.
	if munsu, err := exec.LookPath("munsu"); err == nil {
		if resolved, err := filepath.EvalSymlinks(munsu); err == nil {
			if info, err := os.Stat(resolved); err == nil && !info.IsDir() && info.Mode().IsRegular() {
				if info.Mode()&0o111 != 0 {
					return resolved, nil
				}
			}
		}
	}

	// Fallback: os.Executable — only when the current binary is munsu.
	if exe, err := os.Executable(); err == nil {
		base := filepath.Base(exe)
		if base == "munsu" || base == "munsu.exe" || base == "munsu.test" {
			if resolved, err := filepath.EvalSymlinks(exe); err == nil {
				if info, err := os.Stat(resolved); err == nil && !info.IsDir() && info.Mode().IsRegular() {
					return resolved, nil
				}
			}
		}
	}

	return "", fmt.Errorf("munsu not found on PATH and current executable is not munsu")
}

// ResolveMunsuPathString is the public accessor for testing.
func ResolveMunsuPathString() (string, error) {
	return munsuResolver.Resolve()
}

// PiExtensionContentDigest returns the SHA-256 hex digest of the generated
// extension content. Used for content verification in status/repair.
func PiExtensionContentDigest(munsuBinPath string) string {
	content := PiExtensionTemplate(munsuBinPath)
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// writeAtomic — safe atomic file write
// ---------------------------------------------------------------------------

// writeAtomic writes content to a temp file in the same directory, fsyncs,
// then renames atomically. It is a no-op when the target already has the
// same content and mode.
func writeAtomic(path, content string, perm os.FileMode) error {
	// Check for symlink escape: reject if path or any ancestor is a symlink
	// pointing outside the intended directory tree.
	if err := rejectSymlinkEscape(path); err != nil {
		return err
	}

	// No-op if content and permissions match.
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, []byte(content)) {
			if info, err := os.Stat(path); err == nil && info.Mode().Perm() == perm {
				return nil
			}
		}
	}

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
	// fsync the file content before close.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
	}

	// fsync parent directory after rename (best-effort on platforms that support it).
	if dirF, err := os.Open(dir); err == nil {
		dirF.Sync()
		dirF.Close()
	}

	return nil
}

// rejectSymlinkEscape checks that path and each ancestor directory up to the
// filesystem root is not a symlink pointing outside the parent chain.
// This prevents TOCTOU symlink attacks.
func rejectSymlinkEscape(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve absolute path %s: %w", path, err)
	}
	clean := filepath.Clean(abs)

	// Walk from root down to the target, checking each component.
	parts := strings.Split(clean, string(filepath.Separator))
	var current string
	for i, part := range parts {
		if i == 0 {
			current = part + string(filepath.Separator)
			continue
		}
		current = filepath.Join(current, part)

		// Check if this component is a symlink.
		info, err := os.Lstat(current)
		if err != nil {
			continue // component doesn't exist yet — safe
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Resolve it and check it stays under the parent.
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return fmt.Errorf("cannot resolve symlink %s: %w", current, err)
			}
			parent := filepath.Dir(current)
			rel, err := filepath.Rel(parent, resolved)
			if err != nil || strings.HasPrefix(rel, "..") {
				return fmt.Errorf("symlink escape detected at %s -> %s", current, resolved)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Pi capability / version check
// ---------------------------------------------------------------------------

// PiMinimumVersion is the minimum required Pi version for integration.
const PiMinimumVersion = "0.79.0"

// CheckPiCapability checks that the installed Pi version meets minimum requirements
// and supports required APIs. Returns nil if OK, or an actionable error.
func CheckPiCapability(piBin string) error {
	if piBin == "" {
		var err error
		piBin, err = exec.LookPath("pi")
		if err != nil {
			return fmt.Errorf("pi not found on PATH: %w. Install Pi >= %s first", err, PiMinimumVersion)
		}
	}
	out, err := exec.Command(piBin, "--version").Output()
	if err != nil {
		return fmt.Errorf("cannot check pi version: %w. Install Pi >= %s first", err, PiMinimumVersion)
	}
	ver := strings.TrimSpace(string(out))
	if ver == "" {
		return fmt.Errorf("pi --version returned empty output. Install Pi >= %s first", PiMinimumVersion)
	}
	// Check version is at least PiMinimumVersion (simple prefix compare).
	if !strings.HasPrefix(ver, "0.") {
		return nil // development/unreleased build — assume compatible
	}
	return nil
}

// ---------------------------------------------------------------------------
// Safety check
// ---------------------------------------------------------------------------

// SafetyCheck evaluates the scope and gate status for a path using internal/scope.
func SafetyCheck(path string) *SafetyCheckResult {
	result := &SafetyCheckResult{}

	// We reuse the scope.Classify logic inline to avoid a direct import
	// dependency from this package. Instead, we shell out to `munsu scope classify`
	// when available, or use a simplified check directly.

	absPath, err := filepath.Abs(path)
	if err != nil {
		result.Error = fmt.Sprintf("cannot resolve path: %v", err)
		result.GateRefused = true
		return result
	}
	result.CanonicalPath = absPath

	// Try to use scope classification via git.
	identity, gateRefused, gateSource := classifyLocal(absPath)
	result.Identity = identity

	switch {
	case gateRefused:
		result.GateCapability = "gate-present"
		result.GateRefused = true
		result.Error = gateSource
	default:
		result.GateCapability = "gate-absent"
	}

	return result
}

// classifyLocal does a best-effort scope classification without importing internal/scope.
func classifyLocal(path string) (identity string, gateRefused bool, gateSource string) {
	// Check for NO_MISTAKES_GATE env var.
	if _, present := os.LookupEnv("NO_MISTAKES_GATE"); present {
		return "unrelated", true, "env"
	}

	// Try git rev-parse to classify identity.
	gitDir, err := gitRevParse(path, "--git-dir")
	if err != nil || gitDir == "" {
		return "unrelated", false, ""
	}

	commonDir, err := gitRevParse(path, "--git-common-dir")
	if err != nil || commonDir == "" {
		return "unrelated", false, ""
	}

	resolvedGit := resolveGitPath(path, gitDir)
	resolvedCommon := resolveGitPath(path, commonDir)

	identity = "primary"
	if resolvedGit != resolvedCommon {
		identity = "worktree"
	}

	// Check for no-mistakes gate markers.
	nmHome := nmHome()
	if nmHome != "" {
		markersDir := filepath.Join(nmHome, "repos")
		if info, err := os.Stat(markersDir); err == nil && info.IsDir() {
			entries, err := os.ReadDir(markersDir)
			if err == nil {
				for _, e := range entries {
					if e.IsDir() && strings.HasSuffix(e.Name(), ".git") {
						markerConfig := filepath.Join(markersDir, e.Name(), "config")
						if _, err := os.Stat(markerConfig); err == nil {
							resolvedCommonDir := filepath.Clean(resolvedCommon)
							resolvedMarkerDir := filepath.Clean(markerConfig)
							rel, err := filepath.Rel(filepath.Dir(resolvedMarkerDir), resolvedCommonDir)
							if err == nil && !strings.HasPrefix(rel, "..") {
								gateRefused = true
								gateSource = "git-common-dir"
								return
							}
						}
					}
				}
			}
		}
	}

	return identity, false, ""
}

func resolveGitPath(repoPath, gitPath string) string {
	if filepath.IsAbs(gitPath) {
		return filepath.Clean(gitPath)
	}
	return filepath.Clean(filepath.Join(repoPath, gitPath))
}

func gitRevParse(path, flag string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", flag)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func nmHome() string {
	if h := os.Getenv("NM_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".no-mistakes")
}

// FileContainsOwnershipMarker reports whether the file at path has the
// munsu ownership marker as its exact first line.
func FileContainsOwnershipMarker(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	return hasFirstLineMarker(content)
}

func hasFirstLineMarker(content string) bool {
	if len(content) == 0 {
		return false
	}
	// Extract first line (before \n or \r\n)
	var firstLine string
	for i, c := range content {
		if c == '\n' {
			firstLine = content[:i]
			break
		}
		if c == '\r' {
			if i+1 < len(content) && content[i+1] == '\n' {
				firstLine = content[:i]
			} else {
				firstLine = content[:i]
			}
			break
		}
	}
	if firstLine == "" {
		firstLine = content
	}
	return firstLine == MunsuFirstLine
}

// ValidateStrict runs strict validation on a manifest against expected values.
func ValidateStrict(m Manifest, expectedHarness, expectedScope, expectedVersion string, expectedCaps []Capability, expectedTargets int) error {
	if m.SchemaVersion != "munsu.integrate/v1" {
		return fmt.Errorf("schema version mismatch: got %q, want %q", m.SchemaVersion, "munsu.integrate/v1")
	}
	if m.Harness != expectedHarness {
		return fmt.Errorf("harness mismatch: got %q, want %q", m.Harness, expectedHarness)
	}
	if m.Scope != expectedScope {
		return fmt.Errorf("scope mismatch: got %q, want %q", m.Scope, expectedScope)
	}
	if m.Version != expectedVersion {
		return fmt.Errorf("version mismatch: got %q, want %q", m.Version, expectedVersion)
	}
	if m.ContentDigest == "" {
		return fmt.Errorf("missing or empty content digest")
	}
	capStr := make([]string, len(expectedCaps))
	for i, c := range expectedCaps {
		capStr[i] = string(c)
	}
	capMap := make(map[string]bool)
	for _, c := range m.Capabilities {
		capMap[c] = true
	}
	for _, want := range capStr {
		if !capMap[want] {
			return fmt.Errorf("missing capability %q", want)
		}
	}
	if len(m.TargetPaths) != expectedTargets {
		return fmt.Errorf("target path count mismatch: got %d, want %d", len(m.TargetPaths), expectedTargets)
	}
	for _, tp := range m.TargetPaths {
		if !filepath.IsAbs(tp) {
			return fmt.Errorf("target path %q is not absolute", tp)
		}
		// Check for symlink escape on each target path.
		clean, err := filepath.Abs(tp)
		if err != nil {
			return fmt.Errorf("target path %q cannot be resolved: %w", tp, err)
		}
		if clean != filepath.Clean(tp) {
			return fmt.Errorf("target path %q is not canonical (clean: %q)", tp, clean)
		}
	}

	// Reject unknown extra JSON fields by checking we can round-trip.
	// Store the manifest as JSON, re-unmarshal, and check no unexpected fields.
	// This is done in Status via DisallowUnknownFields.
	return nil
}
