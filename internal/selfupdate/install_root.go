// Package selfupdate provides fast-forward-only self-update for munsu.
package selfupdate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
)

// ResolveInstallRoot resolves the munsu install root using fail-closed
// precedence:
//
//  1. --repo <path> flag (repoOpt)
//  2. MUNSU_REPO environment variable
//  3. Persisted path at <MUNSU_HOME>/config/install-root
//  4. Executable ancestry (binary inside a git checkout)
//  5. Current working directory (identity-matching munsu checkout)
//  6. Actionable error
//
// Each tier is fail-closed: if the value is non-empty, it must resolve to a
// valid munsu repo or return an error. Only an empty/absent value falls
// through to the next tier.
func ResolveInstallRoot(homeDir, repoOpt string) (string, error) {
	// Tier 1: --repo <path> flag
	if repoOpt != "" {
		return verifyAndCanonicalize(repoOpt)
	}

	// Tier 2: MUNSU_REPO environment variable
	if envRepo := os.Getenv("MUNSU_REPO"); envRepo != "" {
		return verifyAndCanonicalize(envRepo)
	}

	// Tier 3: Persisted path at <MUNSU_HOME>/config/install-root
	if homeDir != "" {
		persisted, err := config.Get(homeDir, "install-root")
		if err == nil && persisted != "" {
			return verifyAndCanonicalize(persisted)
		}
	}

	// Tier 4: Executable ancestry when binary is inside a git checkout.
	execPath, err := os.Executable()
	if err == nil {
		if root, err := resolveBinaryAncestry(execPath); err == nil {
			return root, nil
		}
	}

	// Tier 5: Current working directory matching a munsu checkout.
	if root, err := resolveCwdAncestry(); err == nil {
		return root, nil
	}

	// Tier 6: Actionable error.
	persistHint := ""
	if homeDir != "" {
		persistHint = fmt.Sprintf("    munsu update --repo <path>  (persisted to %s/config/install-root)\n", homeDir)
	}
	return "", fmt.Errorf("cannot determine munsu install root\n  Provide one of:\n    --repo <path>          explicit source checkout path\n    MUNSU_REPO=<path>      environment variable\n%s    Run from within a munsu repository checkout", persistHint)
}

// PersistInstallRoot writes the canonical install root path to
// <MUNSU_HOME>/config/install-root. Must only be called after a successful
// identity-verified self-update. The path is canonicalized and identity
// verified before writing.
func PersistInstallRoot(homeDir, root string) error {
	canonical, err := gitToplevel(root)
	if err != nil {
		return fmt.Errorf("persist install-root: %w", err)
	}
	if err := verifyMunsuModule(canonical); err != nil {
		return fmt.Errorf("persist install-root: %w", err)
	}
	return config.Set(homeDir, "install-root", canonical)
}

// verifyAndCanonicalize checks that path is a munsu git checkout and returns
// the canonical git worktree root. Fail-closed: any mismatch is an error.
func verifyAndCanonicalize(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving path %q: %w", path, err)
	}

	// Check that the path exists and is a directory.
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("path %q does not exist: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", abs)
	}

	// Resolve to canonical git worktree root.
	root, err := gitToplevel(abs)
	if err != nil {
		return "", fmt.Errorf("%q is not inside a git worktree: %w", abs, err)
	}

	// Verify repository identity: go.mod module declaration.
	if err := verifyMunsuModule(root); err != nil {
		return "", fmt.Errorf("%q: %w", root, err)
	}

	return root, nil
}

// gitToplevel returns the canonical git worktree root using
// `git rev-parse --show-toplevel`.
func gitToplevel(path string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// verifyMunsuModule checks that the repo at root has a go.mod with the
// canonical munsu module path on the first line matching "module "
// (not just contains, to avoid false matches in comments).
func verifyMunsuModule(root string) error {
	goModPath := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("not a munsu repository (no go.mod: %w)", err)
	}

	moduleLine := "module github.com/minhtri2710/munsu"
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == moduleLine {
			return nil
		}
	}
	return fmt.Errorf("not a munsu repository (expected %q in go.mod)", moduleLine)
}

// resolveBinaryAncestry walks up from the binary's directory to find a git
// checkout, then verifies it's munsu. Returns the canonical root.
func resolveBinaryAncestry(execPath string) (string, error) {
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks: %w", err)
	}

	root, err := findGitRoot(filepath.Dir(realPath))
	if err != nil {
		return "", fmt.Errorf("binary ancestry: %w", err)
	}

	if err := verifyMunsuModule(root); err != nil {
		return "", fmt.Errorf("binary ancestry: %w", err)
	}

	return gitToplevel(root)
}

// resolveCwdAncestry checks whether the current working directory is inside a
// munsu git checkout. Returns the canonical root.
func resolveCwdAncestry() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting cwd: %w", err)
	}

	root, err := findGitRoot(cwd)
	if err != nil {
		return "", fmt.Errorf("cwd ancestry: not inside a git checkout")
	}

	if err := verifyMunsuModule(root); err != nil {
		return "", fmt.Errorf("cwd ancestry: %w", err)
	}

	return gitToplevel(root)
}
