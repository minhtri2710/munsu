package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

func newWorktreeCmd() *cobra.Command {
	return newWorktreeCmdWithStatus(backend.StatusWorktrees)
}

func newWorktreeCmdWithStatus(statusWorktrees func(string) ([]backend.WorktreeEntry, error)) *cobra.Command {
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
		Short: "Reclaim worktrees not claimed by active tasks",
		Long: `Reclaim worktrees not referenced by active task metadata or
authoritative task worktree bindings. If task metadata or task authority
cannot be read, the command aborts without reclaiming anything.

Leases should always be returned via "worktree return <path>" when a
soldier finishes. This command is a safety net for orphaned leases. Both
providers are protected against the spawn/reclaim reservation race: the whole
snapshot-to-return pass runs under the home-level worktree-pool fence, which a
launch's lease also takes, so no lease can interleave; and within that pass the
git worktree provider spares a reserved-but-unbound worktree by its
deterministic reservation path, while the treehouse provider spares one whose
"status --json" lease_holder is a live launch reservation. Git protection
assumes the registered project path remains stable for the launch lifetime;
relocation or removal during a launch can leave its originally reserved path
unprotected.`,
		Args: NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			// Hold the worktree-pool fence across the whole snapshot->return
			// pass. A launch takes the same fence around its lease, so no lease
			// can land between this snapshot and the return loop; a slot leased
			// before the snapshot shows its holder here and is spared, and a slot
			// leased after the pass was never a candidate. This closes the
			// spawn/reclaim reservation race a status reread alone cannot.
			poolLock, err := fleet.LockWorktreePool(ctx.Home)
			if err != nil {
				return err
			}
			defer poolLock.Release()

			// Git protection assumes the registered project path remains stable
			// for the launch lifetime; relocation or removal can leave the
			// original reserved path unprotected.
			entries, err := statusWorktrees(ctx.Home)
			if err != nil {
				return fmt.Errorf("getting worktree status: %w", err)
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

			// Spare a reserved-but-unbound worktree from reclaim. Collect the
			// live launch reservations (committed, not yet bound, not terminal)
			// once, then spare by each provider's mechanism.
			reservedUnbound := make(map[string]bool)
			for _, agg := range aggs {
				if agg.Worktree != nil || agg.Launch == nil || agg.Launch.WorktreeReservationID == "" {
					continue
				}
				switch agg.Phase {
				case taskauthority.PhaseDone, taskauthority.PhaseResolved, taskauthority.PhaseRetired:
					continue
				}
				reservedUnbound[agg.Launch.WorktreeReservationID] = true

				// git fallback: the worktree path is a deterministic function of
				// the reservation, so map reservation -> path and spare it.
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

			// treehouse: the reservation is the worktree's lease_holder, so spare
			// any candidate held by a live reservation (git entries carry no
			// holder, so this is a no-op there).
			for _, e := range entries {
				if e.LeaseHolder != "" && reservedUnbound[e.LeaseHolder] {
					active[e.Path] = true
				}
			}

			// Return worktrees not in the active set.
			count := 0
			for _, e := range entries {
				if e.Path == "" || active[e.Path] {
					continue
				}
				fmt.Printf("returning orphaned worktree: %s\n", e.Path)
				if err := backend.ReturnWorktree(ctx.Home, e.Path); err != nil {
					fmt.Fprintf(os.Stderr, "  error: %v\n", err)
				} else {
					count++
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
