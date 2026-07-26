// Package backend provides terminal session resolution, session adapters (tmux, herdr, orca),
// worktree pool management, and home tag helpers.
package backend

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/hometag"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/worktree"
)

// Re-export session interfaces and errors
type Backend = session.Backend
type AgentAwareBackend = session.AgentAwareBackend
type BackendMetaExtras = session.BackendMetaExtras

var ErrPaneNotFound = session.ErrPaneNotFound
var ErrAgentNotFound = session.ErrAgentNotFound

// Resolve returns the configured session backend for homeDir with resilient fallback to tmux.
func Resolve(homeDir string, backendOverride string) (session.Backend, string, error) {
	bk, name, err := session.Resolve(homeDir, backendOverride)
	if err == nil && bk != nil {
		return bk, name, nil
	}

	// Resilient fallback to tmux when non-tmux backend fails
	if backendOverride != "tmux" && backendOverride != "" {
		fmt.Fprintf(os.Stderr, "munsu: warning: backend %q failed resolution (%v); falling back to tmux\n", backendOverride, err)
		if tmuxBk, errTmux := session.Select("tmux"); errTmux == nil {
			return tmuxBk, "tmux", nil
		}
	}
	return nil, "", fmt.Errorf("session backend resolution failed: %w", err)
}

// BackendForTask resolves the session backend for a specific task using its metadata,
// falling back to tmux if the specified backend is unavailable.
func BackendForTask(homeDir string, meta map[string]string) (session.Backend, string, error) {
	bk, name, err := session.BackendForTask(homeDir, meta)
	if err == nil && bk != nil {
		return bk, name, nil
	}

	// Fallback to tmux
	if tmuxBk, errTmux := session.Select("tmux"); errTmux == nil {
		return tmuxBk, "tmux", nil
	}
	return nil, "", fmt.Errorf("task session backend resolution failed: %w", err)
}

// Worktree helpers re-exported for convenience
func GetWorktree(homeDir, repoPath string, lease bool) (string, error) {
	return worktree.Get(homeDir, repoPath, lease)
}

func ReturnWorktree(homeDir, path string) error {
	return worktree.Return(homeDir, path)
}

func WorktreeStatus(homeDir string) (string, error) {
	return worktree.Status(homeDir)
}

func IsWorktreeIsolated(path string) (bool, error) {
	return worktree.IsIsolated(path)
}

func EnsureWorktreeNotPrimary(path string) error {
	return worktree.EnsureNotPrimary(path)
}

func AssertNotTangled(projectDir, projectName string) error {
	return worktree.AssertNotTangled(projectDir, projectName)
}

func Hometag(homeDir string) string {
	return hometag.Tag(homeDir)
}

func WorkspaceTag(homeDir string) string {
	return hometag.WorkspaceTag(homeDir)
}
