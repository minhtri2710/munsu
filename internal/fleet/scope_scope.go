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

// Result is a path's checkout identity.
// Words: Human, typed, 2026-10-15: "xóa no-mistake đi, nó chỉ là 1 plugin tích hợp rời nếu user cần thui"
type Result struct {
	Identity      Identity `json:"identity"`
	CommonDir     string   `json:"common_dir,omitempty"`
	GitDir        string   `json:"git_dir,omitempty"`
	CanonicalPath string   `json:"canonical_path,omitempty"`
	Err           error    `json:"error,omitempty"`
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

// ClassifyIdentity is the single owner of checkout identity. Every consumer
// (worktree binding, spawn authorization, git target binding) classifies a path
// here and states its own admission rule over the result; none re-derives
// identity from paths.
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

func Classify(path string) *Result {
	res := &Result{}
	canonical, err := canonicalPath(path)
	if err == nil {
		res.CanonicalPath = canonical
	} else {
		res.CanonicalPath = path
	}

	identity, gitDir, commonDir, err := ClassifyIdentity(path)
	if err != nil {
		res.Identity = Unknown
		res.Err = fmt.Errorf("classifying identity: %w", err)
		return res
	}
	res.Identity, res.GitDir, res.CommonDir = identity, gitDir, commonDir
	return res
}
