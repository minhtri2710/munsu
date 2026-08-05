package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

func newDeliveryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delivery",
		Short: "Manage delivery operations",
		Long: `Manage delivery operations: review diffs, check PR status,
and merge PRs through the journaled delivery execution.`,
	}
	cmd.AddCommand(newReviewDiffCmd())
	cmd.AddCommand(newMergeStatusCmd())
	cmd.AddCommand(newPRMergeCmd())
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
			err := fleet.MergeStatus(ctx.Home, args[0])
			var statusErr *fleet.MergeStatusError
			if errors.As(err, &statusErr) && statusErr.Unverifiable {
				return usageError("unverifiable", "Retry after restoring provider access", statusErr.Error())
			}
			return err
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

// buildDeliverRequest builds the typed journaled delivery intent for one
// `pr-merge` invocation from the explicit CLI args (PR/MR URL and merge
// method) and the canonical task identity/bindings. The identity head comes
// from the retained read-only provider snapshot seam; the canonical
// authorization gates it against the bound worktree head, so a stale
// identity fails closed before any mutation.
func buildDeliverRequest(auth *taskauthority.Canonical, taskID, prURL string, extra []string) (fleet.DeliverRequest, error) {
	provider, owner, repo, num, _, err := domain.ParseProviderURL(prURL)
	if err != nil {
		return fleet.DeliverRequest{}, err
	}
	snap, err := fleet.FetchProviderSnapshot(prURL)
	if err != nil {
		return fleet.DeliverRequest{}, fmt.Errorf("capturing delivery identity: %w", err)
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return fleet.DeliverRequest{}, err
	}
	agg, err := auth.Get(tid)
	if err != nil {
		return fleet.DeliverRequest{}, fmt.Errorf("resolving task %s: %w", taskID, err)
	}
	if agg.Worktree == nil {
		return fleet.DeliverRequest{}, fmt.Errorf("task %s has no bound worktree; spawn it before delivery", taskID)
	}
	ident := &domain.DeliveryIdentity{
		Provider:   provider,
		Owner:      owner,
		Repo:       repo,
		Number:     num,
		URL:        prURL,
		BaseRef:    snap.BaseRef,
		HeadRef:    snap.HeadRef,
		HeadSHA:    snap.HeadSHA,
		CapturedAt: snap.ObservedAt,
	}
	method := "squash"
	for _, arg := range extra {
		switch strings.TrimSpace(arg) {
		case "--merge":
			method = "merge"
		case "--rebase":
			method = "rebase"
		case "--squash":
			method = "squash"
		}
	}
	return fleet.DeliverRequest{
		Kind:     taskauthority.DeliveryAuthorizationProviderMerge,
		Identity: *ident,
		Method:   method,
		Preconditions: []taskauthority.DeliveryPrecondition{
			taskauthority.DeliveryPreconditionPRMergeable,
			taskauthority.DeliveryPreconditionPRHeadCurrent,
		},
	}, nil
}

// projectDeliveryIdentity overlays one captured delivery identity onto the
// task .meta projection (pr_* keys). It is a post-commit projection for the
// read-only seams (merge-status, retirement poll, soldier state); the
// canonical delivery authorization/outcome remains the delivery truth and is
// never derived from these keys.
func projectDeliveryIdentity(homeDir, taskID string, ident domain.DeliveryIdentity) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = map[string]string{}
	}
	for k, v := range ident.ToMeta() {
		meta[k] = v
	}
	return home.WriteMeta(homeDir, taskID, meta)
}

func newPRMergeCmd() *cobra.Command {
	var doTeardown bool
	cmd := &cobra.Command{
		Use:   "pr-merge <id> <pr-url> [-- --merge|--rebase]",
		Short: "Merge a PR via the journaled delivery execution",
		Long: `Merge a PR/MR through the journaled delivery execution (fleet.Deliver):
durable journal intent precedes the irreversible provider mutation, the
canonical delivery authorization gates the exact identity against the bound
worktree head, and the truthful closed-set outcome commits canonically.

Default merge method is squash. Use -- --merge or -- --rebase to override.
The --repo/-R flag is not allowed (repository comes from the URL).

PR URL format: https://github.com/<owner>/<repo>/pull/<n>
MR URL format: https://gitlab.com/<owner>/<repo>/-/merge_requests/<n>

Task meta is resolved from the current home first, then each registered
captain home (so general can merge after captain handoff + spawn).

Merge does not remove soldier panes or worktrees. Pass --teardown to run
munsu teardown on the task home after a successful merge (landed cleanup),
resuming retirement only after a completed canonical delivery outcome.`,
		Args: MinimumNArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			prURL := args[1]
			extra := args[2:]

			taskHome, _, err := fleet.RequireShipMeta(ctx.Home, id)
			if err != nil {
				return fmt.Errorf("pr-merge %s: %w", id, err)
			}

			auth, err := ctx.TaskAuthorityFor(taskHome)
			if err != nil {
				return fmt.Errorf("pr-merge %s: composing task authority: %w", id, err)
			}

			if !doTeardown {
				req, err := buildDeliverRequest(auth, id, prURL, extra)
				if err != nil {
					return fmt.Errorf("pr-merge %s: %w", id, err)
				}
				result, err := fleet.Deliver(taskHome, id, req)
				if err != nil {
					return err
				}
				fmt.Print(result.Render())
				// Every non-completed outcome is a non-zero exit for script
				// chaining; the rendered partial-state report is printed first so
				// retryable/partial/remote-unknown detail stays visible.
				if result.IsError() {
					return fmt.Errorf("pr-merge %s: delivery did not complete (status %s)", id, result.Status)
				}
				if perr := projectDeliveryIdentity(taskHome, id, req.Identity); perr != nil {
					return &LifecyclePartialError{TaskID: id, State: "delivered", Cause: perr}
				}
				return nil
			}

			// --teardown: the delivery preparation projection pins the identity
			// for the B thin MergeAndRetire continuation (mirroring the retired
			// pr-check preparation); the canonical authorization remains the
			// delivery truth. MergeAndRetire runs the journaled delivery (or
			// resumes retirement when the outcome is already committed) and
			// retires only after a completed outcome.
			req, err := buildDeliverRequest(auth, id, prURL, extra)
			if err != nil {
				return fmt.Errorf("pr-merge %s: %w", id, err)
			}
			if perr := projectDeliveryIdentity(taskHome, id, req.Identity); perr != nil {
				return fmt.Errorf("pr-merge %s: writing delivery identity projection: %w", id, perr)
			}
			fmt.Printf("Running merge-and-retire for %s in %s...\n", id, taskHome)
			mars := fleet.MergeAndRetire(taskHome, id, prURL, extra, newSessionBoundTeardown(), orchestratorRetirementJournals{}, auth)
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
