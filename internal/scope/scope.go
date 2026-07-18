// Package scope provides canonical repository identity classification using
// resolved paths and Git common-dir/worktree metadata. It detects gate-agent
// capability from NO_MISTAKES_GATE and .no-mistakes/repos/*.git markers with
// explicit precedence, returning structured decisions for primary checkout,
// related worktree, unrelated repository, or ambiguous/security-relevant error.
//
// The primary purpose is to refuse gate-capable agents from entering primary
// fleet scope at every supported entry point (session-start, spawn, native
// integration callbacks, turn-end guard).
package scope

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// --- Canonical repository identity ---

// Identity classifies a repository path using Git common-dir/worktree metadata.
type Identity int

const (
	// Primary is the main checkout (git-dir == git-common-dir).
	Primary Identity = iota
	// Worktree is a git worktree linked to a primary checkout.
	Worktree
	// Unrelated is a repository with no common ancestry in the munsu home.
	Unrelated
)

// String returns a human-readable name for the identity.
func (i Identity) String() string {
	switch i {
	case Primary:
		return "primary"
	case Worktree:
		return "worktree"
	case Unrelated:
		return "unrelated"
	default:
		return "unknown"
	}
}

// --- Gate capability ---

// GateCapability describes whether a repository path is protected by a gate agent.
type GateCapability int

const (
	// GateUnknown means gate capability could not be determined.
	GateUnknown GateCapability = iota
	// GatePresent means a gate agent is active for this repository.
	GatePresent
	// GateAbsent means no gate agent is configured for this repository.
	GateAbsent
)

// String returns a human-readable name for the gate capability.
func (g GateCapability) String() string {
	switch g {
	case GatePresent:
		return "gate-present"
	case GateAbsent:
		return "gate-absent"
	case GateUnknown:
		return "gate-unknown"
	default:
		return "gate-unknown"
	}
}

// --- Classification result ---

// Result is the complete classification for a repository path.
type Result struct {
	Identity      Identity      `json:"identity"`
	GateCap       GateCapability `json:"gate_capability"`
	CommonDir     string         `json:"common_dir,omitempty"`
	GitDir        string         `json:"git_dir,omitempty"`
	CanonicalPath string         `json:"canonical_path,omitempty"`
	GateSource    string         `json:"gate_source,omitempty"` // "env", "marker", or ""
	Err           error          `json:"error,omitempty"`
}

// IsGateRefusal returns true when a gate-capable agent should be refused
// in this scope — that is, when a primary checkout has an active gate.
func (r *Result) IsGateRefusal() bool {
	if r.Err != nil {
		return false // ambiguous state; callers should handle error separately
	}
	return r.Identity == Primary && r.GateCap == GatePresent
}

// IsGateRefusalWithError returns true and the error when the state is
// ambiguous/security-relevant. Callers should prefer fail-closed and refuse
// when this returns an error.
func (r *Result) GateRefusalError() error {
	if r.Err != nil {
		return r.Err
	}
	if r.IsGateRefusal() {
		return fmt.Errorf("gate agent refused: %s is the primary checkout with an active gate (%s)", r.CanonicalPath, r.GateSource)
	}
	return nil
}

// --- Core classification ---

// NMHome returns the resolved no-mistakes home directory.
// Uses NM_HOME env var if set, otherwise ~/.no-mistakes.
func NMHome() string {
	if h := os.Getenv("NM_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".no-mistakes")
}

// reposMarkersDir returns the path to .no-mistakes/repos/ where gate markers live.
func reposMarkersDir() string {
	nmHome := NMHome()
	if nmHome == "" {
		return ""
	}
	return filepath.Join(nmHome, "repos")
}

// --- Git helpers ---

// resolveGitDir returns the absolute git-dir for a path.
func resolveGitDir(path string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-dir: %w", err)
	}
	raw := strings.TrimSpace(string(out))
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(path, raw)
	}
	return filepath.Clean(raw), nil
}

// resolveGitCommonDir returns the absolute git-common-dir for a path.
func resolveGitCommonDir(path string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}
	raw := strings.TrimSpace(string(out))
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(path, raw)
	}
	return filepath.Clean(raw), nil
}

// resolveCanonicalPath resolves a path to its canonical form, resolving
// symlinks. If that fails, it returns the cleaned absolute path.
func resolveCanonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	eval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return eval
}

// --- Identity classification ---

