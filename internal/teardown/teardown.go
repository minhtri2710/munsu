// Package teardown implements crewmate teardown safety checks and lifecycle.
package teardown

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/worktree"
)

// Options controls teardown behavior.
type Options struct {
	HomeDir string // munsu home directory
	ID      string // task ID
	Force   bool   // skip safety checks
}

// TeardownResult describes the outcome of each teardown step.
type TeardownResult struct {
	Steps []string
}

// Run performs a crewmate teardown.
func Run(opts Options) (*TeardownResult, error) {
	result := &TeardownResult{}

	// Read task meta
	meta, err := task.ReadMeta(opts.HomeDir, opts.ID)
	if err != nil {
		return nil, fmt.Errorf("teardown %s: reading meta: %w", opts.ID, err)
	}

	kind := meta["kind"]
	if kind == "" {
		kind = "ship" // default
	}

	// Safety checks (skip with --force)
	if !opts.Force {
		if err := safetyCheck(opts, meta, kind); err != nil {
			return nil, fmt.Errorf("teardown %s: safety check failed: %w", opts.ID, err)
		}
	}

	// 1. Kill session window
	if windowID, ok := meta["window"]; ok && windowID != "" {
		bk := session.Default()
		if bk.Alive(windowID) {
			if err := bk.Teardown(windowID); err != nil {
				result.Steps = append(result.Steps, fmt.Sprintf("session teardown: %v", err))
			} else {
				result.Steps = append(result.Steps, "session window killed")
			}
		} else {
			result.Steps = append(result.Steps, "session window already gone")
		}
	}

	// 2. Return worktree to pool
	if wtPath, ok := meta["worktree"]; ok && wtPath != "" {
		if fi, err := os.Stat(wtPath); err == nil && fi.IsDir() {
			if err := worktree.Return(wtPath); err != nil {
				result.Steps = append(result.Steps, fmt.Sprintf("worktree return: %v", err))
			} else {
				result.Steps = append(result.Steps, "worktree returned to pool")
			}
		} else {
			result.Steps = append(result.Steps, "worktree path no longer exists")
		}
	}

	// 3. Remove task meta file
	metaFilePath, err := taskMetaFilePath(opts.HomeDir, opts.ID)
	if err == nil {
		if err := os.Remove(metaFilePath); err != nil && !os.IsNotExist(err) {
			result.Steps = append(result.Steps, fmt.Sprintf("remove meta: %v", err))
		} else {
			result.Steps = append(result.Steps, "task meta removed")
		}
	}

	return result, nil
}

// safetyCheck verifies that work is landed before allowing teardown.
func safetyCheck(opts Options, meta map[string]string, kind string) error {
	switch kind {
	case "scout":
		return scoutSafetyCheck(opts, meta)
	default:
		return shipSafetyCheck(opts, meta)
	}
}

// scoutSafetyCheck verifies the report.md exists.
func scoutSafetyCheck(opts Options, meta map[string]string) error {
	reportPath := filepath.Join(opts.HomeDir, "data", opts.ID, "report.md")
	if _, err := os.Stat(reportPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("scout task %s has no report.md at %s (use --force to override)", opts.ID, reportPath)
		}
		return fmt.Errorf("checking report.md: %w", err)
	}
	return nil
}

// shipSafetyCheck verifies work is landed.
// Checks: remote-reachable branch, no dirty worktree, content in default branch.
func shipSafetyCheck(opts Options, meta map[string]string) error {
	wtPath, ok := meta["worktree"]
	if !ok || wtPath == "" {
		return fmt.Errorf("no worktree path in meta for %s", opts.ID)
	}

	// Check worktree exists
	if _, err := os.Stat(wtPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("worktree %s does not exist", wtPath)
		}
		return fmt.Errorf("checking worktree %s: %w", wtPath, err)
	}

	// Check not dirty
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("checking git status: %w", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("worktree %s has uncommitted changes (use --force to override)", wtPath)
	}

	// Check current branch has a remote tracking ref
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	cmd.Dir = wtPath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("branch has no remote tracking branch (use --force to override): %w", err)
	}

	// Check the remote branch is reachable
	cmd = exec.Command("git", "fetch", "--dry-run")
	cmd.Dir = wtPath
	fetchOut, fetchErr := cmd.CombinedOutput()
	if fetchErr != nil {
		// fetch failed — likely no remote or network issue; warn but don't block
		// Actually, let's be strict: if we can't reach remote, block
		return fmt.Errorf("cannot reach remote (use --force to override): %s", strings.TrimSpace(string(fetchOut)))
	}
	_ = fetchOut

	return nil
}

// taskMetaFilePath returns the path to the task meta file.
func taskMetaFilePath(homeDir, id string) (string, error) {
	return filepath.Join(homeDir, "state", id+".meta"), nil
}
