package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

var fetchLiveIdentity = CaptureIdentity

func validateLiveIdentity(stored, live *domain.DeliveryIdentity) error {
	if err := domain.ValidateIdentity(live); err != nil {
		return fmt.Errorf("invalid live PR identity: %w", err)
	}
	if stored.Provider != live.Provider || stored.Owner != live.Owner || stored.Repo != live.Repo ||
		stored.Number != live.Number || stored.BaseRef != live.BaseRef || stored.HeadRef != live.HeadRef ||
		stored.HeadSHA != live.HeadSHA {
		return fmt.Errorf("live PR identity changed since capture; re-run pr-check before merge")
	}
	return nil
}

// PRMerge runs `munsu pr-merge <id> <pr-url> [--merge|--rebase]`.
// It merges a PR via gh-axi CLI and validates the delivery identity
// before performing the merge. The identity must have been captured
// by pr-check first.
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
	ghURL, err := domain.ParseGHURL(prURL)
	if err != nil {
		return fmt.Errorf("invalid PR URL: %w", err)
	}

	// Validate stored delivery identity before destructive action
	ident, err := RequireIdentity(homeDir, id)
	if err != nil {
		return fmt.Errorf("cannot merge without valid delivery identity: %w", err)
	}

	// Verify the requested and live PR identities still match the stored capture.
	identURL := domain.GHURL{Owner: ident.Owner, Repo: ident.Repo, Num: ident.Number}.FullURL()
	if identURL != ghURL.FullURL() {
		return fmt.Errorf("PR URL mismatch: stored identity points to %s, but merge target is %s; re-run pr-check to update", identURL, ghURL.FullURL())
	}
	live, err := fetchLiveIdentity(ghURL.FullURL())
	if err != nil {
		return fmt.Errorf("refreshing live PR identity: %w", err)
	}
	if err := validateLiveIdentity(ident, live); err != nil {
		return err
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

	// Merge via consolidated GitHubClient
	client, err := DefaultGitHubClient()
	if err != nil {
		return fmt.Errorf("gh-axi not available: %w", err)
	}
	if err := client.MergePR(ghURL.Owner, ghURL.Repo, ghURL.Num, method); err != nil {
		return fmt.Errorf("merge via gh-axi: %w", err)
	}

	// Reconcile merge delivery: query provider for remote truth, classify the
	// outcome, and persist the result. This replaces the inline post-merge
	// snapshot check with a structured reconciliation that handles merged,
	// already-merged, open, remote-unknown, and failed outcomes.
	result, reconcileErr := ReconcileMergeDelivery(homeDir, id, ident.URL)
	if reconcileErr != nil {
		return fmt.Errorf("post-merge reconciliation: %w", reconcileErr)
	}

	// Set merge method on the result for rendering
	result.MergeMethod = method

	// Print the reconciliation result (human and AXI output)
	fmt.Print(result.Render())

	// Always print the cleanup next step — merge does not teardown panes/worktrees.
	if result.Outcome == MergeOutcomeMerged || result.Outcome == MergeOutcomeAlreadyMerged {
		fmt.Printf("Next: munsu teardown %s --home %s\n", id, homeDir)
		fmt.Printf("  (or re-run pr-merge with --teardown to merge+cleanup in one step)\n")

		// Best-effort fleet-sync the project clone
		meta, _ := home.ReadMeta(homeDir, id)
		if project := meta["project"]; project != "" {
			if res, err := Sync(homeDir, project); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: fleet-sync for %s failed: %v\n", project, err)
			} else if len(res.Stuck) > 0 {
				for _, s := range res.Stuck {
					fmt.Fprintf(os.Stderr, "Warning: fleet-sync: %s\n", s)
				}
			}
		}
	}

	// Return non-zero exit code for partial/unknown outcomes
	if result.IsError() {
		return fmt.Errorf("merge delivery: %s %s", result.Outcome, result.Detail)
	}

	return nil
}

// checkPROpen verifies that a PR is in OPEN state before attempting merge.
// Uses gh-axi via the consolidated GitHubClient when the capability is Ready.
// Falls back to gh CLI for the degraded path.
func checkPROpen(ghURL domain.GHURL) error {
	client, err := DefaultGitHubClient()
	if err == nil {
		state, err := client.ViewPRState(ghURL.Owner, ghURL.Repo, ghURL.Num)
		if err != nil {
			return fmt.Errorf("checking PR state via gh-axi: %w", err)
		}
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

	// Degraded path: gh CLI directly
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
