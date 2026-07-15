package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/worktree"
	"github.com/spf13/cobra"
)

func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage pooled git worktrees",
	}

	getCmd := &cobra.Command{
		Use:   "get <repo-path>",
		Short: "Acquire a pooled worktree",
		Long:  `Acquire a pooled worktree via treehouse. With --lease, pass through to treehouse for durable holds.`,
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lease, _ := cmd.Flags().GetBool("lease")
			path, err := worktree.Get(args[0], lease)
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		},
	}
	getCmd.Flags().Bool("lease", false, "Acquire a durable lease hold")

	returnCmd := &cobra.Command{
		Use:   "return <path>",
		Short: "Return a worktree to the pool",
		Args:  ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := worktree.Return(args[0]); err != nil {
				return err
			}
			return nil
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show worktree pool status",
		Args:  NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := worktree.Status()
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}

	reclaimCmd := &cobra.Command{
		Use:   "reclaim",
		Short: "Reclaim orphaned worktrees not referenced by any task meta",
		Long: `List all treehouse-visible worktrees and return those not
referenced by any active task meta file. Use after crash recovery or
manual cleanup to release stale leases.

Leases should always be returned via "worktree return <path>" when a
crewmate finishes. This command is a safety net for orphaned leases.`,
		Args: NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			// Get all active worktree paths from task meta
			entries, err := task.ListMeta(homeDir)
			if err != nil {
				return fmt.Errorf("listing task meta: %w", err)
			}
			active := make(map[string]bool)
			for _, e := range entries {
				meta, err := task.ReadMeta(homeDir, e.ID)
				if err != nil {
					continue
				}
				if wt := meta["worktree"]; wt != "" {
					active[wt] = true
				}
			}

			// Get treehouse status and parse worktree list
			out, err := worktree.Status()
			if err != nil {
				return fmt.Errorf("getting treehouse status: %w", err)
			}

			// Return worktrees not in active set
			count := 0
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) == 0 {
					continue
				}
				wtPath := parts[len(parts)-1]
				if !active[wtPath] {
					fmt.Printf("returning orphaned worktree: %s\n", wtPath)
					if err := worktree.Return(wtPath); err != nil {
						fmt.Fprintf(os.Stderr, "  error: %v\n", err)
					} else {
						count++
					}
				}
			}

			fmt.Printf("Reclaimed %d orphaned worktrees\n", count)
			return nil
		},
	}

	cmd.AddCommand(getCmd)
	cmd.AddCommand(returnCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(reclaimCmd)
	return cmd
}
