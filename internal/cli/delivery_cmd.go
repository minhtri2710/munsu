package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/delivery"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/spf13/cobra"
)

func newDeliveryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delivery",
		Short: "Manage delivery operations",
		Long: `Manage delivery operations: review diffs, check PR status,
merge PRs, and merge branches locally.`,
	}
	cmd.AddCommand(newReviewDiffCmd())
	cmd.AddCommand(newPRCheckCmd())
	cmd.AddCommand(newPRMergeCmd())
	cmd.AddCommand(newMergeLocalCmd())
	return cmd
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
			return delivery.ReviewDiff(ctx.Home, args[0])
		}),
	}
}

func newPRCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pr-check <id> <pr-url>",
		Short: "Record PR URL and arm merge poll",
		Long: `Parse a full GitHub PR URL, record the PR and head SHA in task meta,
and write a check.sh script to poll the PR merge status via gh CLI.

PR URL format: https://github.com/<owner>/<repo>/pull/<n>`,
		Args: ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			prURL := args[1]

			// Preflight: verify task meta exists with kind=ship
			meta, err := task.ReadMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("reading meta for %s: %w", id, err)
			}
			if meta["kind"] != "ship" {
				return fmt.Errorf("task %s has kind=%q, pr-check requires kind=ship (promote scout tasks before checking PRs)", id, meta["kind"])
			}

			return delivery.PRCheck(ctx.Home, id, prURL)
		}),
	}
}

func newPRMergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr-merge <id> <pr-url> [-- --merge|--rebase]",
		Short: "Merge a PR via gh-axi",
		Long: `Merge a PR via gh-axi CLI. Repository is derived from the PR URL.
Default merge method is squash.

Use -- --merge or -- --rebase to override the merge method.
The --repo/-R flag is not allowed (repository comes from the URL).

PR URL format: https://github.com/<owner>/<repo>/pull/<n>`,
		Args: MinimumNArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			prURL := args[1]
			extra := args[2:]

			// Preflight: verify task meta exists with kind=ship
			meta, err := task.ReadMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("reading meta for %s: %w", id, err)
			}
			if meta["kind"] != "ship" {
				return fmt.Errorf("task %s has kind=%q, pr-merge requires kind=ship (promote scout tasks before merging)", id, meta["kind"])
			}

			return delivery.PRMerge(ctx.Home, id, prURL, extra)
		}),
	}
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
			return delivery.MergeLocal(ctx.Home, args[0])
		}),
	}
}
