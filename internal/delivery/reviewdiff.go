package delivery

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/ghurl"
	"github.com/minhtri2710/munsu/internal/task"
)

// ReviewDiff runs `munsu review-diff` for the given task.
// It compares the soldier branch against the authoritative base and prints
// a Markdown diff summary. It uses the stored delivery identity when available
// rather than reconstructing from the current branch state.
func ReviewDiff(homeDir string, id string) error {
	meta, err := task.ReadMeta(homeDir, id)
	if err != nil {
		return fmt.Errorf("reading task %s meta: %w", id, err)
	}

	projectName, ok := meta["project"]
	if !ok || projectName == "" {
		return fmt.Errorf("task %s has no project in meta", id)
	}
	_ = projectName

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

	ident, _ := IdentityFromMeta(meta)
	if ident != nil && ident.URL != "" {
		// Use the stored identity for the base reference
		ghURL, err := ghurl.ParseGHURL(ident.URL)
		if err != nil {
			return fmt.Errorf("parsing PR URL from delivery identity: %w", err)
		}
		base = fmt.Sprintf("refs/pull/%d/merge", ghURL.Num)
	} else {
		prURL, hasPR := meta["pr"]
		if hasPR && prURL != "" {
			// PR tasks: compare against PR's merge ref (legacy key)
			ghURL, err := ghurl.ParseGHURL(prURL)
			if err != nil {
				return fmt.Errorf("parsing PR URL from meta: %w", err)
			}
			base = fmt.Sprintf("refs/pull/%d/merge", ghURL.Num)
		} else {
			// Use default branch
			defaultBranch, derr := gitDefaultBranch(worktreePath)
			if derr != nil {
				return fmt.Errorf("cannot detect default branch: %w", derr)
			}
			base = defaultBranch

			// Warn if local default branch is stale vs origin
			warn, werr := checkDefaultBranchStale(worktreePath, defaultBranch)
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

// gitDefaultBranch returns the default branch name (e.g., main, master).
func gitDefaultBranch(repoPath string) (string, error) {
	// Try origin/HEAD first
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		// refs/remotes/origin/main -> main
		if parts := strings.Split(ref, "/"); len(parts) > 0 {
			return parts[len(parts)-1], nil
		}
	}

	// Fall back to common names
	for _, name := range []string{"main", "master"} {
		cmd := exec.Command("git", "rev-parse", "--verify", name)
		cmd.Dir = repoPath
		if err := cmd.Run(); err == nil {
			return name, nil
		}
	}

	return "", fmt.Errorf("cannot determine default branch")
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
	// Get merge-base
	mergeBase := base
	mbCmd := exec.Command("git", "merge-base", base, branch)
	mbCmd.Dir = repoPath
	mbOut, err := mbCmd.Output()
	if err == nil {
		mergeBase = strings.TrimSpace(string(mbOut))
	}

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
			ins, _ := strconvParseInt(fields[0])
			insertions += ins
			del, _ := strconvParseInt(fields[1])
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
	b.WriteString(fmt.Sprintf("- **Merge base:** `%s`\n", mergeBase[:minInt(len(mergeBase), 12)]))
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

// strconvParseInt is a simplified atoi for int64, returning 0 on error.
func strconvParseInt(s string) (int64, error) {
	var n int64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int64(c-'0')
		} else {
			break
		}
	}
	return n, nil
}

// minInt returns the smaller of two ints.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
