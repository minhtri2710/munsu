package delivery

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/task"
)

// MergeLocal runs `munsu merge-local <id>`.
// It fast-forward merges the crewmate branch into the local default branch.
// Only works for local-only mode projects (no remote).
func MergeLocal(homeDir string, id string) error {
	meta, err := task.ReadMeta(homeDir, id)
	if err != nil {
		return fmt.Errorf("reading task %s meta: %w", id, err)
	}

	worktreePath, ok := meta["worktree"]
	if !ok || worktreePath == "" {
		return fmt.Errorf("task %s has no worktree path in meta", id)
	}

	// Check worktree exists
	if _, err := os.Stat(worktreePath); err != nil {
		return fmt.Errorf("worktree %s does not exist: %w", worktreePath, err)
	}

	// Check for remote — refuse if remote exists (local-only mode only)
	if hasRemote(worktreePath) {
		return fmt.Errorf("project has remotes — use pr-merge for remote delivery, not merge-local")
	}

	// Detect current branch
	currentBranch, err := gitBranch(worktreePath)
	if err != nil {
		return fmt.Errorf("detecting current branch: %w", err)
	}

	if currentBranch == "HEAD" {
		return fmt.Errorf("detached HEAD — cannot merge")
	}

	// Detect default branch
	defaultBranch, err := gitDefaultBranch(worktreePath)
	if err != nil {
		return fmt.Errorf("cannot detect default branch: %w", err)
	}

	// Checkout default branch
	checkoutCmd := exec.Command("git", "checkout", defaultBranch)
	checkoutCmd.Dir = worktreePath
	checkoutOut, err := checkoutCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout %s: %s", defaultBranch, strings.TrimSpace(string(checkoutOut)))
	}

	// Fast-forward merge
	mergeCmd := exec.Command("git", "merge", "--ff-only", currentBranch)
	mergeCmd.Dir = worktreePath
	mergeOut, err := mergeCmd.CombinedOutput()
	if err != nil {
		// Try to checkout back to the original branch on failure
		revertCmd := exec.Command("git", "checkout", currentBranch)
		revertCmd.Dir = worktreePath
		_ = revertCmd.Run()

		return fmt.Errorf("git merge --ff-only: %s", strings.TrimSpace(string(mergeOut)))
	}

	fmt.Printf("Fast-forward merged %s into %s\n", currentBranch, defaultBranch)
	fmt.Printf("Result:\n%s\n", strings.TrimSpace(string(mergeOut)))
	return nil
}

// hasRemote checks whether the repo has any git remotes.
func hasRemote(repoPath string) bool {
	cmd := exec.Command("git", "remote")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}
