package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

func newWorktreeCmd() *cobra.Command {
	return newWorktreeCmdWithStatus(backend.WorktreeStatus)
}

func newWorktreeCmdWithStatus(status func(string) (string, error)) *cobra.Command {
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
			out, err := status(ctx.Home)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		}),
	}

	reclaimCmd := &cobra.Command{
		Use:   "reclaim",
		Short: "Reclaim worktrees not claimed by active tasks",
		Long: `Reclaim worktrees not referenced by active task metadata or
authoritative task worktree bindings. If task metadata or task authority
cannot be read, the command aborts without reclaiming anything.

Leases should always be returned via "worktree return <path>" when a
soldier finishes. This command is a safety net for orphaned leases. The git
worktree provider is protected against the spawn/reclaim reservation race; the
treehouse provider is not, because it exposes no reservation-keyed or holder
status query, so reclaim cannot distinguish a reserved-but-unbound treehouse
worktree from an orphan. Git protection assumes the registered project path
remains stable for the launch lifetime; relocation or removal during a launch
can leave its originally reserved path unprotected.`,
		Args: NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			// Snapshot candidates before reading authority: a git launch commits
			// its reservation before creating the worktree, so later authority
			// reads see reservations for every candidate in this snapshot. Git
			// protection assumes the registered project path remains stable for
			// the launch lifetime; relocation or removal can leave the original
			// reserved path unprotected.
			out, err := status(ctx.Home)
			if err != nil {
				return fmt.Errorf("getting treehouse status: %w", err)
			}

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

			// Only git can spare reserved-but-unbound paths: treehouse has no
			// reservation-keyed or holder status query.
			for _, agg := range aggs {
				if agg.Worktree != nil || agg.Launch == nil || agg.Launch.WorktreeReservationID == "" {
					continue
				}
				switch agg.Phase {
				case taskauthority.PhaseDone, taskauthority.PhaseResolved, taskauthority.PhaseRetired:
					continue
				}
				repoPath, rerr := fleet.ResolveRepoPath(ctx.Home, agg.Launch.Project)
				if rerr != nil || repoPath == "" {
					continue
				}
				path, ok, perr := backend.ReservedWorktreePath(ctx.Home, repoPath, agg.Launch.WorktreeReservationID)
				if perr != nil || !ok || path == "" {
					continue
				}
				active[path] = true
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
