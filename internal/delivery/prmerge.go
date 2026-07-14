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
		} else if len(res.Errors) > 0 {
			for _, e := range res.Errors {
				fmt.Fprintf(os.Stderr, "Warning: fleet-sync: %s\n", e)
			}
		}
	}
	return nil
}
