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
	cmd.AddCommand(newRecordVerdictCmd())
	return cmd
}

func newRecordVerdictCmd() *cobra.Command {
	var reviewerTask string
	cmd := &cobra.Command{
		Use:   "record-verdict --reviewer-task <review-task-id>",
		Short: "Record the verdict file of a review task",
		Long: `Record the verdict a review task wrote to its verdict file (ADR-0025).
This is the manual entry to the record step the supervision watcher runs when
the file appears: it reads the review task's current verdict file, refuses a
file that is malformed or does not speak for that task, generation and reviewed
head, observes the reviewed worktree, and records the verdict on the reviewed
task through the canonical task authority. Delivery authorization requires a
PASS verdict for exactly the head it delivers, from a review task other than the
reviewed task, over a tree that did not move during the review. A later verdict
replaces the earlier one.`,
		Args: ExactArgs(0),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			taskHome, _, err := fleet.ResolveTaskHome(ctx.Home, reviewerTask)
			if err != nil {
				return fmt.Errorf("record-verdict %s: %w", reviewerTask, err)
			}
			summary, err := fleet.RecordReviewVerdict(taskHome, reviewerTask, 0, "")
			if err != nil {
				return fmt.Errorf("record-verdict %s: %w", reviewerTask, err)
			}
			fmt.Println(summary)
			return nil
		}),
	}
	cmd.Flags().StringVar(&reviewerTask, "reviewer-task", "", "the review task whose verdict file to record")
	_ = cmd.MarkFlagRequired("reviewer-task")
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
// from the retained read-only provider snapshot seam; the verdict head must
// equal it (ReviewVerdict.Approves) and Fleet refuses at delivery when git HEAD
// at the bound worktree differs from it, so a stale identity fails closed
// before any mutation.
func buildDeliverRequest(auth *taskauthority.Canonical, taskID, prURL string, extra []string, words domain.Words) (fleet.DeliverRequest, error) {
	if err := words.Validate(); err != nil {
		return fleet.DeliverRequest{}, err
	}
	provider, owner, repo, num, _, err := domain.ParseProviderURL(prURL)
	if err != nil {
		return fleet.DeliverRequest{}, err
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return fleet.DeliverRequest{}, err
	}
	agg, err := auth.Get(tid)
	if err != nil {
		return fleet.DeliverRequest{}, fmt.Errorf("resolving task %s: %w", taskID, err)
	}
	if agg.DeliveryContract == nil {
		return fleet.DeliverRequest{}, fmt.Errorf("task %s has no captured delivery contract; spawn it before delivery", taskID)
	}
	snap, err := fleet.FetchProviderSnapshot(agg.DeliveryContract.Forge, prURL)
	if err != nil {
		return fleet.DeliverRequest{}, fmt.Errorf("capturing delivery identity: %w", err)
	}
	if agg.Worktree == nil {
		return fleet.DeliverRequest{}, fmt.Errorf("task %s has no bound worktree; spawn it before delivery", taskID)
	}
	switch strings.ToUpper(snap.State) {
	case "OPEN":
		if !snap.Mergeable() {
			return fleet.DeliverRequest{}, fmt.Errorf("delivery provider state is not mergeable: open with passing checks and no provider review requesting changes are required")
		}
	case "MERGED", "CLOSED":
	default:
		return fleet.DeliverRequest{}, fmt.Errorf("delivery provider state is unknown: %q", snap.State)
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
		Words:    words,
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
	return home.UpdateMeta(homeDir, taskID, func(meta map[string]string) error {
		for k, v := range ident.ToMeta() {
			meta[k] = v
		}
		return nil
	})
}

func newPRMergeCmd() *cobra.Command {
	var doTeardown bool
	var words domain.Words
	cmd := &cobra.Command{
		Use:   "pr-merge <id> <pr-url> [-- --merge|--rebase]",
		Short: "Merge a PR via the journaled delivery execution",
		Long: `Merge a PR/MR through the journaled delivery execution (fleet.Deliver):
durable journal intent precedes the irreversible provider mutation, the
canonical delivery authorization gates the exact identity against the verdict
head and the head git reads at the bound worktree now, and the truthful closed-set outcome commits canonically.

Delivery requires a PASS review verdict for the exact head (recorded from a review
task by record-verdict) and
the Human's words (--grantor, --channel, --quote); each external write is preceded
by a gate record carrying the head and those words.

Default merge method is squash. Use -- --merge or -- --rebase to override.
The --repo/-R flag is not allowed (repository comes from the URL).

PR URL format: https://github.com/<owner>/<repo>/pull/<n> (merged with
gh pr merge --match-head-commit, only when every required check reported and passed)
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
				req, err := buildDeliverRequest(auth, id, prURL, extra, words)
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
			req, err := buildDeliverRequest(auth, id, prURL, extra, words)
			if err != nil {
				return fmt.Errorf("pr-merge %s: %w", id, err)
			}
			if perr := projectDeliveryIdentity(taskHome, id, req.Identity); perr != nil {
				return fmt.Errorf("pr-merge %s: writing delivery identity projection: %w", id, perr)
			}
			fmt.Printf("Running merge-and-retire for %s in %s...\n", id, taskHome)
			mars := fleet.MergeAndRetire(taskHome, id, prURL, extra, words, newSessionBoundTeardown(), orchestratorRetirementJournals{}, auth)
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
	cmd.Flags().StringVar(&words.Grantor, "grantor", "", "who gave the delivery words (required)")
	cmd.Flags().StringVar(&words.Channel, "channel", "", "the channel the words came through (required)")
	cmd.Flags().StringVar(&words.Quote, "quote", "", "the Human's verbatim words permitting this delivery (required)")
	cmd.Flags().BoolVar(&doTeardown, "teardown", false, "after successful merge, teardown the soldier (pane+worktree+meta) in the task home")
	return cmd
}