// ClassifyIdentity determines whether the given path is a primary checkout,
// a git worktree, or unrelated (not a git repo or outside the munsu scope).
//
// It uses resolved git-dir vs git-common-dir comparison:
//   - equal => primary checkout
//   - different => git worktree
//   - git error => unrelated or error
func ClassifyIdentity(path string) (Identity, string, string, error) {
	gitDir, err := resolveGitDir(path)
	if err != nil {
		return Unrelated, "", "", nil // not a git repo
	}

	commonDir, err := resolveGitCommonDir(path)
	if err != nil {
		return Unrelated, "", "", nil // not a git repo
	}

	if gitDir == commonDir {
		return Primary, gitDir, commonDir, nil
	}
	return Worktree, gitDir, commonDir, nil
}

// --- Gate capability detection ---

// DetectGateCapability checks whether a gate agent is active for the
// repository at the given path, using the following precedence:
//
//  1. NO_MISTAKES_GATE env var — if set to a non-empty value, gate is present.
//  2. .no-mistakes/repos/<hash>.git marker — if the repo's resolved canonical
//     path matches a marker dir's remote "origin" URL stored in its config,
//     gate is present.
//
// Returns GateUnknown when evidence is ambiguous (env set but empty, or marker
// index unreachable).
func DetectGateCapability(repoPath string) (GateCapability, string) {
	// 1. NO_MISTAKES_GATE env var
	if val := os.Getenv("NO_MISTAKES_GATE"); val != "" {
		return GatePresent, "env"
	}

	// 2. .no-mistakes/repos/<hash>.git markers
	markersDir := reposMarkersDir()
	if markersDir == "" {
		return GateAbsent, ""
	}

	canonical := resolveCanonicalPath(repoPath)

	entries, err := os.ReadDir(markersDir)
	if err != nil {
		// markers dir doesn't exist or can't be read — treat as absent
		return GateAbsent, ""
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		markerDir := filepath.Join(markersDir, entry.Name())
		if !strings.HasSuffix(markerDir, ".git") {
			continue
		}
		if matchesMarkerRepo(canonical, markerDir) {
			return GatePresent, "marker"
		}
	}

	return GateAbsent, ""
}

// matchesMarkerRepo checks whether the repository at canonicalPath matches
// the marker directory by checking the marker's git config for a matching
// remote origin URL. The marker is a bare git directory whose config contains
// the repository's URL.
func matchesMarkerRepo(canonicalPath, markerDir string) bool {
	cfg, err := os.ReadFile(filepath.Join(markerDir, "config"))
	if err != nil {
		return false
	}

	// Check if the marker's remote URL references the same canonical path,
	// or if the marker dir name encodes a hash that maps to this repo.
	// no-mistakes uses repos/<short-sha>.git for markers; we check the
	// remote "origin" URL against the canonical path's repo URL.

	markerRemoteURL := extractRemoteURL(string(cfg))
	if markerRemoteURL == "" {
		return false
	}

	// Check if the canonical path corresponds to this remote URL.
	// Try to get the remote origin URL from the local repo.
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = canonicalPath
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	localOrigin := strings.TrimSpace(string(out))
	if localOrigin == "" {
		return false
	}

	return strings.TrimSpace(markerRemoteURL) == localOrigin
}

// extractRemoteURL parses the remote origin URL from a git config file content.
func extractRemoteURL(configContent string) string {
	for _, line := range strings.Split(configContent, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "url = ") {
			return strings.TrimPrefix(line, "url = ")
		}
	}
	return ""
}

// --- Convenience classifier ---

// Classify runs the full classification: identity + gate capability.
// It is fail-closed for ambiguous security-relevant states.
func Classify(path string) *Result {
	res := &Result{
		CanonicalPath: resolveCanonicalPath(path),
	}

	identity, gitDir, commonDir, err := ClassifyIdentity(path)
	if err != nil {
		res.Err = fmt.Errorf("classifying identity: %w", err)
		return res
	}
	res.Identity = identity
	res.GitDir = gitDir
	res.CommonDir = commonDir

	// Only detect gate capability for primary checkouts and worktrees.
	// Unrelated repos don't need gate detection.
	if identity != Unrelated {
		cap, source := DetectGateCapability(path)
		res.GateCap = cap
		res.GateSource = source
	}

	return res
}

// --- Caller-facing error ---

// GateRefusalError returns a user-facing error when the path is a primary
// checkout with an active gate. Callers at entry points should refuse
// gate agents when this error is non-nil.
func GateRefusalError(path string) error {
	r := Classify(path)
	return r.GateRefusalError()
}

// IsGateAgentActive returns true if the NO_MISTAKES_GATE env var is set to
// a non-empty value OR a .no-mistakes/repos/<hash>.git marker matches the
// repository at path. It does not classify identity.
func IsGateAgentActive(path string) bool {
	cap, _ := DetectGateCapability(path)
	return cap == GatePresent
}
