package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

func newDeliveryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delivery",
		Short: "Manage delivery operations",
		Long: `Manage delivery operations: review diffs, check PR status,
merge PRs, amend PR identities, reconcile stale metadata,
and merge branches locally.`,
	}
	cmd.AddCommand(newReviewDiffCmd())
	cmd.AddCommand(newPRCheckCmd())
	cmd.AddCommand(newPRMergeCmd())
	cmd.AddCommand(newMergeLocalCmd())
	cmd.AddCommand(newMergeStatusCmd())
	cmd.AddCommand(newPRAmendCmd())
	cmd.AddCommand(newReconcileCmd())
	return cmd
}

func newMergeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge-status <id>",
		Short: "Query delivery merge status via provider-neutral seam",
		Long: `Query the current merge status of a delivery identity (PR or MR)
via the provider-neutral QueryDeliveryMergeStatus seam.
Exit: 0 = merged, 1 = not merged/open/closed, 2+ = error.
Used by watcher .check scripts.`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return fleet.MergeStatus(ctx.Home, args[0])
		}),
	}
}

func newReviewDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review-diff <id>",
		Short: "Review diff between soldier branch and base",
		Long: `Compare the soldier branch against the authoritative base and print
a Markdown diff summary.

For registered projects with a remote, compares against the default branch.
For PR tasks (where meta has pr=), fetches the PR head and compares.
Warns if local default branch is stale vs origin.`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return fleet.ReviewDiff(ctx.Home, args[0])
		}),
	}
}

func newPRCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pr-check <id> <pr-url>",
		Short: "Record PR URL and arm merge poll",
		Long: `Parse a full GitHub PR URL, record the PR and head SHA in task meta,
and write a check.sh script to poll the PR merge status via gh CLI.

PR URL format: https://github.com/<owner>/<repo>/pull/<n>

Task meta is resolved from the current home first, then each registered
captain home (so general can arm checks after captain handoff + spawn).`,
		Args: ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			prURL := args[1]

			taskHome, _, err := fleet.RequireShipMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("pr-check %s: %w", id, err)
			}

			return fleet.PRCheck(taskHome, id, prURL)
		}),
	}
}

func newPRMergeCmd() *cobra.Command {
	var doTeardown bool
	cmd := &cobra.Command{
		Use:   "pr-merge <id> <pr-url> [-- --merge|--rebase]",
		Short: "Merge a PR via gh-axi",
		Long: `Merge a PR via gh-axi CLI. Repository is derived from the PR URL.
Default merge method is squash.

Use -- --merge or -- --rebase to override the merge method.
The --repo/-R flag is not allowed (repository comes from the URL).

PR URL format: https://github.com/<owner>/<repo>/pull/<n>

Task meta is resolved from the current home first, then each registered
captain home (so general can merge after captain handoff + spawn).

Merge does not remove soldier panes or worktrees. Pass --teardown to run
munsu teardown on the task home after a successful merge (landed cleanup).
Without --teardown, the command prints the exact teardown invocation to run next.`,
		Args: MinimumNArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			prURL := args[1]
			extra := args[2:]

			taskHome, _, err := fleet.RequireShipMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("pr-merge %s: %w", id, err)
			}

			if !doTeardown {
				// Without --teardown: run PRMerge only (no retirement).
				// On retry, if already merged, PRMerge will fail closed —
				// the user should retry with --teardown to resume retirement.
				if err := fleet.PRMerge(taskHome, id, prURL, extra); err != nil {
					return err
				}
				return nil
			}
			// With --teardown: use MergeAndRetire which handles both the
			// merge delivery and retirement. On retry after partial cleanup,
			// it detects delivery_state=merged and resumes retirement only.
			fmt.Printf("Running merge-and-retire for %s in %s...\n", id, taskHome)
			mars := fleet.MergeAndRetire(taskHome, id, prURL, extra, newSessionBoundTeardown(), orchestratorRetirementJournals{})
			if mars.TeardownResult != nil {
				for _, step := range mars.TeardownResult.Steps {
					fmt.Println(step)
				}
			}
			if mars.IsError() {
				if mars.TeardownError != nil {
					return fmt.Errorf("post-merge teardown %s: %w", id, mars.TeardownError)
				}
				return fmt.Errorf("merge-and-retire %s: %s %s", id, mars.MergeOutcome, mars.MergeDetail)
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&doTeardown, "teardown", false, "after successful merge, teardown the soldier (pane+worktree+meta) in the task home")
	return cmd
}

func newMergeLocalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge-local <id>",
		Short: "Fast-forward merge to local default branch",
		Long: `Fast-forward merge the soldier branch into the local default branch.
Only works for local-only mode projects (no remote).
Refuses if the merge is not a clean fast-forward.`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return fleet.MergeLocal(ctx.Home, args[0])
		}),
	}
}

func newPRAmendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pr-amend <id>",
		Short: "Amend delivery identity after new push (atomic CAS)",
		Long: `Begin an amendment and immediately accept it, transitioning the
delivery lifecycle through amending -> review-ready.

The stored identity is CAS-checked against the current meta. The provider
is queried for the new head SHA. If the old head is an ancestor of the new
head (no force-push), the identity is updated atomically.

Use 'delivery reconcile' to recover from already-stale metadata.`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]

			// Read worktree from meta
			wtPath, err := resolveWorktree(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("pr-amend: %w", err)
			}

			// Begin amendment (CAS review-ready -> amending) — idempotent: if already
			// in amending state (e.g. retry after partial failure), skip begin.
			currentMeta, err := home.ReadMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("pr-amend: reading meta: %w", err)
			}

			if currentMeta[fleet.MetaDeliveryState] != string(fleet.DeliveryStateAmending) {
				if _, err := fleet.BeginAmendment(ctx.Home, id); err != nil {
					return fmt.Errorf("pr-amend: begin: %w", err)
				}
			}

			// Accept amendment (verify provider, CAS update identity)
			newIdent, record, err := fleet.AcceptAmendment(ctx.Home, id, wtPath)
			if err != nil {
				return fmt.Errorf("pr-amend: accept: %w", err)
			}

			fmt.Printf("PR identity amended:\n")
			fmt.Printf("  PR:   %s/%s#%d\n", newIdent.Owner, newIdent.Repo, newIdent.Number)
			fmt.Printf("  Old:  %s\n", record.OldHeadSHA)
			fmt.Printf("  New:  %s\n", record.NewHeadSHA)
			fmt.Printf("  Reason: %s\n", record.Reason)
			return nil
		}),
	}
}

func newReconcileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile <id>",
		Short: "Reconcile stale delivery metadata from provider",
		Long: `Query the provider for the current PR/MR state and update the stored
identity if the PR advanced without a proper amendment cycle.

Requires only the stored identity and provider access. No manual meta
editing or --force needed.

Supports both open and merged PRs. For merged PRs, sets delivery_state=merged.
For open PRs with advanced heads, updates the identity and sets state=review-ready.

Rejects force-push, rewritten ancestry, branch replacement, and ambiguous
state. Use 'pr-check' to recapture from scratch after such events.`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]

			// Read worktree from meta
			wtPath, err := resolveWorktree(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("reconcile: %w", err)
			}

			newIdent, record, err := fleet.ReconcileIdentity(ctx.Home, id, wtPath)
			if err != nil {
				return fmt.Errorf("reconcile: %w", err)
			}

			if record == nil {
				fmt.Printf("Identity already up to date: %s/%s#%d (head=%s)\n",
					newIdent.Owner, newIdent.Repo, newIdent.Number, newIdent.HeadSHA)
				return nil
			}

			fmt.Printf("Identity reconciled:\n")
			fmt.Printf("  PR:   %s/%s#%d\n", newIdent.Owner, newIdent.Repo, newIdent.Number)
			fmt.Printf("  Old:  %s\n", record.OldHeadSHA)
			fmt.Printf("  New:  %s\n", record.NewHeadSHA)
			fmt.Printf("  Reason: %s\n", record.Reason)
			return nil
		}),
	}
}

// resolveWorktree reads the worktree path from task meta for a given task.
// Returns an error if the worktree is not set in meta.
func resolveWorktree(homeDir, id string) (string, error) {
	meta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		return "", fmt.Errorf("reading meta: %w", err)
	}
	wtPath, ok := meta["worktree"]
	if !ok || wtPath == "" {
		return "", fmt.Errorf("no worktree path in meta for task %s", id)
	}
	return wtPath, nil
}
