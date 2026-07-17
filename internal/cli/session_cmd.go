package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/brief"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/spawn"
	"github.com/minhtri2710/munsu/internal/supervision"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/waker"
	"github.com/minhtri2710/munsu/internal/worktree"
	"github.com/spf13/cobra"
)

func newBriefCmd() *cobra.Command {
	var scout bool
	var force bool
	var modeFlag string

	cmd := &cobra.Command{
		Use:   "brief <id> <repo>",
		Short: "Scaffold a task brief",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			id := args[0]
			repo := args[1]

			// Resolve delivery mode using full auto-detection chain
			projectMode := ""
			projYolo := false
			if m, y, err := project.Mode(ctx.Home, repo); err == nil {
				projectMode = m
				projYolo = y
			}

			resolvedMode, err := spawn.ResolveDeliveryMode(ctx.Home, modeFlag, projectMode)
			if err != nil {
				return err
			}

			// Require existing task meta unless --force
			if !force {
				if _, err := task.ReadMeta(ctx.Home, id); err != nil {
					return fmt.Errorf("task %q not found: create it with 'munsu task add %s ...' or use --force", id, id)
				}
			}
			opts := brief.ScaffoldOptions{
				HomeDir: ctx.Home,
				ID:      id,
				Repo:    repo,
				Scout:   scout,
				Mode:    resolvedMode,
				Yolo:    projYolo,
			}

			if err := brief.Scaffold(opts); err != nil {
				return err
			}

			kind := "ship"
			if scout {
				kind = "scout"
			}

			var b strings.Builder
			b.WriteString(fmt.Sprintf("Brief scaffolded at %s\n", brief.Path(ctx.Home, id)))
			b.WriteString(fmt.Sprintf("  id:    %s\n", id))
			b.WriteString(fmt.Sprintf("  repo:  %s\n", repo))
			b.WriteString(fmt.Sprintf("  kind:  %s\n", kind))
			if resolvedMode != "" {
				b.WriteString(fmt.Sprintf("  mode:  %s\n", resolvedMode))
			}
			if projYolo {
				b.WriteString("  yolo:  true\n")
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "brief",
				Status:        "success",
				Data:          contract.MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(cmd)

	cmd.Flags().BoolVar(&scout, "scout", false, "Generate a scout brief instead of ship brief")
	cmd.Flags().BoolVar(&force, "force", false, "Scaffold brief without requiring existing task meta")
	cmd.Flags().StringVar(&modeFlag, "mode", "", "Delivery mode override (no-mistakes|direct-PR|local-only)")

	return cmd
}

func newSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "session-start",
		Short: "Lock, bootstrap, and print session-start digest (Context, Fleet State, Supervision)",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			_, err := session.RunSessionStart(ctx.Home)
			return err
		}),
	}
}

func newBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap [install <tools>...]",
		Short: "Detect toolchain and run setup sweeps",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			locked := lifecycle.IsSessionLocked(ctx.Home)
			var installTools []string
			if len(args) > 1 && args[0] == "install" {
				installTools = args[1:]
			}
			result, err := bootstrap.Run(ctx.Home, !locked, installTools)
			if err != nil {
				return err
			}

			var b strings.Builder
			for _, d := range result.Tools {
				b.WriteString(d.String() + "\n")
			}
			if result.Auth != nil {
				b.WriteString(result.Auth.String() + "\n")
			}
			for _, c := range result.Configs {
				b.WriteString(c.String() + "\n")
			}
			if result.GC != nil {
				b.WriteString(result.GC.String() + "\n")
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "bootstrap",
				Status:        "success",
				Data:          contract.MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Run the event-driven watcher",
		Long:  `Run the event-driven watcher loop. Exits with a wake reason when an actionable event is found. Singleton-safe (home-scoped lock).`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			reason, err := supervision.Run(ctx.Home)
			if err != nil {
				return err
			}
			if reason != nil {
				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "watch",
					Status:        "success",
					Data:          contract.MessageResult{Message: fmt.Sprintf("wake: %s — %s", reason.Kind, reason.Message)},
				})
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "watch",
				Status:        "success",
				Data:          contract.MessageResult{Message: "no wake reason", Noop: true},
			})
		}),
	}
	configureContractCommand(cmd)

	// Add subcommands
	ensureCmd := newWatchEnsureCmd()
	ensureCmd.Use = "ensure"
	runCmd := newWatchRunCmd()
	runCmd.Use = "run"
	cmd.AddCommand(ensureCmd)
	cmd.AddCommand(runCmd)
	cmd.AddCommand(newWatchStopCmd())

	return cmd
}

func newWatchArmCmd() *cobra.Command {
	var restart bool
	cmd := &cobra.Command{
		Use:   "watch-arm",
		Short: "Arm the watcher (home-scoped)",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return supervision.ArmBackground(ctx.Home, restart)
		}),
	}
	cmd.Flags().BoolVar(&restart, "restart", false, "Restart existing watcher before arming")
	return cmd
}

func newWakeDrainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wake-drain",
		Short: "Drain queued wakes",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			records, err := waker.Drain(ctx.Home)
			if err != nil {
				return err
			}
			waker.PrintRecords(records)
			return nil
		}),
	}
}

func newGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guard",
		Short: "Warn on tangle or stale watcher",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			waker.CheckGuard(ctx.Home)

			// Check all registered projects for tangles using resolved paths
			projects, err := project.List(ctx.Home)
			if err == nil {
				for _, p := range projects {
					projDir, resolveErr := project.ResolveRepoPath(ctx.Home, p.Name)
					if resolveErr != nil {
						continue // skip unresolvable projects
					}
					if err := worktree.AssertNotTangled(projDir, p.Name); err != nil {
						w := err.Error()
						border := strings.Repeat("●", len(w)+4)
						fmt.Println(border)
						fmt.Println("● " + w + " ●")
						fmt.Println(border)
					}
				}
			}

			return nil
		}),
	}
}

func newAfkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "afk",
		Short: "Enter away-mode supervision",
		Long: `Start the away-mode sub-supervisor daemon.

The daemon sets the AFK consent flag, acquires an identity lock,
and runs one wake-triage cycle. It then blocks until SIGTERM/SIGINT.
The flag and lock are cleaned up on stop.

Subcommands:
  return     Ordered AFK daemon shutdown with digest drain
  return check  Check if actionable AFK state remains`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			var d afk.Daemon
			return d.Start(ctx.Home)
		}),
	}
	cmd.AddCommand(newAfkReturnCmd())
	return cmd
}

func newAfkReturnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "return",
		Short: "Perform ordered AFK daemon shutdown",
		Long: `Stop the AFK daemon, drain the durable digest queue,
and print a summary of escalations, wedge alarms, and blocked items.

Check exit code via 'munsu afk return check' — returns 0 when
no actionable AFK state remains.`,
		Args: cobra.NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			report, err := afk.Return(ctx.Home)
			if err != nil {
				return err
			}
			cmd.Println(report.String())
			return nil
		}),
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Check if any actionable AFK state remains",
		Long: `Re-read the durable digest and exit 0 if clean,
non-zero if actionable items remain.`,
		Args: cobra.NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if !afk.IsClean(ctx.Home) {
				return fmt.Errorf("actionable AFK state remains — run 'munsu afk return' to reconcile")
			}
			return nil
		}),
	})
	return cmd
}
