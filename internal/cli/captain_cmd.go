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

	cmd.AddCommand(&cobra.Command{
		Use:   "seed <id> <home-path>",
		Short: "Seed a captain home with charter",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return captain.SeedWithParent(args[0], args[1], ctx.Home, "")
		}),
	})

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

	migrateCmd := &cobra.Command{
		Use:   "migrate <captain-home> <id>",
		Short: "Migrate a seeded home (write provenance marker)",
		Args:  ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return captain.Migrate(args[0], args[1])
		},
	}
	cmd.AddCommand(migrateCmd)

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
			return captain.Converge(ctx.Home, registered)
		}),
	}
	cmd.AddCommand(convergeCmd)

	recoverCmd := &cobra.Command{
		Use:   "recover [captain-home]",
		Short: "Probe captain liveness and relaunch launched-but-dead endpoints",
		Long: `Probe registered captain endpoints and relaunch any that are launched-but-dead.
	Fails closed on unknown/unverified harnesses (recorded per-captain, does not abort the sweep).
	With no argument, probes every registered captain. Pass a captain home to scope to one.`,
		Args: cobra.MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			registered, err := captain.List(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing registered captains: %w", err)
			}
			if len(args) == 1 {
				registered, err = scopeCaptainsToHome(registered, args[0])
				if err != nil {
					return err
				}
			}
			res, err := captain.Recover(ctx.Home, registered)
			if err != nil {
				return err
			}
			fmt.Println(res.String())
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

// scopeCaptainsToHome filters registered captains to those whose home matches the given
// path. Returns an error if none match.
func scopeCaptainsToHome(registered []captain.Info, home string) ([]captain.Info, error) {
	var out []captain.Info
	for _, m := range registered {
		if m.Home == home {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no registered captain matches home %q", home)
	}
	return out, nil
}
