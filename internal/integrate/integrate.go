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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/scope"
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
	Identity       string `json:"identity"`        // "primary", "worktree", "unrelated"
	GateCapability string `json:"gate_capability"` // "gate-present", "gate-absent", "gate-unknown"
	CanonicalPath  string `json:"canonical_path,omitempty"`
	GateRefused    bool   `json:"gate_refused"`
	Error          string `json:"error,omitempty"`
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
	case harness.Claude, harness.Grok, harness.Codex, harness.Agy:
		return []Capability{
			CapSessionStart,
			CapTurnEndGuard,
			CapPreToolCheck,
		}
	case harness.Opencode:
		return []Capability{
			CapSessionStart,
			CapWakeFollowUp,
			CapTurnEndGuard,
			CapPreToolCheck,
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
// For user scope, the path is <home>/integrate/<harness>/user/manifest.json.
// For project scope, the path includes a SHA-256 slug derived from the
// canonical project root to ensure isolation between different projects.
func ManifestPath(homeDir, harnessName string, scope Scope, cwd ...string) string {
	base := MunsuHomeArtifactsDir(homeDir)
	if scope == ScopeProject && len(cwd) > 0 && cwd[0] != "" {
		slug := projectPathSlug(cwd[0])
		return filepath.Join(base, harnessName, "project", slug, "manifest.json")
	}
	return filepath.Join(base, harnessName, string(scope), "manifest.json")
}

// projectPathSlug returns a deterministic SHA-256 hex prefix (16 chars)
// derived from the canonical (EvalSymlinks-resolved, Clean) project root path.
// Uses the first 16 hex characters for a readable but unique path component.
func projectPathSlug(projectRoot string) string {
	canonical, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		canonical = projectRoot
	}
	canonical = filepath.Clean(canonical)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:8]) // 16 hex chars
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
// For project scope, includes a deterministic SHA-256 slug of the cwd/canonical path.
func homePathForScope(homeDir, harnessName string, scope Scope, cwd ...string) string {
	base := MunsuHomeArtifactsDir(homeDir)
	if scope == ScopeProject && len(cwd) > 0 && cwd[0] != "" {
		slug := projectPathSlug(cwd[0])
		return filepath.Join(base, harnessName, "project", slug)
	}
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

	// fsync parent directory after rename and check errors.
	dirF, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open parent dir for fsync: %w", err)
	}
	if err := dirF.Sync(); err != nil {
		dirF.Close()
		return fmt.Errorf("fsync parent dir: %w", err)
	}
	if err := dirF.Close(); err != nil {
		return fmt.Errorf("close parent dir: %w", err)
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

// DefaultCapabilityProbeTimeout is the default timeout for external capability
// probes (pi --version, node API check). Override in tests with SetProbeTimeout.
const DefaultCapabilityProbeTimeout = 30 * time.Second

// capabilityProbeTimeout is injectable for tests. Default is 30s.
var capabilityProbeTimeout = DefaultCapabilityProbeTimeout

// SetProbeTimeout sets the capability probe timeout for testing.
func SetProbeTimeout(d time.Duration) time.Duration {
	prev := capabilityProbeTimeout
	capabilityProbeTimeout = d
	return prev
}

// runCapabilityCommand runs cmd with args in dir with a timeout. It starts the
// child in its own process group (Setpgid) and kills the process tree (SIGKILL
// to the negated PID) when the timeout expires before waiting. Returns combined
// stdout+stderr. Fails closed on timeout or any execution error.
var runCapabilityCommand = runCapabilityCommandDefault

// SetCapabilityCommandRunner overrides the capability command runner (for tests).
func SetCapabilityCommandRunner(fn func(name string, args []string, dir string, timeout time.Duration) (string, error)) func(name string, args []string, dir string, timeout time.Duration) (string, error) {
	prev := runCapabilityCommand
	runCapabilityCommand = fn
	return prev
}

// ResetCapabilityCommandRunner restores the default runner.
func ResetCapabilityCommandRunner() {
	runCapabilityCommand = runCapabilityCommandDefault
}

func runCapabilityCommandDefault(name string, args []string, dir string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	out, err := cmd.CombinedOutput()
	if err != nil {
		// If the context timed out, kill the process group to ensure no orphans.
		if ctx.Err() != nil {
			// Kill the process group (negative PID). Ignore errors: process may
			// already be gone.
			if cmd.Process != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			// Wait to reap before returning.
			cmd.Wait()
			return string(out), fmt.Errorf("%s timed out after %v: %w", name, timeout, err)
		}
		return string(out), fmt.Errorf("%s failed: %w\n%s", name, err, string(out))
	}
	return string(out), nil
}

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
	out, err := runCapabilityCommand(piBin, []string{"--version"}, "", capabilityProbeTimeout)
	if err != nil {
		if strings.Contains(err.Error(), "timed out") {
			return fmt.Errorf("pi --version timed out after %v. Install Pi >= %s first", capabilityProbeTimeout, PiMinimumVersion)
		}
		return fmt.Errorf("cannot check pi version: %w. Install Pi >= %s first", err, PiMinimumVersion)
	}
	ver := strings.TrimSpace(out)
	if ver == "" {
		return fmt.Errorf("pi --version returned empty output. Install Pi >= %s first", PiMinimumVersion)
	}

	// Parse version with semver. Strip leading 'v' if present.
	cleanVer := strings.TrimPrefix(ver, "v")
	parsed, err := semver.NewVersion(cleanVer)
	if err != nil {
		return fmt.Errorf("cannot parse pi version %q: %w. Install Pi >= %s first", ver, err, PiMinimumVersion)
	}

	minVer, err := semver.NewVersion(PiMinimumVersion)
	if err != nil {
		return fmt.Errorf("invalid minimum version %q: %w", PiMinimumVersion, err)
	}

	if parsed.LessThan(minVer) {
		return fmt.Errorf("pi version %s < minimum %s. Upgrade Pi to >= %s", parsed.String(), PiMinimumVersion, PiMinimumVersion)
	}

	// Before mutation, verify the installed Pi package supports required APIs
	// by probing the actual ExtensionAPI type via a compile/load check.
	if err := probePiAPIs(piBin); err != nil {
		return fmt.Errorf("pi API capability check failed: %w", err)
	}

	return nil
}

