package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/home"
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
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			lease, _ := cmd.Flags().GetBool("lease")
			path, err := backend.GetWorktree(ctx.Home, args[0], lease)
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		}),
	}
	getCmd.Flags().Bool("lease", false, "Acquire a durable lease hold")

	returnCmd := &cobra.Command{
		Use:   "return <path>",
		Short: "Return a worktree to the pool",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := backend.ReturnWorktree(ctx.Home, args[0]); err != nil {
				return err
			}
			return nil
		}),
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show worktree pool status",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			out, err := backend.WorktreeStatus(ctx.Home)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		}),
	}

	reclaimCmd := &cobra.Command{
		Use:   "reclaim",
		Short: "Reclaim orphaned worktrees not referenced by any task meta",
		Long: `List all treehouse-visible worktrees and return those not
referenced by any active task meta file. Use after crash recovery or
manual cleanup to release stale leases.

Leases should always be returned via "worktree return <path>" when a
soldier finishes. This command is a safety net for orphaned leases.`,
		Args: NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			// Get all active worktree paths from task meta. Enumerate the meta
			// ids directly rather than through ListMeta, whose display-tolerant
			// read silently drops an unreadable projection: for a destructive
			// command an unreadable .meta is not evidence that the task holds no
			// worktree, so refuse rather than skip. Otherwise a live worktree
			// behind an unreadable .meta would be classified orphaned and
			// destroyed.
			ids, err := home.ListMetaIDs(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing task meta: %w", err)
			}
			active := make(map[string]bool)
			for _, id := range ids {
				meta, err := home.ReadMeta(ctx.Home, id)
				if err != nil {
					return fmt.Errorf("reading task meta %q: %w", id, err)
				}
				if wt := meta["worktree"]; wt != "" {
					active[wt] = true
				}
			}

			auth, err := taskAuthorityForRead(ctx.Home)
			if err != nil {
				return fmt.Errorf("reading task authority: %w", err)
			}
			aggs, err := auth.List()
			if err != nil {
				return fmt.Errorf("listing task authority: %w", err)
			}
			for _, agg := range aggs {
				if agg.Worktree != nil && agg.Worktree.Path != "" {
					active[agg.Worktree.Path] = true
				}
			}

			// Get treehouse status and parse worktree list
			out, err := backend.WorktreeStatus(ctx.Home)
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
					if err := backend.ReturnWorktree(ctx.Home, wtPath); err != nil {
						fmt.Fprintf(os.Stderr, "  error: %v\n", err)
					} else {
						count++
					}
				}
			}

			fmt.Printf("Reclaimed %d orphaned worktrees\n", count)
			return nil
		}),
	}

	cmd.AddCommand(getCmd)
	cmd.AddCommand(returnCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(reclaimCmd)
	return cmd
}
