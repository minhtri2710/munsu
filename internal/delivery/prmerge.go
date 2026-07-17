package delivery

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/ghurl"
	"github.com/minhtri2710/munsu/internal/task"
)

// PRMerge runs `munsu pr-merge <id> <pr-url> [--merge|--rebase]`.
// It merges a PR via gh-axi CLI and records the PR info in task meta.
// After merging, it also runs a best-effort fleet sync of the project clone.
// The prURL must be a full https://github.com/<owner>/<repo>/pull/<n> URL.
// Extra args after `--` can specify merge method: `-- --merge`, `-- --rebase`.
func PRMerge(homeDir string, id, prURL string, extraArgs []string) error {
	// Reject --repo/-R overrides in extraArgs
	for _, arg := range extraArgs {
		if arg == "--repo" || arg == "-R" || strings.HasPrefix(arg, "--repo=") {
			return fmt.Errorf("--repo/-R is not allowed: repository is derived from the PR URL")
		}
	}

	// Parse the PR URL
	ghURL, err := ghurl.ParseGHURL(prURL)
	if err != nil {
		return fmt.Errorf("invalid PR URL: %w", err)
	}

	// Verify PR is open before attempting merge
	if err := checkPROpen(ghURL); err != nil {
		return err
	}

	// Determine merge method (default squash)
	method := "squash"
	for _, arg := range extraArgs {
		switch arg {
		case "--merge":
			method = "merge"
		case "--rebase":
			method = "rebase"
		}
	}

	// Build gh-axi args
	ghAxiPath, err := exec.LookPath("gh-axi")
	if err != nil {
		return fmt.Errorf("gh-axi not found on PATH: %w", err)
	}

	args := []string{"pr", "merge",
		fmt.Sprintf("%d", ghURL.Num),
		"--repo", fmt.Sprintf("%s/%s", ghURL.Owner, ghURL.Repo),
		fmt.Sprintf("--%s", method),
	}

	cmd := exec.Command(ghAxiPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh-axi pr merge: %w", err)
	}

	// Write PR info to task meta
	meta, err := task.ReadMeta(homeDir, id)
	if err != nil {
		meta = make(map[string]string)
	}
	meta["pr"] = prURL
	meta["pr_head"] = "" // not known at merge time without an extra API call
	if err := task.WriteMeta(homeDir, id, meta); err != nil {
		return fmt.Errorf("writing task meta: %w", err)
	}

	fmt.Printf("PR merged: %s (%s method)\n", ghURL.FormatPRRef(), method)

	// Best-effort fleet-sync the project clone
	if project := meta["project"]; project != "" {
		if res, err := fleet.Sync(homeDir, project); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: fleet-sync for %s failed: %v\n", project, err)
		} else if len(res.Stuck) > 0 {
			for _, s := range res.Stuck {
				fmt.Fprintf(os.Stderr, "Warning: fleet-sync: %s\n", s)
			}
		}
	}
	return nil
}

// checkPROpen verifies that a PR is in OPEN state before attempting merge.
// Uses `gh pr view --json state` to check the current PR state.
// Returns an error if the PR is merged, closed, or unreachable.
func checkPROpen(ghURL ghurl.GHURL) error {
	cmd := exec.Command("gh", "pr", "view",
		fmt.Sprintf("%d", ghURL.Num),
		"--repo", fmt.Sprintf("%s/%s", ghURL.Owner, ghURL.Repo),
		"--json", "state",
		"--jq", ".state",
	)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("checking PR state: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("checking PR state: %w", err)
	}

	state := strings.TrimSpace(string(out))
	switch state {
	case "OPEN":
		return nil
	case "MERGED":
		return fmt.Errorf("PR #%d is already merged (state=%s): refusing to merge", ghURL.Num, state)
	case "CLOSED":
		return fmt.Errorf("PR #%d is closed (state=%s): refusing to merge", ghURL.Num, state)
	default:
		return fmt.Errorf("PR #%d has unexpected state %q: refusing to merge", ghURL.Num, state)
	}
}
