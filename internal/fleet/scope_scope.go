// Package scope classifies repository checkout identity and refuses no-mistakes
// gate agents at fleet lifecycle entry points.
package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Identity int

const (
	// Unknown is the zero value on purpose: an Identity nobody assigned, or
	// one produced alongside a classification error, must never read as a
	// real verdict. With Primary at zero, a failed classification reported
	// "primary" for a path it could not classify at all, and any future
	// `if identity == Primary { block }` written without an Err check would
	// have refused on it.
	Unknown Identity = iota
	Primary
	Worktree
	Unrelated
)

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

type GateCapability int

const (
	GateUnknown GateCapability = iota
	GatePresent
	GateAbsent
)

func (g GateCapability) String() string {
	switch g {
	case GatePresent:
		return "gate-present"
	case GateAbsent:
		return "gate-absent"
	default:
		return "gate-unknown"
	}
}

type Result struct {
	Identity      Identity       `json:"identity"`
	GateCap       GateCapability `json:"gate_capability"`
	CommonDir     string         `json:"common_dir,omitempty"`
	GitDir        string         `json:"git_dir,omitempty"`
	CanonicalPath string         `json:"canonical_path,omitempty"`
	GateSource    string         `json:"gate_source,omitempty"`
	Err           error          `json:"error,omitempty"`
}

func (r *Result) GateRefusalError() error {
	if r == nil {
		return fmt.Errorf("scope classification unavailable")
	}
	if r.Err != nil {
		return r.Err
	}
	if r.GateCap == GatePresent {
		return fmt.Errorf("gate agent refused for %s (%s)", r.CanonicalPath, r.GateSource)
	}
	return nil
}

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

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func gitPath(path, flag string) (string, bool, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", flag)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && strings.Contains(strings.ToLower(string(exitErr.Stderr)), "not a git repository") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("git rev-parse %s for %s: %w", flag, path, err)
	}
	raw := strings.TrimSpace(string(out))
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(path, raw)
	}
	resolved, err := canonicalPath(raw)
	if err != nil {
		return "", false, fmt.Errorf("resolving %s for %s: %w", flag, path, err)
	}
	return resolved, true, nil
}

func ClassifyIdentity(path string) (Identity, string, string, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return Unknown, "", "", fmt.Errorf("resolving repository path %s: %w", path, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return Unknown, "", "", fmt.Errorf("inspecting repository path %s: %w", canonical, err)
	}
	if !info.IsDir() {
		return Unknown, "", "", fmt.Errorf("repository path %s is not a directory", canonical)
	}

	gitDir, isRepo, err := gitPath(canonical, "--git-dir")
	if err != nil {
		return Unknown, "", "", err
	}
	if !isRepo {
		return Unrelated, "", "", nil
	}
	commonDir, isRepo, err := gitPath(canonical, "--git-common-dir")
	if err != nil {
		return Unknown, "", "", err
	}
	if !isRepo {
		return Unknown, "", "", fmt.Errorf("git common-dir unavailable for repository %s", canonical)
	}
	if gitDir == commonDir {
		return Primary, gitDir, commonDir, nil
	}
	return Worktree, gitDir, commonDir, nil
}

func gateCommonDir(commonDir string) bool {
	if commonDir == "" {
		return false
	}
	markers := filepath.Join(NMHome(), "repos")
	resolvedMarkers, err := canonicalPath(markers)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedMarkers, commonDir)
	if err != nil || rel == "." || strings.Contains(rel, string(filepath.Separator)) {
		return false
	}
	return strings.HasSuffix(rel, ".git")
}

func Classify(path string) *Result {
	res := &Result{}
	canonical, err := canonicalPath(path)
	if err == nil {
		res.CanonicalPath = canonical
	} else {
		res.CanonicalPath = path
	}

	if _, present := os.LookupEnv("NO_MISTAKES_GATE"); present {
		res.GateCap = GatePresent
		res.GateSource = "env"
		identity, gitDir, commonDir, identityErr := ClassifyIdentity(path)
		res.Identity, res.GitDir, res.CommonDir = identity, gitDir, commonDir
		if identityErr != nil {
			res.Err = fmt.Errorf("classifying identity: %w", identityErr)
		}
		return res
	}

	identity, gitDir, commonDir, err := ClassifyIdentity(path)
	if err != nil {
		res.GateCap = GateUnknown
		res.Identity = Unknown
		res.Err = fmt.Errorf("classifying identity: %w", err)
		return res
	}
	res.Identity, res.GitDir, res.CommonDir = identity, gitDir, commonDir
	if gateCommonDir(commonDir) {
		res.GateCap = GatePresent
		res.GateSource = "git-common-dir"
	} else {
		res.GateCap = GateAbsent
	}
	return res
}

func GateRefusalError(path string) error {
	return Classify(path).GateRefusalError()
}

// GateRefuseFromCWD checks the current working directory for gate agent signals.
// Returns nil (no error) when this is a normal session — neither the
// NO_MISTAKES_GATE env marker nor a gate checkout path is detected.
// When either signal is present, returns a non-nil error formatted for
// direct display to the user, mirroring firstmate's fm-refuse-if-gate-agent.
func GateRefuseFromCWD() error {
	// 1. Env marker check: NO_MISTAKES_GATE present (even empty) → immediate refuse.
	if _, present := os.LookupEnv("NO_MISTAKES_GATE"); present {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("gate agent refused: NO_MISTAKES_GATE set (env)")
		}
		return GateRefusalError(cwd)
	}

	// 2. Path check: try to detect gate checkout from cwd.
	// When git is unavailable or the classification fails, pass through
	// (not a gate agent) rather than refusing — matching firstmate's
	// fail-safe behavior where an absent git means no gate checkout.
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	res := Classify(cwd)
	if res.Err != nil {
		// git unavailable → not a gate agent
		return nil
	}
	if res.GateCap == GatePresent {
		return fmt.Errorf("gate agent refused for %s (%s)", res.CanonicalPath, res.GateSource)
	}
	return nil
}
