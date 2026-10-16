package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

func newWorktreeCmd() *cobra.Command {
	return newWorktreeCmdWithStatus(backend.StatusWorktrees)
}

func newWorktreeCmdWithStatus(statusWorktrees func(homeDir, treehouse string) ([]backend.WorktreeEntry, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage task worktrees",
		Long: `Manage task worktrees. The provider is git worktree unless --project names a
project whose worktree tool entry configures treehouse. Reclaim without
--project walks every provider the home's projects use.`,
	}
	cmd.PersistentFlags().String("project", "", "Use this project's configured worktree tool")

	getCmd := &cobra.Command{
		Use:   "get <repo-path>",
		Short: "Acquire a worktree",
		Long:  `Acquire a worktree. With treehouse, --lease passes through for a durable hold.`,
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			lease, _ := cmd.Flags().GetBool("lease")
			tool, err := worktreeTool(cmd, ctx.Home)
			if err != nil {
				return err
			}
			path, err := backend.GetWorktree(ctx.Home, tool, args[0], lease)
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
			tool, err := worktreeTool(cmd, ctx.Home)
			if err != nil {
				return err
			}
			poolLock, err := fleet.LockWorktreePool(ctx.Home)
			if err != nil {
				return err
			}
			defer poolLock.Release()

			entries, err := statusWorktrees(ctx.Home, tool)
			if err != nil {
				return fmt.Errorf("getting worktree status: %w", err)
			}
			active, _, err := activeWorktreeClaims(ctx.Home, entries)
			if err != nil {
				return err
			}
			if active[worktreeClaimKey(args[0])] {
				return fmt.Errorf("refusing to return claimed worktree %q; task-owned worktrees are released only by canonical retirement", args[0])
			}
			entry, err := reconciledOrphanWorktree(args[0], entries)
			if err != nil {
				return err
			}
			if err := verifyManualWorktreeRelease(entry.Path); err != nil {
				return err
			}
			return backend.ReturnWorktree(ctx.Home, tool, entry.Path)
		}),
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show worktree pool status",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			tool, err := worktreeTool(cmd, ctx.Home)
			if err != nil {
				return err
			}
			out, err := backend.WorktreeStatus(ctx.Home, tool)
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
		Long: `Reclaim worktrees not referenced by canonical task bindings. If task
authority cannot be read, the command aborts without reclaiming anything.

This command releases only provider-listed worktrees with no task, retirement,
or launch-reservation claim. A reported lease holder that cannot be reconciled
to canonical authority refuses the whole reclaim pass; nothing is returned.
It returns only worktrees with no known
launch artifacts and no dirty, untracked, or ignored content. The snapshot-to-
return pass holds the worktree-pool fence used by acquisition.`,
		Args: NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			// Hold the worktree-pool fence across the whole snapshot->return
			// pass. A launch takes the same fence around its lease, so no lease
			// can land between this snapshot and the return loop; a slot leased
			// before the snapshot shows its holder here and is spared, and a slot
			// leased after the pass was never a candidate. This closes the
			// spawn/reclaim reservation race a status reread alone cannot.
			tools, err := reclaimWorktreeTools(cmd, ctx.Home)
			if err != nil {
				return err
			}
			poolLock, err := fleet.LockWorktreePool(ctx.Home)
			if err != nil {
				return err
			}
			defer poolLock.Release()

			type toolEntry struct {
				tool  string
				entry backend.WorktreeEntry
			}
			var listed []toolEntry
			var entries []backend.WorktreeEntry
			for _, tool := range tools {
				got, err := statusWorktrees(ctx.Home, tool)
				if err != nil {
					return fmt.Errorf("getting worktree status: %w", err)
				}
				for _, entry := range got {
					listed = append(listed, toolEntry{tool, entry})
				}
				entries = append(entries, got...)
			}
			active, released, err := activeWorktreeClaims(ctx.Home, entries)
			if err != nil {
				return err
			}

			// Preflight every unowned candidate before any provider return so a
			// known unsafe candidate cannot follow an earlier destructive release.
			var candidates []toolEntry
			for _, c := range listed {
				entry := c.entry
				if active[worktreeClaimKey(entry.Path)] {
					continue
				}
				if entry.LeaseHolder != "" && !released[entry.LeaseHolder] {
					return fmt.Errorf("worktree %s lease holder %q is not canonically reconciled; refusing reclaim", entry.Path, entry.LeaseHolder)
				}
				if err := verifyManualWorktreeRelease(entry.Path); err != nil {
					return fmt.Errorf("worktree %s is not safe to reclaim: %w", entry.Path, err)
				}
				candidates = append(candidates, c)
			}

			for i, c := range candidates {
				fmt.Printf("returning orphaned worktree: %s\n", c.entry.Path)
				if err := backend.ReturnWorktree(ctx.Home, c.tool, c.entry.Path); err != nil {
					return fmt.Errorf("reclaim return outcome uncertain for %s: %w", c.entry.Path, err)
				}
				fmt.Printf("Reclaimed %d orphaned worktrees\n", i+1)
			}
			if len(candidates) == 0 {
				fmt.Println("Reclaimed 0 orphaned worktrees")
			}
			return nil
		}),
	}

	cmd.AddCommand(getCmd)
	cmd.AddCommand(returnCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(reclaimCmd)
	return cmd
}