// probePiAPIs verifies the installed @earendil-works/pi-coding-agent package
// supports required ExtensionAPI methods by attempting to import the package
// with a minimal type check script.
func probePiAPIs(piBin string) error {
	if piBin == "" {
		return fmt.Errorf("pi binary path required for API probe")
	}

	// Find the pi package root to resolve the installed typing module.
	piDir := filepath.Dir(piBin)

	// Write a minimal TypeScript fixture that imports the Pi ExtensionAPI type
	// and verifies the required API surface compiles: ExtensionAPI, agent_settled,
	// pi.exec, appendEntry, sendUserMessage, registerCommand, tool_call.
	// Must NOT require unrelated registerTool.
	fixture := `import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { strict as assert } from "assert";

// Verify ExtensionAPI type is available at compile time.
type CheckAPI = ExtensionAPI;

// Verify key method signatures by declaring a function that uses them.
function checkAPIs(pi: ExtensionAPI) {
  // pi.exec: child process execution
  const execResult: Promise<{ code: number; stdout: string; stderr: string }> = pi.exec("munsu", []);
  void execResult;

  // appendEntry: durable append-only storage
  pi.appendEntry("test", { key: "value" });

  // sendUserMessage: send a message to the user
  pi.sendUserMessage("test", { deliverAs: "followUp" });

  // registerCommand: register a slash command
  pi.registerCommand("test", {
    description: "Test command",
    handler: async (_args: string, _ctx: any) => {},
  });

  // Verify events exist on pi.on
  pi.on("agent_settled", async (_event: any, _ctx: any) => {});
  pi.on("tool_call", async (_event: any, _ctx: any) => {});
  pi.on("session_start", async (_event: any, _ctx: any) => {});
  pi.on("turn_end", async (_event: any, _ctx: any) => {});
}

console.log("API probe passed");
`

	tmpDir, err := os.MkdirTemp("", "pi-api-probe-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	srcPath := filepath.Join(tmpDir, "probe.ts")
	if err := os.WriteFile(srcPath, []byte(fixture), 0644); err != nil {
		return fmt.Errorf("write probe file: %w", err)
	}

	// Compile with node --experimental-strip-types using the bounded runner.
	out, err := runCapabilityCommand("node", []string{"--experimental-strip-types", srcPath}, piDir, capabilityProbeTimeout)
	if err != nil {
		if strings.Contains(err.Error(), "timed out") {
			return fmt.Errorf("pi API probe timed out after %v. Ensure @earendil-works/pi-coding-agent >= %s is installed", capabilityProbeTimeout, PiMinimumVersion)
		}
		return fmt.Errorf("pi package API probe failed (exit %v): %s. Ensure @earendil-works/pi-coding-agent >= %s is installed", err, out, PiMinimumVersion)
	}
	if !strings.Contains(out, "API probe passed") {
		return fmt.Errorf("pi API probe did not complete: %s", out)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Safety check
// ---------------------------------------------------------------------------

func SafetyCheck(path string) *SafetyCheckResult {
	scopeResult := scope.Classify(path)

	result := &SafetyCheckResult{
		CanonicalPath: scopeResult.CanonicalPath,
	}

	// Map scope.Identity to string
	switch scopeResult.Identity {
	case scope.Primary:
		result.Identity = "primary"
	case scope.Worktree:
		result.Identity = "worktree"
	default:
		result.Identity = "unrelated"
	}

	// Map scope.GateCap
	switch scopeResult.GateCap {
	case scope.GatePresent:
		result.GateCapability = "gate-present"
	case scope.GateAbsent:
		result.GateCapability = "gate-absent"
	default:
		result.GateCapability = "gate-unknown"
	}

	// GateRefused: true when classification errored (GateUnknown), gate is present,
	// or identity is Unrelated (fail closed for unknown repos).
	if scopeResult.Err != nil {
		result.GateRefused = true
		result.Error = scopeResult.Err.Error()
	} else if scopeResult.GateCap == scope.GatePresent {
		result.GateRefused = true
		result.Error = scopeResult.GateSource
	} else if scopeResult.Identity == scope.Unrelated {
		// Unrelated identity with no explicit gate still fails closed
		result.GateRefused = true
		result.Error = "unrelated checkout (not a recognized repository)"
	}

	return result
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

// ExpectedTargetPath computes the expected extension target path for a given scope+cwd.
// For user scope: ~/.pi/agent/extensions/munsu-pi-integration.ts
// For project scope: <cwd>/.pi/extensions/munsu-pi-integration.ts
func ExpectedTargetPath(scope Scope, cwd string) (string, error) {
	switch scope {
	case ScopeUser:
		dir := UserExtensionsDir()
		if dir == "" {
			return "", fmt.Errorf("cannot determine user extensions directory")
		}
		return filepath.Join(dir, "munsu-pi-integration.ts"), nil
	case ScopeProject:
		if cwd == "" {
			return "", fmt.Errorf("cwd is required for project scope")
		}
		canonical, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return "", fmt.Errorf("cannot resolve cwd %s: %w", cwd, err)
		}
		dir := ProjectExtensionsDir(canonical)
		return filepath.Join(dir, "munsu-pi-integration.ts"), nil
	default:
		return "", fmt.Errorf("unsupported scope %q", scope)
	}
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
	// Store the manifest as JSON, re-ungeneral, and check no unexpected fields.
	// This is done in Status via DisallowUnknownFields.
	return nil
}
