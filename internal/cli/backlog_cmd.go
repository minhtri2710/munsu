package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/backlog"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/spf13/cobra"
)

func newBacklogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "Manage the task backlog",
		Long: `Manage the task backlog via the configured backlog backend.

Subcommands: add, list, show, start, done, block, ready, unblock.

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
			id := args[0]
			desc := args[1]

			if err := backlog.AddItemDispatch(ctx.Home, id, desc, kind, repo, start); err != nil {
				return err
			}
			// When --start is set, also register the task meta so it appears in fleet state.
			if start {
				meta := map[string]string{
					"description": desc,
					"kind":        kind,
				}
				if repo != "" {
					meta["repo"] = repo
					meta["project"] = repo
				}
				if err := task.WriteMeta(ctx.Home, id, meta); err != nil {
					// Non-fatal: log but don't fail the backlog add
					fmt.Fprintf(os.Stderr, "warning: writing task meta for %s: %v\n", id, err)
				}
			}
			return nil
		}),
	}

	cmd.Flags().StringVar(&kind, "kind", "ship", "Task kind (ship|scout|task)")
	cmd.Flags().StringVar(&repo, "repo", "", "Project repository name")
	cmd.Flags().BoolVar(&start, "start", false, "Start task immediately (set state to in-flight)")

	return cmd
}

func newBacklogListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [state-filter]",
		Short: "List backlog items",
		Args:  MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if isDefaultHome(ctx.Home) {
				return backlog.Run(ctx.Home, "list", args)
			}
			return backlog.RunManual(ctx.Home, "list", args)
		}),
	}
}

func newBacklogShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show backlog item details",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if isDefaultHome(ctx.Home) {
				return backlog.Run(ctx.Home, "show", args)
			}
			return backlog.RunManual(ctx.Home, "show", args)
		}),
	}
}

func newBacklogStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <id>",
		Short: "Start a backlog item (mark in-flight)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if isDefaultHome(ctx.Home) {
				return backlog.Run(ctx.Home, "start", args)
			}
			return backlog.RunManual(ctx.Home, "start", args)
		}),
	}
}

func newBacklogDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a backlog item as done",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if isDefaultHome(ctx.Home) {
				return backlog.Run(ctx.Home, "done", args)
			}
			return backlog.RunManual(ctx.Home, "done", args)
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

		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if by != "" && isDefaultHome(ctx.Home) {
				return backlog.Run(ctx.Home, "block", []string{args[0], "--by", by})
			}
			return backlog.RunManual(ctx.Home, "block", args)
		}),
	}
	cmd.Flags().StringVar(&by, "by", "", "Dependency that blocks this item (required for tasks-axi backend)")
	return cmd
}

func newBacklogReadyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ready <id>",
		Short: "Unblock a backlog item (mark ready)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if isDefaultHome(ctx.Home) {
				return backlog.Run(ctx.Home, "ready", args)
			}
			return backlog.RunManual(ctx.Home, "ready", args)
		}),
	}
}

func newBacklogUnblockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unblock <id>",
		Short: "Alias for ready (unblock a backlog item)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if isDefaultHome(ctx.Home) {
				return backlog.Run(ctx.Home, "unblock", args)
			}
			return backlog.RunManual(ctx.Home, "unblock", args)
		}),
	}
}