// reclaimWorktreeTools is the --project worktree tool, or without it every
// provider the home's projects use.
func reclaimWorktreeTools(cmd *cobra.Command, home string) ([]string, error) {
	if project, _ := cmd.Flags().GetString("project"); project != "" {
		tool, err := fleet.ProjectWorktreeTool(home, project)
		if err != nil {
			return nil, err
		}
		return []string{tool}, nil
	}
	return fleet.HomeWorktreeTools(home)
}

// worktreeTool resolves the --project worktree tool, or "" for git worktree.
func worktreeTool(cmd *cobra.Command, home string) (string, error) {
	project, _ := cmd.Flags().GetString("project")
	if project == "" {
		return "", nil
	}
	return fleet.ProjectWorktreeTool(home, project)
}

func reconciledOrphanWorktree(path string, entries []backend.WorktreeEntry) (backend.WorktreeEntry, error) {
	key := worktreeClaimKey(path)
	var match *backend.WorktreeEntry
	for _, entry := range entries {
		if entry.Path == "" {
			return backend.WorktreeEntry{}, fmt.Errorf("provider status contains an empty worktree path; refusing return")
		}
		if worktreeClaimKey(entry.Path) != key {
			continue
		}
		if match != nil {
			return backend.WorktreeEntry{}, fmt.Errorf("provider status lists worktree %q more than once; refusing return", path)
		}
		match = &entry
	}
	if match == nil {
		return backend.WorktreeEntry{}, fmt.Errorf("worktree ownership is not established by provider status; refusing return %q", path)
	}
	if match.LeaseHolder != "" {
		return backend.WorktreeEntry{}, fmt.Errorf("worktree lease holder %q is not reconciled; refusing return", match.LeaseHolder)
	}
	return *match, nil
}

func verifyManualWorktreeRelease(worktreePath string) error {
	if !filepath.IsAbs(worktreePath) {
		return fmt.Errorf("worktree path must be absolute for ownership verification: %q", worktreePath)
	}
	gitMarker, err := os.Lstat(filepath.Join(worktreePath, ".git"))
	if err != nil {
		return fmt.Errorf("verifying worktree identity: %w", err)
	}
	if !gitMarker.Mode().IsRegular() {
		return fmt.Errorf("worktree .git marker is not a regular file; refusing return")
	}
	known := map[string]bool{fleet.ManifestName: true}
	for _, name := range fleet.CoreLaunchArtifactNames {
		known[filepath.FromSlash(name)] = true
	}
	for _, adapter := range harness.Adapters {
		for _, name := range adapter.SoldierLaunch.WorktreeFiles {
			known[filepath.FromSlash(name)] = true
		}
	}
	for name := range known {
		if _, err := os.Lstat(filepath.Join(worktreePath, name)); err == nil {
			return fmt.Errorf("known or unanchored launch artifact %q is present; refusing return", filepath.ToSlash(name))
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking launch artifact %q: %w", filepath.ToSlash(name), err)
		}
	}
	entries, err := os.ReadDir(worktreePath)
	if err != nil {
		return fmt.Errorf("checking worktree contents: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".soldier-launch-guard-") || known[name] {
			return fmt.Errorf("known or unanchored launch artifact %q is present; refusing return", name)
		}
	}
	cmd := exec.Command("git", "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching", "-z")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("checking worktree cleanliness: %w", err)
	}
	if len(out) != 0 {
		return fmt.Errorf("worktree contains dirty, untracked, or ignored content; refusing return")
	}
	return nil
}

