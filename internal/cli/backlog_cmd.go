package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

func newBacklogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "Manage the task backlog",
		Long: `Manage the task backlog via the configured backlog backend.

Subcommands: add, list, show, start, done, block, ready, unblock, reopen, paths.

Uses tasks-axi CLI when available (>= 0.1.1), falling back to
hand-editing $MUNSU_HOME/data/backlog.md.

Use --home to scope backlog data to a specific home directory.
When --home is a non-default path, the manual backend is forced to prevent data leaks.`,
	}

	cmd.AddCommand(newBacklogAddCmd())
	cmd.AddCommand(newBacklogListCmd())
	cmd.AddCommand(newBacklogShowCmd())
	cmd.AddCommand(newBacklogStartCmd())
	cmd.AddCommand(newBacklogDoneCmd())
	cmd.AddCommand(newBacklogBlockCmd())
	cmd.AddCommand(newBacklogReadyCmd())
	cmd.AddCommand(newBacklogUnblockCmd())
	cmd.AddCommand(newBacklogReopenCmd())
	cmd.AddCommand(newBacklogRetryCmd())
	cmd.AddCommand(newBacklogPathsCmd())

	return cmd
}

func newBacklogAddCmd() *cobra.Command {
	var kind string
	var repo string
	var start bool

	cmd := &cobra.Command{
		Use:   `add <id> "<description>"`,
		Short: "Add a task to the backlog",
		Long: `Add a task to the backlog.

The description must be quoted if it contains multiple words.
Example:
  munsu backlog add flow-r2 "Flow retest scout"
`,
		Args: ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := refuseCaptainBacklogMutation(); err != nil {
				return err
			}
			if start {
				command := fmt.Sprintf("munsu backlog add %s %s", shellQuote(args[0]), shellQuote(args[1]))
				if kind != "" {
					command += " --kind " + shellQuote(kind)
				}
				if repo != "" {
					command += " --repo " + shellQuote(repo)
				}
				command += " && munsu backlog start " + shellQuote(args[0])
				return exactCommandCorrection("unsupported_input", command, "backlog add always queues tasks; use the exact command in error.action to add, then start")
			}
			id := args[0]
			desc := args[1]

			auth, err := ctx.TaskAuthority()
			if err != nil {
				return err
			}
			tid, err := domain.NewTaskID(id)
			if err != nil {
				return err
			}
			req := taskauthority.CanonicalCreateRequest{
				HomeID:      auth.HomeID(),
				TaskID:      tid,
				Owner:       resolveTaskOwner(ctx.Home),
				Description: desc,
				Kind:        kind,
				Reason:      "cli backlog add",
			}
			if repo != "" {
				pid, err := domain.NewProjectID(repo)
				if err != nil {
					return err
				}
				req.Project = pid
			}
			op, err := newCanonicalOperation("backlog-add", req)
			if err != nil {
				return err
			}
			if _, err := auth.Create(op, req); err != nil {
				return err
			}
			// The backlog file is a post-commit projection: a projection
			// failure must not roll back the authoritative Task Generation
			// (ADR-0007 §7).
			var projectionErr error
			if isDefaultHome(ctx.Home) {
				projectionErr = fleet.AddItemDispatch(ctx.Home, id, desc, kind, repo, false)
			} else {
				projectionErr = fleet.AddItem(ctx.Home, id, desc, kind, repo, false)
			}
			if projectionErr != nil {
				return &LifecyclePartialError{TaskID: id, State: "queued", Cause: projectionErr}
			}
			return nil
		}),
	}

	cmd.Flags().StringVar(&kind, "kind", "ship", "Task kind (ship|scout|task)")
	cmd.Flags().StringVar(&repo, "repo", "", "Project repository name")
	cmd.Flags().BoolVar(&start, "start", false, "Deprecated: use `backlog start <id>` after adding")
	_ = cmd.Flags().MarkHidden("start")

	return cmd
}

func newBacklogListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [state-filter]",
		Short: "List backlog items",
		Args:  MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "list", args)
		}),
	}
}

func newBacklogShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show backlog item details",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "show", args)
		}),
	}
}

func newBacklogStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <id>",
		Short: "Start a backlog item (mark in-flight)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			// Supervision gate: start fails closed when the watcher lease is
			// degraded, before any Task Authority call (Task 4.3, ADR-0007 §8).
			if err := fleet.CheckSupervisionForDispatch(ctx.Home, home.DispatchActionStart); err != nil {
				return err
			}
			return runAuthorityLifecycleTransition(ctx, "start", args, "working", func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				req := taskauthority.CanonicalStartRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Reason:       "backlog: start",
				}
				op, err := newCanonicalOperation("backlog-start", req)
				if err != nil {
					return err
				}
				_, err = auth.Start(op, req)
				return err
			})
		}),
	}
}

func newBacklogDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a backlog item as done",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return runAuthorityLifecycleTransition(ctx, "done", args, "done", func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				req := taskauthority.CanonicalCompleteRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					To:           taskauthority.PhaseDone,
					Reason:       "backlog: done",
				}
				op, err := newCanonicalOperation("backlog-done", req)
				if err != nil {
					return err
				}
				_, err = auth.Complete(op, req)
				return err
			})
		}),
	}
}
func newBacklogBlockCmd() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "block <id> [--by <dependency-id>]",
		Short: "Block a backlog item",
		Long: `Mark a backlog item as blocked.
When using tasks-axi backend, --by specifies the dependency that blocks this item.
When --by is omitted, falls back to manual backend.`,

		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			detail := "backlog: blocked"
			if by != "" {
				detail += " by " + by
				args = append(args, "--by", by)
			}
			return runAuthorityLifecycleTransition(ctx, "block", args, "blocked", func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				req := taskauthority.CanonicalBlockRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Detail:       detail,
					Reason:       "backlog: block",
				}
				op, err := newCanonicalOperation("backlog-block", req)
				if err != nil {
					return err
				}
				_, err = auth.Block(op, req)
				return err
			})
		}),
	}
	cmd.Flags().StringVar(&by, "by", "", "Dependency that blocks this item (required for tasks-axi backend)")
	return cmd
}

func newBacklogReadyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ready [id]",
		Short: "Query backlog readiness without mutation",
		Args:  MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if len(args) == 1 {
				return exactCommandCorrection("unsupported_input", "munsu backlog unblock "+shellQuote(args[0]), "backlog ready is query-only; use the exact command in error.action to clear a blocker")
			}
			auth, err := ctx.TaskAuthority()
			if err != nil {
				return err
			}
			aggs, err := auth.List()
			if err != nil {
				return err
			}
			rows := make([]BacklogReadinessRow, 0, len(aggs))
			for _, agg := range aggs {
				tid, err := domain.NewTaskID(agg.TaskID)
				if err != nil {
					return err
				}
				readiness, err := auth.Readiness(tid)
				if err != nil {
					return err
				}
				rows = append(rows, backlogReadinessRow(readiness))
			}
			return writeContract(cmd, Response[[]BacklogReadinessRow]{
				SchemaVersion: SchemaVersion,
				Kind:          "backlog.ready",
				Status:        "success",
				Data:          rows,
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newBacklogUnblockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unblock <id>",
		Short: "Unblock a blocked backlog item",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := refuseCaptainBacklogMutation(); err != nil {
				return err
			}
			return runAuthorityLifecycleTransition(ctx, "unblock", args, "queued", func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				req := taskauthority.CanonicalUnblockRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Reason:       "backlog: unblock",
				}
				op, err := newCanonicalOperation("backlog-unblock", req)
				if err != nil {
					return err
				}
				_, err = auth.Unblock(op, req)
				return err
			})
		}),
	}
}

func newBacklogReopenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <id>",
		Short: "Reopen a terminal task as a new generation",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := refuseCaptainBacklogMutation(); err != nil {
				return err
			}
			return runAuthorityLifecycleTransition(ctx, "reopen", args, "queued", func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				req := taskauthority.CanonicalReopenRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Reason:       "backlog: reopen",
				}
				op, err := newCanonicalOperation("backlog-reopen", req)
				if err != nil {
					return err
				}
				_, err = auth.Reopen(op, req)
				return err
			})
		}),
	}
}

func newBacklogRetryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry <id>",
		Short: "Supersede a failed/terminal generation as a new queued generation",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := refuseCaptainBacklogMutation(); err != nil {
				return err
			}
			return runAuthorityLifecycleTransition(ctx, "retry", args, "queued", func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error {
				// Retry is the canonical Reopen operation: a terminal generation
				// is superseded as a fresh queued Generation at Revision one and
				// the prior generation is preserved as historical state. Live
				// generations are refused so a retry never claims running work.
				req := taskauthority.CanonicalReopenRequest{
					HomeID:       auth.HomeID(),
					TaskID:       tid,
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Reason:       "backlog: retry",
				}
				op, err := newCanonicalOperation("backlog-retry", req)
				if err != nil {
					return err
				}
				_, err = auth.Reopen(op, req)
				return err
			})
		}),
	}
}

func newBacklogPathsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "paths",
		Short: "Show separate development and runtime backlog paths",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, _ []string, ctx Ctx) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolving working directory: %w", err)
			}
			paths, err := fleet.ResolvePaths(cwd, ctx.Home)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "development: %s\nruntime: %s\n", paths.Development, paths.Runtime)
			if paths.Config != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "config: %s\n", paths.Config)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "config: (none; development defaults to cwd/backlog.md)")
			}
			return nil
		}),
	}
}

// refuseCaptainBacklogMutation returns an error when run inside a captain context.
// The Captain must not add, ready, or unblock backlog items without General instruction.
func refuseCaptainBacklogMutation() error {
	if os.Getenv("MUNSU_ROLE") == "captain" {
		return fmt.Errorf("captain backlog authority: captains may not modify the backlog without General instruction; use 'munsu send captain:<id> <task-add|unblock|ready> ...' from the General home")
	}
	return nil
}

func runAuthorityLifecycleTransition(ctx Ctx, verb string, args []string, projectionState string, op func(auth *taskauthority.Canonical, tid domain.TaskID, agg taskauthority.Aggregate) error) error {
	taskID := args[0]
	auth, err := ctx.TaskAuthority()
	if err != nil {
		return err
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return err
	}
	agg, err := auth.Get(tid)
	if err != nil {
		return err
	}
	if err := op(auth, tid, agg); err != nil {
		return err
	}
	// The backlog file is a post-commit projection: a projection failure must
	// not roll back the authoritative lifecycle transition (ADR-0007 §7). The
	// projection can be retried independently; the authoritative operation is
	// never replayed.
	if err := fleet.Run(ctx.Home, isDefaultHome(ctx.Home), verb, args); err != nil {
		return &LifecyclePartialError{TaskID: taskID, State: projectionState, Cause: err}
	}
	return nil
}
