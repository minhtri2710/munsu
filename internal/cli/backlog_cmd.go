package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

func newBacklogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "Manage the task backlog",
		Long: `Manage the task backlog via the configured backlog backend.

Subcommands: add, list, show, start, done, block, ready, unblock, paths.

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
			id := args[0]
			desc := args[1]

			var err error
			if isDefaultHome(ctx.Home) {
				err = fleet.AddItemDispatch(ctx.Home, id, desc, kind, repo, start)
			} else {
				err = fleet.AddItem(ctx.Home, id, desc, kind, repo, start)
			}
			if err != nil {
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
				if err := home.WriteMeta(ctx.Home, id, meta); err != nil {
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
			return fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "start", args)
		}),
	}
}

func newBacklogDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a backlog item as done",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "done", args)
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
			return fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "block", args)
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
			if err := refuseCaptainBacklogMutation(); err != nil {
				return err
			}
			return fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "ready", args)
		}),
	}
}

func newBacklogUnblockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unblock <id>",
		Short: "Alias for ready (unblock a backlog item)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := refuseCaptainBacklogMutation(); err != nil {
				return err
			}
			return fleet.Run(ctx.Home, isDefaultHome(ctx.Home), "unblock", args)
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
