package fleet

import (
	"fmt"
	"os"
	"path/filepath"
)

// GitShimBinRelPath is the fleet git-shim directory, relative to a munsu home.
// The soldier/captain launch script prepends it to PATH so agent-issued git
// resolves to the shim (munsu git-guard) instead of the real git; the munsu
// binary strips this same path so its own git runs unfenced
// (cli.stripShimDirFromPath, which matches this suffix rather than rederiving
// the absolute dir, so the strip does not depend on MUNSU_HOME). It lives under
// the home's already-untracked state/ tree, never inside a worktree, so it
// needs no launch-envelope manifest entry.
const GitShimBinRelPath = "state/shim/bin"

// provisionGitShim writes the git shim under <homeDir>/state/shim/bin/git and
// returns the shim directory to prepend to PATH. The shim is a small bash
// script that re-invokes this munsu binary's git-guard with the git arguments;
// the binary path is resolved (symlinks followed) at provision time so a reset
// PATH that drops munsu fails loudly rather than silently bypassing the fence.
// It is idempotent (write-if-differs) and shared by every soldier/captain of
// the home. Provisioning failure is returned so the caller fails the launch
// closed.
func provisionGitShim(homeDir string) (string, error) {
	shimDir := filepath.Join(homeDir, filepath.FromSlash(GitShimBinRelPath))
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return "", fmt.Errorf("provisioning git shim directory: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("provisioning git shim: resolving munsu binary: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
		self = resolved
	}
	script := "#!/usr/bin/env bash\nexec " + spawnShQuote(self) + " git-guard \"$@\"\n"
	shimPath := filepath.Join(shimDir, "git")
	if err := atomicWriteFile(shimPath, []byte(script), 0o755); err != nil {
		return "", fmt.Errorf("provisioning git shim: %w", err)
	}
	return shimDir, nil
}
