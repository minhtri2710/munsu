package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// ReviewDiff runs `munsu review-diff` for the given task.
// It compares the soldier branch against the authoritative base and prints
// a Markdown diff summary. It uses the stored delivery identity when available
// rather than reconstructing from the current branch state.
func ReviewDiff(homeDir string, id string) error {
	meta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		return fmt.Errorf("reading task %s meta: %w", id, err)
	}

	agg, err := currentCanonical(homeDir, id)
	if err != nil {
		return fmt.Errorf("reading task %s: %w", id, err)
	}
	if agg.Definition.Project == "" {
		return fmt.Errorf("task %s has no project", id)
	}

	worktreePath, ok := meta["worktree"]
	if !ok || worktreePath == "" {
		return fmt.Errorf("task %s has no worktree path in meta", id)
	}

	// Check worktree exists
	if _, err := os.Stat(worktreePath); err != nil {
		return fmt.Errorf("worktree %s does not exist: %w", worktreePath, err)
	}

	// Detect the current branch on the worktree
	currentBranch, err := gitBranch(worktreePath)
	if err != nil {
		return fmt.Errorf("detecting current branch: %w", err)
	}

	// Resolve authoritative base using stored identity if available
	base := ""

	ident, _ := domain.IdentityFromMeta(meta)
	if ident != nil && ident.BaseRef != "" {
		// Diff against the stored identity's base branch as the worktree's
		// origin remote knows it.
		base = "origin/" + ident.BaseRef
	} else {
		// Use default branch
		def, derr := backend.DefaultBranch(worktreePath)
		if derr != nil {
			return fmt.Errorf("cannot detect default branch: %w", derr)
		}
		base = def.Ref

		// Warn if the local default branch, when there is one, is stale vs origin
		if def.Ref == def.Name {
			warn, werr := checkDefaultBranchStale(worktreePath, def.Name)
			if werr == nil && warn != "" {
				fmt.Fprintf(os.Stderr, "warning: %s\n", warn)
			}
		}
	}

	// Compute diff summary
	summary, err := gitDiffSummary(worktreePath, base, currentBranch)
	if err != nil {
		return fmt.Errorf("computing diff: %w", err)
	}

	fmt.Println(summary)
	return nil
}

// gitBranch returns the current branch name.
func gitBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// checkDefaultBranchStale warns if the local default branch is behind origin.
func checkDefaultBranchStale(repoPath, branch string) (string, error) {
	// Fetch to update refs (lightweight, no merge)
	fetchCmd := exec.Command("git", "fetch", "origin", branch, "--quiet")
	fetchCmd.Dir = repoPath
	_ = fetchCmd.Run()

	// Compare local vs remote
	cmd := exec.Command("git", "rev-list", "--left-right", "--count",
		fmt.Sprintf("%s...origin/%s", branch, branch))
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", nil // ignore errors (e.g., no remote)
	}

	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) == 2 && parts[1] != "0" {
		return fmt.Sprintf("default branch %q is %s commits behind origin/%s - consider pulling first", branch, parts[1], branch), nil
	}
	return "", nil
}

// gitDiffSummary generates a Markdown diff summary between base and branch.
func gitDiffSummary(repoPath, base, branch string) (string, error) {
	mbCmd := exec.Command("git", "merge-base", base, branch)
	mbCmd.Dir = repoPath
	mbOut, err := mbCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git merge-base %s %s: %w", base, branch, err)
	}
	mergeBase := strings.TrimSpace(string(mbOut))

	// Numstat for counts
	numstatCmd := exec.Command("git", "diff", "--numstat", mergeBase+".."+branch)
	numstatCmd.Dir = repoPath
	numstatOut, err := numstatCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff --numstat: %w", err)
	}

	// Count files changed, insertions, deletions
	var files int
	var insertions, deletions int64
	for _, line := range strings.Split(strings.TrimSpace(string(numstatOut)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			files++
			ins, _ := strconv.ParseInt(fields[0], 10, 64)
			insertions += ins
			del, _ := strconv.ParseInt(fields[1], 10, 64)
			deletions += del
		}
	}

	// Get diff stat
	statCmd := exec.Command("git", "diff", "--stat", mergeBase+".."+branch)
	statCmd.Dir = repoPath
	statOut, _ := statCmd.Output()

	// Get shortlog of commits
	logCmd := exec.Command("git", "log", "--oneline", "--no-decorate",
		fmt.Sprintf("%s..%s", mergeBase, branch))
	logCmd.Dir = repoPath
	logOut, _ := logCmd.Output()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Review Diff: `%s` -> `%s`\n\n", branch, base))
	b.WriteString(fmt.Sprintf("- **Merge base:** `%s`\n", mergeBase[:min(len(mergeBase), 12)]))
	b.WriteString(fmt.Sprintf("- **Current branch:** `%s`\n", branch))
	b.WriteString(fmt.Sprintf("- **Base:** `%s`\n\n", base))

	if files == 0 {
		b.WriteString("*No differences - branches are at the same commit.*\n")
		return b.String(), nil
	}

	b.WriteString("### Summary\n\n")
	b.WriteString("| Metric | Value |\n|--------|-------|\n")
	b.WriteString(fmt.Sprintf("| Files changed | %d |\n", files))
	b.WriteString(fmt.Sprintf("| Insertions | %d |\n", insertions))
	b.WriteString(fmt.Sprintf("| Deletions | %d |\n\n", deletions))

	b.WriteString("### Commits\n\n")
	b.WriteString("```\n")
	b.WriteString(string(logOut))
	if len(logOut) > 0 && logOut[len(logOut)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	b.WriteString("### Diff Stat\n\n")
	b.WriteString("```\n")
	b.WriteString(string(statOut))
	if len(statOut) > 0 && statOut[len(statOut)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("```\n")

	return b.String(), nil
}
