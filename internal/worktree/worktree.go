// Package worktree manages pooled (treehouse) and fallback (git worktree) worktree acquisition.
//
// When treehouse is on PATH, all operations delegate to the treehouse CLI for pooled
// worktree management. When treehouse is absent, a bare git worktree fallback is used
// with a one-time stderr note.
//
// Lease hygiene: every worktree acquired via Get (especially with --lease)
// MUST be returned via Return when the owning soldier finishes. Orphaned
// leases block the pool and must be reclaimed manually.
// Use "munsu worktree reclaim" to detect and return orphaned leases.
//
// IMPORTANT: Return always passes --force to treehouse to prevent interactive
// prompts (e.g. "Worktree has uncommitted changes. Clean and return? [Y/n]")
// from hanging soldiers with no stdin. If treehouse still emits "Aborted" in
// its output even with --force, Return treats that as an error even on exit 0.
//
// The --force flag is required: without it, treehouse prompts interactively
// and produces "Aborted" (exit 0) when stdin is closed, causing a false
// "worktree returned to pool" success.
//
// The git worktree fallback uses stable hashed paths under <homeDir>/.worktrees.
// homeDir must be non-empty when treehouse is absent; it is passed by callers
// that have already resolved the munsu home (e.g. via home.Resolve).
package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Provider is the interface for acquiring, returning, and querying worktrees.
// treehouseProvider implements it via the treehouse CLI; gitWorktreeProvider
// implements it via bare git worktree commands.
type Provider interface {
	Get(repoPath string, lease bool) (string, error)
	Return(path string) error
	Status() (string, error)
}

var printFallbackNote sync.Once

// selectProvider chooses treehouseProvider when treehouse is on PATH,
// otherwise falls back to gitWorktreeProvider using the given homeDir.
// homeDir must be non-empty when treehouse is absent.
func selectProvider(homeDir string) (Provider, error) {
	if _, err := exec.LookPath("treehouse"); err == nil {
		return &treehouseProvider{}, nil
	}
	if homeDir == "" {
		return nil, fmt.Errorf("worktree: homeDir is required for git worktree fallback (resolve munsu home before calling)")
	}
	printFallbackNote.Do(func() {
		fmt.Fprintf(os.Stderr, "munsu: treehouse not found, using git worktree fallback (not pooled)\n")
	})
	return &gitWorktreeProvider{homeDir: homeDir}, nil
}

// Get acquires a worktree for the given repo path within the given munsu home.
// If lease is true and treehouse is the active provider, the --lease flag is
// passed for a durable hold.
func Get(homeDir, repoPath string, lease bool) (string, error) {
	p, err := selectProvider(homeDir)
	if err != nil {
		return "", err
	}
	return p.Get(repoPath, lease)
}

// Return returns a worktree path within the given munsu home.
// When treehouse is active, --force is always passed to prevent interactive prompts.
func Return(homeDir, path string) error {
	p, err := selectProvider(homeDir)
	if err != nil {
		return err
	}
	return p.Return(path)
}

// Status returns worktree status within the given munsu home.
// With treehouse, this shows pool status. With the git fallback, it lists
// managed worktree directories.
func Status(homeDir string) (string, error) {
	p, err := selectProvider(homeDir)
	if err != nil {
		return "", err
	}
	return p.Status()
}

// --- treehouse provider ---

type treehouseProvider struct{}

