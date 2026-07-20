package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/spf13/cobra"
)

func newCaptainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "captain",
		Short: "Manage persistent domain supervisors (captains)",
	}

	var seedRepo string
	var seedForce bool
	var seedRef string
	seedCmd := &cobra.Command{
		Use:   "seed <id> <home-path>",
		Short: "Seed a captain home with charter",
		Long: `Seed a captain home with charter and optional managed git worktree.

Without --repo, creates a state-only captain home (legacy format).
With --repo <path>, provisions a managed git-worktree captain home:
a detached worktree at <home-path> from the specified project repo,
with gitignore and provenance metadata.

Flags for worktree provisioning:
  --force  Replace existing managed worktree
  --ref    Explicit branch/ref (default: repo's default branch)
`,
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if seedRepo != "" {
				return captain.SeedFromWorktree(args[0], args[1], seedRepo, ctx.Home, "", seedForce, seedRef)
			}
			return captain.SeedWithParent(args[0], args[1], ctx.Home, "")
		}),
	}
	seedCmd.Flags().StringVar(&seedRepo, "repo", "", "Path to the project git repo for managed worktree captain home")
	seedCmd.Flags().BoolVar(&seedForce, "force", false, "Replace existing managed worktree")
	seedCmd.Flags().StringVar(&seedRef, "ref", "", "Explicit branch/ref (default: repo's default branch)")
	cmd.AddCommand(seedCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "launch <captain-home>",
		Short: "Launch a captain in its home (session-backed)",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return captain.Launch(args[0], ctx.Home)
		}),
	})

	var retireForce bool
	retireCmd := &cobra.Command{
		Use:   "retire <captain-home>",
		Short: "Retire a captain (session-backed)",
		Long:  "Retire tears down the captain endpoint, clears parent meta, and unregisters from data/captains.md. Refuses while the captain home has in-flight soldiers (kind ship|scout) unless --force.",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return captain.Retire(args[0], ctx.Home, false, retireForce)
		}),
	}
	retireCmd.Flags().BoolVar(&retireForce, "force", false, "Retire even if captain home has in-flight soldiers")
	cmd.AddCommand(retireCmd)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List registered captains",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			mates, err := captain.List(ctx.Home)
			if err != nil {
				return err
			}
			if len(mates) == 0 {
				return writeContract(cmd, contract.Response[contract.EmptyResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "captain.list",
					Status:        "success",
					Data:          contract.EmptyResult{Count: 0, Context: "No captains registered."},
				})
			}
			var b strings.Builder
			for _, m := range mates {
				b.WriteString(fmt.Sprintf("- %s (%s; scope: %s; projects: %s; added: %s)\n", m.ID, m.Home, m.Scope, m.Project, m.Added))
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "captain.list",
				Status:        "success",
				Data:          contract.MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(listCmd)
	cmd.AddCommand(listCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "handoff <captain-home> <item-key...>",
		Short: "Hand off backlog items to a captain",
		Long: `Hand off queued backlog items from the parent home to a captain.
All keys must be in queued state. Uses tasks-axi mv atomically.`,
		Args: MinimumNArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return captain.Handoff(ctx.Home, args[0], args[1:])
		}),
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "config-push <captain-home>",
		Short: "Push inheritable config to a captain",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return captain.ConfigPush(ctx.Home, args[0])
		}),
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "validate <captain-home>",
		Short: "Validate a captain home structure and provenance",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := captain.Validate(args[0], ctx.Home); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}
			fmt.Println("valid")
			return nil
		}),
	})

	migrateRepo := ""
	migrateCmd := &cobra.Command{
		Use:   "migrate <captain-home> <id>",
		Short: "Migrate a seeded home to managed worktree",
		Long: `Migrate a captain home to a managed git worktree.

Without --repo, writes a provenance marker to a legacy state-only home (simple).
With --repo <path>, performs a transactional migration from state-only home to
managed git worktree, preserving operational dirs (state/, config/, data/, etc.).

In worktree mode, the migration is atomic: on failure the original home is
restored and a rollback marker (.migration-rollback) is written. On success,
the old home is backed up at <home-path>.backup-<timestamp>.`,
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if migrateRepo != "" {
				return captain.MigrateToWorktree(args[0], migrateRepo, args[1], ctx.Home)
			}
			return captain.Migrate(args[0], args[1])
		}),
	}
	migrateCmd.Flags().StringVar(&migrateRepo, "repo", "", "Path to the project git repo for managed worktree migration")
	cmd.AddCommand(migrateCmd)

	updateCmd := &cobra.Command{
		Use:   "update <captain-home>",
		Short: "Update a captain home (safe FF) and return typed outcome",
		Long: `Update performs a safe local fast-forward of a captain clone, returning a typed outcome:
already-current, fast-forwarded, state-only-skipped, dirty, diverged, offline,
wrong-remote, wrong-branch, or invalid-provenance.
State-only homes (no git worktree) return state-only-skipped rather than failing.`,
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			res := captain.Update(args[0], ctx.Home)
			fmt.Printf("outcome: %s\n", res.Outcome)
			if res.Outcome == captain.FastForwarded {
				fmt.Printf("  %s → %s\n", res.Before[:8], res.After[:8])
			}
			if res.Err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "detail: %s\n", res.Err)
			}
			if res.Outcome.IsFailure() {
				return fmt.Errorf("update failed: %s", res.Outcome)
			}
			return nil
		}),
	}
	cmd.AddCommand(updateCmd)

	convergeCmd := &cobra.Command{
		Use:   "converge",
		Short: "Converge all registered captains",
		Long: `Locked convergence sweep: validate registry/provenance, flush send outbox,
retry pending nudges, safe local fast-forward, inheritance push, liveness check, and instruction
surface tracking. State changes tracked in parent state/.captain-converge.lock`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			registered, err := captain.List(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing registered captains: %w", err)
			}
			result, convergeErr := captain.Converge(ctx.Home, registered)
			if result != nil {
				for _, step := range result.Steps {
					fmt.Printf("  %-50s %s\n", step.Name+":", step.Status)
					if step.Detail != "" && step.Detail != "ok" {
						fmt.Printf("  %-50s %s\n", "", step.Detail)
					}
				}
				fmt.Printf("  Overall: %s\n", result.OverallStatus())
			}
			return convergeErr
		}),
	}
	cmd.AddCommand(convergeCmd)

	recoverCmd := &cobra.Command{
		Use:   "recover <captain-id>",
		Short: "Run structured recovery transaction for a captain",
		Long: `Run the full recovery transaction for one captain: provenance → config → integration → launch readiness → relaunch pane → watcher ensure → outbox flush → nudge retry.
	Each step reports ok/failed/skipped so partial failures do not block the whole recovery.`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			registered, err := captain.List(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing registered captains: %w", err)
			}
			var target *captain.Info
			for _, m := range registered {
				if m.ID == args[0] {
					m2 := m
					target = &m2
					break
				}
			}
			if target == nil {
				return fmt.Errorf("no registered captain with id %q", args[0])
			}
			tx := &captain.RecoverTransaction{}
			res := tx.Recover(ctx.Home, *target)
			fmt.Println(res.StepsString())
			return nil
		}),
	}
	cmd.AddCommand(recoverCmd)

	return cmd
}

