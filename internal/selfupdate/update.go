// Package selfupdate provides fast-forward-only self-update for munsu.
package selfupdate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Update performs a fast-forward-only git pull on the munsu installation.
// It determines the install root by resolving the munsu binary's real path,
// then walking up to find the git repository root.
func Update() error {
	// Find the munsu binary path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding munsu binary: %w", err)
	}

	// Resolve symlinks to get the real path
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving munsu path: %w", err)
	}

	// Walk up to find the git repo root
	installRoot, err := findGitRoot(filepath.Dir(realPath))
	if err != nil {
		return fmt.Errorf("finding munsu repository: %w", err)
	}

	// Verify it's a git repo we can update
	gitMeta, err := os.ReadFile(filepath.Join(installRoot, ".git"))
	if err != nil {
		// Could be a bare .git directory
		gitDirPath := filepath.Join(installRoot, ".git")
		if fi, statErr := os.Stat(gitDirPath); statErr != nil || !fi.IsDir() {
			return fmt.Errorf("%s is not a git repository", installRoot)
		}
	} else {
		// .git is a file pointing to a worktree gitdir — follow it
		content := strings.TrimSpace(string(gitMeta))
		if !strings.HasPrefix(content, "gitdir: ") {
			return fmt.Errorf("unexpected .git format in %s", installRoot)
		}
	}

	// Fetch and fast-forward
	cmd := exec.Command("git", "fetch", "origin")
	cmd.Dir = installRoot
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch failed: %w", err)
	}

	branchBytes, err := gitDir(installRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("determining current branch: %w", err)
	}
	branch := strings.TrimSpace(string(branchBytes))
	if branch == "" || branch == "HEAD" {
		return fmt.Errorf("not on a branch (detached HEAD)")
	}

	// Fast-forward merge
	mergeCmd := exec.Command("git", "merge", "--ff-only", "origin/"+branch)
	mergeCmd.Dir = installRoot
	mergeCmd.Stderr = os.Stderr
	out, err := mergeCmd.Output()
	if err != nil {
		return fmt.Errorf("ff-merge failed (is the tree dirty or diverged?): %w", err)
	}

	fmt.Printf("Updated %s to %s\n", installRoot, strings.TrimSpace(string(out)))

	// Determine commit hash for version stamping
	commit, err := ShortHEAD(installRoot)
	if err != nil {
		return fmt.Errorf("determining commit hash: %w", err)
	}

	// Rebuild binary with version/commit ldflags
	version := VersionString(commit)
	tmpPath := realPath + ".tmp"
	buildCmd := exec.Command("go", "build",
		"-ldflags", fmt.Sprintf("-X github.com/minhtri2710/munsu/internal/cli.Version=%s", version),
		"-o", tmpPath,
		"./cmd/munsu",
	)
	buildCmd.Dir = installRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rebuild failed after update: %w", err)
	}

	// Atomic install: rename temp file over current binary
	if err := os.Rename(tmpPath, realPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("installing updated binary: %w", err)
	}

	fmt.Printf("Rebuilt binary at %s (version %s)\n", realPath, version)
	return nil
}

// findGitRoot walks up from dir to find a git repository root.
func findGitRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	current := abs
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no git repository found from %s", dir)
		}
		current = parent
	}
}

// gitDir runs git with Dir fixed to root. All repo-scoped git goes through this.
func gitDir(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	return cmd.Output()
}

// ShortHEAD returns the short commit SHA at root (never process CWD).
func ShortHEAD(root string) (string, error) {
	out, err := gitDir(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// VersionString builds "0.1.0-dev+<short>" (pure).
func VersionString(shortCommit string) string {
	return fmt.Sprintf("0.1.0-dev+%s", shortCommit)
}