func (p *treehouseProvider) Get(repoPath string, lease bool) (string, error) {
	bin, err := treehouseBin()
	if err != nil {
		return "", err
	}
	absRepo, absErr := filepath.Abs(repoPath)
	if absErr != nil {
		return "", fmt.Errorf("resolving repo path: %w", absErr)
	}
	args := []string{"get", absRepo}
	if lease {
		args = append(args, "--lease")
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = absRepo
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

func (p *treehouseProvider) Return(path string) error {
	bin, err := treehouseBin()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "return", "--force", path)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	// Even if exit code is 0, check for "Aborted" which means treehouse
	// prompted interactively and was aborted (e.g. stdin closed).
	// This produces a false "worktree returned to pool" without --force.
	if strings.Contains(output, "Aborted") {
		return fmt.Errorf("treehouse return: %s", output)
	}
	if err != nil {
		return fmt.Errorf("treehouse return: %s", output)
	}
	return nil
}

func (p *treehouseProvider) Status() (string, error) {
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

// treehouseBin returns the path to the treehouse binary, or an error if not found.
func treehouseBin() (string, error) {
	path, err := exec.LookPath("treehouse")
	if err != nil {
		return "", fmt.Errorf("treehouse: not found on PATH — install treehouse or verify your PATH")
	}
	return path, nil
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

// --- git worktree fallback provider ---

type gitWorktreeProvider struct {
	homeDir string
}

// getWorktreeBase returns the directory under which git worktrees are created.
// Always <homeDir>/.worktrees — no env fallback.
func (p *gitWorktreeProvider) getWorktreeBase() string {
	return filepath.Join(p.homeDir, ".worktrees")
}

func (p *gitWorktreeProvider) Get(repoPath string, lease bool) (string, error) {
	hash := stableHash(repoPath)
	base := p.getWorktreeBase()
	wtDir := filepath.Join(base, hash)

	// Ensure base directory exists.
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", fmt.Errorf("creating worktree base: %w", err)
	}

	// If worktree already exists, return it (idempotent).
	if fi, err := os.Stat(wtDir); err == nil && fi.IsDir() {
		return wtDir, nil
	}

	// Create new worktree with --detach.
	cmd := exec.Command("git", "worktree", "add", "--detach", wtDir)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git worktree add: %s", strings.TrimSpace(string(out)))
	}
	return wtDir, nil
}

func (p *gitWorktreeProvider) Return(path string) error {
	// Read the .git file to find the owning repo, since git worktree remove
	// must be run from within a git repository.
	gitFile := filepath.Join(path, ".git")
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return fmt.Errorf("reading worktree .git file: %w", err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return fmt.Errorf("unexpected .git file format: %s", line)
	}
	repoGitDir := strings.TrimPrefix(line, "gitdir: ")
	// Resolve relative paths against the worktree parent.
	if !filepath.IsAbs(repoGitDir) {
		repoGitDir = filepath.Join(filepath.Dir(path), repoGitDir)
	}
	// The repo root is three levels above .git/worktrees/<name>.
	repoDir := filepath.Dir(filepath.Dir(filepath.Dir(repoGitDir)))

	cmd := exec.Command("git", "worktree", "remove", "--force", path)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (p *gitWorktreeProvider) Status() (string, error) {
	base := p.getWorktreeBase()
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", base, err)
	}
	var lines []string
	for _, e := range entries {
		if e.IsDir() {
			wtDir := filepath.Join(base, e.Name())
			// Check it looks like a valid worktree (has .git file).
			if _, err := os.Stat(filepath.Join(wtDir, ".git")); err == nil {
				lines = append(lines, wtDir)
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

// stableHash returns a deterministic short hex string from a path, used
// as the directory name for git fallback worktrees.
func stableHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

// --- isolation helpers ---

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

// AssertNotTangled checks if the project's primary checkout at projectDir is on a
// non-default branch. Returns nil if HEAD is detached or on the default branch.
func AssertNotTangled(projectDir, projectName string) error {
	// Check current HEAD state
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil {
		// Can't determine branch state; skip tangle check
		return nil
	}
	branch := strings.TrimSpace(string(out))

	// Detached HEAD is the normal/expected state for worktree usage
	if branch == "HEAD" {
		return nil
	}

	// Get the default branch from origin/HEAD, with main/master fallback
	cmd = exec.Command("git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	cmd.Dir = projectDir
	out, err = cmd.Output()
	if err == nil {
		defaultRef := strings.TrimSpace(string(out))
		defaultBranch := strings.TrimPrefix(defaultRef, "origin/")

		// On the default branch = no tangle
		if branch == defaultBranch {
			return nil
		}
	} else {
		// Fall back to common default branch names when origin/HEAD unavailable
		foundDefault := false
		for _, candidate := range []string{"main", "master"} {
			chk := exec.Command("git", "rev-parse", "--verify", candidate)
			chk.Dir = projectDir
			if err := chk.Run(); err == nil {
				foundDefault = true
				// On the default branch = no tangle
				if branch == candidate {
					return nil
				}
				break
			}
		}
		if !foundDefault {
			// Can't determine default branch; skip tangle check
			return nil
		}
	}
	// Tangle detected: on a non-default branch in the primary checkout
	return fmt.Errorf("cannot spawn: %s is on branch %s, not an isolated worktree. Use a detached HEAD or a worktree",
		projectName, branch)
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
