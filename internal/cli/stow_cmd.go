package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/spf13/cobra"
)

func newStowCmd() *cobra.Command {
	var kind string
	var captain bool

	cmd := &cobra.Command{
		Use:   "stow [text...]",
		Short: "Sweep session for durable knowledge",
		Long: `Capture durable learnings or general preferences from the current session.

Flags:
  --kind learning|general   which file to stow (default: learning)
  --general                  shorthand for --kind general

Entries are inspect-then-update: if a new entry matches an existing
entry (by substring), the existing entry is replaced in place rather
than appended. Non-matching entries are appended as usual.

Examples:
  munsu stow "Go 1.26 uses range-over-func"
  munsu stow --general "Prefer simple project layouts"
  munsu stow --kind general "Prefer simple layouts"
`,
		Args: MinimumNArgs(0),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if captain {
				kind = KindGeneral
			}
			if kind == "" {
				kind = KindLearning
			}

			res, err := RunKinded(ctx.Home, kind, args)
			if err != nil {
				return err
			}

			switch {
			case res.DataLearnings != "":
				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "stow",
					Status:        "success",
					Data:          contract.MessageResult{Message: fmt.Sprintf("Stowed learnings to %s", res.DataLearnings)},
				})
			case res.DataCaptain != "":
				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "stow",
					Status:        "success",
					Data:          contract.MessageResult{Message: fmt.Sprintf("Stowed general preferences to %s", res.DataCaptain)},
				})
			default:
				return writeContract(cmd, contract.Response[contract.MessageResult]{
					SchemaVersion: contract.SchemaVersion,
					Kind:          "stow",
					Status:        "success",
					Data:          contract.MessageResult{Message: "Nothing to stow (no text provided)", Noop: true},
				})
			}
		}),
	}

	configureContractCommand(cmd)
	cmd.Flags().StringVar(&kind, "kind", "", "Kind of stow entry (learning|general)")
	cmd.Flags().BoolVar(&captain, "general", false, "Shorthand for --kind general")
	return cmd
}

func newEnsureAgentsMdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ensure-agents-md <project>",
		Short: "Ensure project AGENTS.md and CLAUDE.md symlink",
		Long: `Create or update AGENTS.md and CLAUDE.md symlink for a project.
Adds the self-governance section if missing.

The <project> argument can be a project name (resolved from the registry)
or an absolute path to a project directory.`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			projectArg := args[0]

			// Resolve project name to path if not already an absolute path
			projectDir := projectArg
			if !filepath.IsAbs(projectArg) {
				resolved, err := fleet.ResolveRepoPath(ctx.Home, projectArg)
				if err != nil {
					return fmt.Errorf("resolving project %q: %w", projectArg, err)
				}
				projectDir = resolved
			}

			res, err := Ensure(projectDir, false)
			if err != nil {
				return err
			}

			var msg strings.Builder
			msg.WriteString(fmt.Sprintf("Ensured AGENTS.md at %s", res.AGENTSMD))
			if res.CLAUDEMDSym != "" {
				msg.WriteString(fmt.Sprintf("\nCreated CLAUDE.md symlink at %s", res.CLAUDEMDSym))
			}
			if res.SelfGovernSec {
				msg.WriteString("\nAdded '## Maintaining this file' section")
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "ensure-agents-md",
				Status:        "success",
				Data:          contract.MessageResult{Message: msg.String()},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newUpdateCmd() *cobra.Command {
	var captains bool
	var repoOpt string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Self-update munsu with watcher handshake",
		Long: `Pull latest munsu sources, rebuild, and atomically install the binary.
If a watcher is currently running, gracefully restart it and wait for
heartbeat confirmation with the new build version.

With --captains, after the self-update succeeds, fast-forward every
registered captain home to the parent default branch and nudge each
captain whose instruction surface (AGENTS.md, bin/, .agents/skills/)
advanced to re-read its charter. Fail-closed per captain: dirty,
diverged, or offline homes are skipped and reported, never forced.

Install root resolution (in order):
  --repo <path>          explicit source checkout path
  MUNSU_REPO             environment variable
  <munsu-home>/config/install-root   persisted from a previous successful update
  Binary ancestry        when the munsu binary is inside a git checkout
  Current working directory         when inside a matching munsu checkout`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			snap, err := UpdateWithHandshakeEx(ctx.Home, repoOpt)
			if err != nil {
				// Self-update failed: captains are NOT touched.
				return err
			}
			if snap.Active {
				fmt.Fprintf(cmd.OutOrStdout(), "Updated munsu to %s; watcher restarted (pid was %d)\n",
					snap.InstalledVersion, snap.OldPID)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Updated munsu to %s (no active watcher)\n",
					snap.InstalledVersion)
			}

			if !captains {
				return nil
			}

			// Only reachable if self-update succeeded.
			registered, err := captain.List(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing registered captains: %w", err)
			}
			if len(registered) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No captains registered — nothing to fast-forward.")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Fast-forwarding captains and nudging...")
			result, convergeErr := captain.Converge(ctx.Home, registered, captain.ConvergeCapabilities{Notification: newSessionUplinkTransport(), Mailbox: newSessionMailboxSender(), Launch: newSessionLaunchEndpoint(), Probe: newSessionProbeEndpoint(), Nudge: newSessionNudgeEndpoint()})
			if result != nil {
				for _, step := range result.Steps {
					fmt.Fprintf(cmd.OutOrStdout(), "  %-50s %s\n", step.Name+":", step.Status)
					if step.Detail != "" && step.Detail != "ok" {
						fmt.Fprintf(cmd.OutOrStdout(), "  %-50s %s\n", "", step.Detail)
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  Overall: %s\n", result.OverallStatus())
			}
			if convergeErr != nil {
				// Self-update already succeeded; converge errors are reported, not fatal.
				fmt.Fprintf(cmd.OutOrStdout(), "%v\n", convergeErr)
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&captains, "captains", false,
		"Fast-forward all registered captain homes and nudge updated captains to re-read charter")
	cmd.Flags().StringVar(&repoOpt, "repo", "",
		"Explicit path to the munsu source checkout (overrides MUNSU_REPO and other resolution)")
	return cmd
}
