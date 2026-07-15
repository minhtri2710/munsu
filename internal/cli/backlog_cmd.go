package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/backlog"
	"github.com/minhtri2710/munsu/internal/home"
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
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			desc := args[1]

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}

			if err := backlog.AddItemDispatch(homeDir, id, desc, kind, repo, start); err != nil {
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
				if err := task.WriteMeta(homeDir, id, meta); err != nil {
					// Non-fatal: log but don't fail the backlog add
					fmt.Fprintf(os.Stderr, "warning: writing task meta for %s: %v\n", id, err)
				}
			}
			return nil

		},
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
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "list", args)
			}
			return backlog.RunManual(homeDir, "list", args)
		},
	}
}

func newBacklogShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show backlog item details",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "show", args)
			}
			return backlog.RunManual(homeDir, "show", args)
		},
	}
}

func newBacklogStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <id>",
		Short: "Start a backlog item (mark in-flight)",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "start", args)
			}
			return backlog.RunManual(homeDir, "start", args)
		},
	}
}

func newBacklogDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a backlog item as done",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "done", args)
			}
			return backlog.RunManual(homeDir, "done", args)
		},
	}
}

func newBacklogBlockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "block <id>",
		Short: "Block a backlog item",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "block", args)
			}
			return backlog.RunManual(homeDir, "block", args)
		},
	}
}

func newBacklogReadyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ready <id>",
		Short: "Unblock a backlog item (mark ready)",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "ready", args)
			}
			return backlog.RunManual(homeDir, "ready", args)
		},
	}
}

func newBacklogUnblockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unblock <id>",
		Short: "Alias for ready (unblock a backlog item)",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if isDefaultHome(homeDir) {
				return backlog.Run(homeDir, "unblock", args)
			}
			return backlog.RunManual(homeDir, "unblock", args)
		},
	}
}
