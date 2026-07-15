// Package worktree wraps the treehouse CLI for pooled git worktree management.
//
// Lease hygiene: every worktree acquired via Get (especially with --lease)
// MUST be returned via Return when the owning crewmate finishes. Orphaned
// leases block the pool and must be reclaimed manually.
// Use "munsu worktree reclaim" to detect and return orphaned leases.
//
// All operations shell out to the treehouse binary. The treehouse CLI must be
// installed and available on PATH.
package worktree
import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// treehouseBin returns the path to the treehouse binary, or an error if not found.
func treehouseBin() (string, error) {
	path, err := exec.LookPath("treehouse")
	if err != nil {
		return "", fmt.Errorf("treehouse: not found on PATH — install treehouse or verify your PATH")
	}
	return path, nil
}

// Get acquires a pooled worktree for the given repo path. If lease is true,
// the --lease flag is passed to treehouse for a durable hold.
// Returns the worktree path, or an error if the path is empty.
func Get(repoPath string, lease bool) (string, error) {
	bin, err := treehouseBin()
	if err != nil {
		return "", err
	}
	args := []string{"get", repoPath}
	if lease {
		args = append(args, "--lease")
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("treehouse get: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("treehouse get: %w", err)
	}
	wtPath := strings.TrimSpace(string(out))
	if wtPath == "" {
		return "", fmt.Errorf("treehouse get returned empty path: use --lease for a durable worktree (non-lease is interactive-only)")
	}
	return wtPath, nil
}

// Return returns a worktree path to the pool via treehouse.
func Return(path string) error {
	bin, err := treehouseBin()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "return", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("treehouse return: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Status returns the treehouse pool status output.
func Status() (string, error) {
	bin, err := treehouseBin()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(bin, "status")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("treehouse status: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("treehouse status: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// IsIsolated verifies that path is a git worktree and not the primary checkout.
// It resolves the canonical path and compares git-dir vs git-common-dir.
// Returns true if the path is an isolated worktree.
func IsIsolated(path string) (bool, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("resolving path: %w", err)
	}

	// Check that it's a git repository
	gitDir, err := gitRevParse(absPath, "--git-dir")
	if err != nil {
		return false, fmt.Errorf("path is not a git repository: %w", err)
	}

	commonDir, err := gitRevParse(absPath, "--git-common-dir")
	if err != nil {
		return false, fmt.Errorf("path is not a git repository: %w", err)
	}

	// Resolve both to absolute paths
	gitDirAbs, err := filepath.Abs(filepath.Join(absPath, gitDir))
	if err != nil {
		return false, err
	}
	commonDirAbs, err := filepath.Abs(filepath.Join(absPath, commonDir))
	if err != nil {
		return false, err
	}

	// In a worktree, --git-dir is under .git/worktrees/<name>,
	// while --git-common-dir is the main .git directory.
	// They are different paths for a worktree, same for the primary checkout.
	if gitDirAbs == commonDirAbs {
		return false, nil // primary checkout
	}
	return true, nil // isolated worktree
}

// gitRevParse runs git rev-parse --<flag> in the given directory.
func gitRevParse(dir, flag string) (string, error) {
	cmd := exec.Command("git", "rev-parse", flag)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", flag, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ErrTreehouseNotFound is returned when treehouse is not on PATH.
var ErrTreehouseNotFound = fmt.Errorf("treehouse: not found on PATH")

// IsTreehouseNotFound reports whether the error indicates treehouse was
// not found on PATH.
func IsTreehouseNotFound(err error) bool {
	if err == nil {
		return false
	}
	// Check if exec.LookPath wrapped it
	if e, ok := err.(*exec.Error); ok && e.Err == exec.ErrNotFound {
		return true
	}
	return strings.Contains(err.Error(), "not found on PATH")
}

// EnsureNotPrimary is a convenience helper that calls IsIsolated and returns
// a descriptive error if the path is the primary checkout.
func EnsureNotPrimary(path string) error {
	isolated, err := IsIsolated(path)
	if err != nil {
		return fmt.Errorf("isolation check failed: %w", err)
	}
	if !isolated {
		absPath, _ := filepath.Abs(path)
		return fmt.Errorf("path %s is the primary checkout, not an isolated worktree", absPath)
	}
	return nil
}

// AbsRoot resolves the true absolute path of the current working directory.
// Useful for comparison against the primary checkout root.
func AbsRoot() string {
	pwd, _ := os.Getwd()
	abs, err := filepath.Abs(pwd)
	if err != nil {
		return pwd
	}
	return abs
}
