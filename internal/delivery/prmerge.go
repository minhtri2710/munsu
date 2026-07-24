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

var fetchLiveIdentity = CaptureIdentity

func validateLiveIdentity(stored, live *DeliveryIdentity) error {
	if err := ValidateIdentity(live); err != nil {
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
	ghURL, err := ghurl.ParseGHURL(prURL)
	if err != nil {
		return fmt.Errorf("invalid PR URL: %w", err)
	}

	// Validate stored delivery identity before destructive action
	ident, err := RequireIdentity(homeDir, id)
	if err != nil {
		return fmt.Errorf("cannot merge without valid delivery identity: %w", err)
	}

	// Verify the requested and live PR identities still match the stored capture.
	identURL := ghurl.GHURL{Owner: ident.Owner, Repo: ident.Repo, Num: ident.Number}.FullURL()
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

	// Read existing meta (preserve the full identity, don't clear pr_head)
	meta, err := task.ReadMeta(homeDir, id)
	if err != nil {
		meta = make(map[string]string)
	}

	// Re-query provider snapshot after merge to get final state evidence.
	// Use the stored identity URL for the query.
	snap, snapErr := FetchProviderSnapshot(ident.URL)
	if snapErr != nil {
		return fmt.Errorf("post-merge provider snapshot: %w", snapErr)
	}
	if !snap.Merged {
		return fmt.Errorf("post-merge provider snapshot: PR #%d is not merged (state=%s); retry or investigate manually", snap.Number, snap.State)
	}
	if snap.MergedSHA == "" {
		return fmt.Errorf("post-merge provider snapshot: PR #%d merged but no merge-result evidence; merge may still be in progress, retry later", snap.Number)
	}

	// Verify final head equality (provider-reported head must match stored identity)
	if ident.HeadSHA != "" && snap.HeadSHA != ident.HeadSHA {
		return fmt.Errorf("post-merge provider snapshot: head SHA mismatch: stored %s, provider reports %s for merged PR #%d",
			ident.HeadSHA, snap.HeadSHA, snap.Number)
	}

	// Build updated identity from snapshot evidence
	finalIdent := &DeliveryIdentity{
		Provider:   ident.Provider,
		Owner:      ident.Owner,
		Repo:       ident.Repo,
		Number:     ident.Number,
		URL:        ident.URL,
		BaseRef:    snap.BaseRef,
		HeadRef:    snap.HeadRef,
		HeadSHA:    snap.HeadSHA,
		CapturedAt: snap.ObservedAt,
	}

	// CAS: verify identity hasn't changed, then update state to merged
	checks := identityChecks(ident)
	checks[MetaDeliveryState] = meta[MetaDeliveryState]

	updates := finalIdent.ToMeta()
	updates[MetaDeliveryState] = string(DeliveryStateMerged)
	updates[MetaIdentityRevision] = incrementRevision(meta[MetaIdentityRevision])

	_, casErr := task.CompareAndSwapMeta(homeDir, id, checks, updates)
	if casErr != nil {
		return fmt.Errorf("post-merge cas: %w", casErr)
	}

	fmt.Printf("PR merged: %s (%s method)\n", ghURL.FormatPRRef(), method)
	// Always print the cleanup next step — merge does not teardown panes/worktrees.
	fmt.Printf("Next: munsu teardown %s --home %s\n", id, homeDir)
	fmt.Printf("  (or re-run pr-merge with --teardown to merge+cleanup in one step)\n")

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
// Uses gh-axi via the consolidated GitHubClient when the capability is Ready.
// Falls back to gh CLI for the degraded path.
func checkPROpen(ghURL ghurl.GHURL) error {
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
