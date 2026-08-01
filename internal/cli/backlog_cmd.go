package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
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

			if _, err := home.CreateTaskAggregate(ctx.Home, id, "", desc, kind, repo); err != nil {
				return err
			}
			var err error
			if isDefaultHome(ctx.Home) {
				err = fleet.AddItemDispatch(ctx.Home, id, desc, kind, repo, false)
			} else {
				err = fleet.AddItem(ctx.Home, id, desc, kind, repo, false)
			}
			if err != nil {
				return &LifecyclePartialError{TaskID: id, State: "queued", Cause: err}
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
			_, err := home.StartTask(ctx.Home, args[0])
			if err != nil {
				return err
			}
			if err := fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "start", args); err != nil {
				return &LifecyclePartialError{TaskID: args[0], State: "working", Cause: err}
			}
			return nil
		}),
	}
}

func newBacklogDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a backlog item as done",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return runBacklogTransition(ctx.Home, "done", args, fleet.StateDone, "done", "backlog: done")
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
			if by != "" {
				args = append(args, "--by", by)
			}
			detail := "backlog: blocked"
			if by != "" {
				detail += " by " + by
			}
			return runBacklogTransition(ctx.Home, "block", args, fleet.StateBlocked, "blocked", detail)
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
			aggs, err := home.ListCurrentTaskAggregates(ctx.Home)
			if err != nil {
				return err
			}
			rows := make([]BacklogReadinessRow, 0, len(aggs))
			for _, agg := range aggs {
				readiness, err := home.QueryTaskReadiness(ctx.Home, agg.TaskID)
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
			if _, err := home.UnblockTask(ctx.Home, args[0]); err != nil {
				return err
			}
			if err := fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "unblock", args); err != nil {
				return &LifecyclePartialError{TaskID: args[0], State: "queued", Cause: err}
			}
			return nil
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
			_, err := home.ReopenTask(ctx.Home, args[0])
			if err != nil {
				return err
			}
			if err := fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "reopen", args); err != nil {
				return &LifecyclePartialError{TaskID: args[0], State: "queued", Cause: err}
			}
			return nil
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
			if _, err := home.SupersedeTask(ctx.Home, args[0]); err != nil {
				return err
			}
			if err := fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "retry", args); err != nil {
				return &LifecyclePartialError{TaskID: args[0], State: "queued", Cause: err}
			}
			return nil
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

func runBacklogTransition(homeDir, verb string, args []string, to fleet.TaskState, aggregateState, detail string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil
	}
	taskID := args[0]
	item, found, err := fleet.GetItem(homeDir, taskID)
	if err != nil {
		return err
	}
	if found && !item.State.CanTransitionTo(to) {
		return fmt.Errorf("backlog: cannot transition from %s to %s", item.State, to)
	}
	prior, hadAggregate, err := home.ReadCurrentTaskAggregate(homeDir, taskID)
	if err != nil {
		return err
	}
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, taskID, aggregateState, detail); err != nil {
		return err
	}
	if err := fleet.Run(homeDir, isDefaultHome(homeDir), verb, args); err != nil {
		if hadAggregate {
			_ = home.WriteTaskAggregate(homeDir, *prior)
		}
		return err
	}
	return nil
}