// activeWorktreeClaims returns the worktree paths held by a task, retirement, or
// live launch reservation, and the reservation IDs released by canonical
// retirement. A reservation ID live in any scanned home is never released.
// Words: Human, typed, 2026-10-16: "các task cần lời của tôi thì hỏi concil rồi quyết" (council: re-home onto canonical bindings, fail closed)
func activeWorktreeClaims(homeDir string, entries []backend.WorktreeEntry) (map[string]bool, map[string]bool, error) {
	active := make(map[string]bool)
	reservedLaunches := make(map[string]bool)
	releasedLaunches := make(map[string]bool)
	addHome := func(dir string) error {
		auth, err := taskAuthorityForRead(dir)
		if err != nil {
			return fmt.Errorf("reading task authority in %s: %w", dir, err)
		}
		aggs, err := auth.List()
		if err != nil {
			return fmt.Errorf("listing task authority in %s: %w", dir, err)
		}
		for _, agg := range aggs {
			if agg.Worktree != nil && agg.Worktree.Path != "" {
				active[worktreeClaimKey(agg.Worktree.Path)] = true
			}
			if agg.Retirement != nil && agg.Retirement.Worktree != nil && agg.Retirement.Worktree.Path != "" && (agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupCompleted || agg.Launch == nil || agg.Retirement.Worktree.LeaseID != agg.Launch.WorktreeReservationID) {
				active[worktreeClaimKey(agg.Retirement.Worktree.Path)] = true
			}
		}
		for _, agg := range aggs {
			if agg.Launch == nil || agg.Launch.WorktreeReservationID == "" || hasCanonicalRetirementForReservation(agg, agg.Launch.WorktreeReservationID) {
				continue
			}
			if retiredUnboundReservation(agg) {
				releasedLaunches[agg.Launch.WorktreeReservationID] = true
				continue
			}
			reservedLaunches[agg.Launch.WorktreeReservationID] = true
			if agg.Launch.Project == "" {
				return fmt.Errorf("launch reservation %q has no canonical project identity", agg.Launch.WorktreeReservationID)
			}
			repoPath, rerr := fleet.ResolveRepoPath(dir, agg.Launch.Project)
			if rerr != nil {
				return fmt.Errorf("resolving project for live worktree reservation %q: %w", agg.Launch.WorktreeReservationID, rerr)
			}
			path, perr := backend.ReservedGitWorktreePath(dir, repoPath, agg.Launch.WorktreeReservationID)
			if perr != nil {
				return fmt.Errorf("deriving worktree for live reservation %q: %w", agg.Launch.WorktreeReservationID, perr)
			}
			active[worktreeClaimKey(path)] = true
		}
		return nil
	}
	if err := addHome(homeDir); err != nil {
		return nil, nil, err
	}
	captains, err := fleet.ListCaptains(homeDir)
	if err != nil {
		return nil, nil, fmt.Errorf("listing captain homes: %w", err)
	}
	for _, captain := range captains {
		if err := addHome(captain.Home); err != nil {
			return nil, nil, err
		}
	}
	for id := range reservedLaunches {
		delete(releasedLaunches, id)
	}

	for _, e := range entries {
		if e.Path == "" {
			return nil, nil, fmt.Errorf("provider status contains an empty worktree path")
		}
		if e.LeaseHolder != "" && reservedLaunches[e.LeaseHolder] {
			active[worktreeClaimKey(e.Path)] = true
		}
	}
	return active, releasedLaunches, nil
}

func hasCanonicalRetirementForReservation(agg taskauthority.Aggregate, reservationID string) bool {
	return agg.Phase == taskauthority.PhaseRetired && agg.Retirement != nil && agg.Retirement.Worktree != nil && agg.Retirement.Worktree.LeaseID == reservationID && agg.CleanupClaim != nil && agg.CleanupClaim.Status == taskauthority.CleanupCompleted
}

// retiredUnboundReservation reports whether an unbound launch reservation is
// released: no worktree was ever bound, and the task is Retired with its cleanup
// Completed. Done, Resolved, and Retired with active cleanup keep it held.
func retiredUnboundReservation(agg taskauthority.Aggregate) bool {
	return agg.Worktree == nil && (agg.Retirement == nil || agg.Retirement.Worktree == nil) && agg.Phase == taskauthority.PhaseRetired && agg.CleanupClaim != nil && agg.CleanupClaim.Status == taskauthority.CleanupCompleted
}

func worktreeClaimKey(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return filepath.Clean(absolute)
}