// captainLivenessForSession backs the session-start Captain Liveness section. It always
// probes; when recover is true it also relaunches launched-but-dead endpoints.
func captainLivenessForSession(home string, recover bool) session.CaptainLivenessResult {
	registered, err := captain.List(home)
	if err != nil {
		return session.CaptainLivenessResult{}
	}
	probes := captain.ProbeLiveness(home, registered)
	res := session.CaptainLivenessResult{Probes: make([]session.CaptainProbe, 0, len(probes))}
	for _, p := range probes {
		res.Probes = append(res.Probes, session.CaptainProbe{ID: p.ID, Home: p.Home, Status: p.Status})
		if p.Status == "dead" {
			res.HasDead = true
		}
	}
	if !recover {
		return res
	}
	rr, _ := captain.Recover(home, registered)
	if rr != nil {
		res.Recover = &session.CaptainRecoverSummary{
			Relaunched: rr.Relaunched,
			Alive:      rr.Alive,
			Seeded:     rr.Seeded,
			Failed:     rr.Failed,
		}
		for _, e := range rr.Entries {
			res.Recover.Entries = append(res.Recover.Entries, captainRecoverEntryLine(e))
		}
	}
	return res
}

// captainRecoverEntryLine renders one RecoverEntry as a single summary line.
func captainRecoverEntryLine(e captain.RecoverEntry) string {
	switch e.Outcome {
	case captain.RecoverAlive:
		return e.ID + ": alive"
	case captain.RecoverSeeded:
		return e.ID + ": seeded (not launched)"
	case captain.RecoverRelaunched:
		return e.ID + ": relaunched"
	case captain.RecoverFailed:
		return e.ID + ": FAILED: " + e.Error
	}
	return e.ID + ": " + string(e.Outcome)
}

