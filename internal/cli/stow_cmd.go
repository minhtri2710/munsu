package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/agentsmd"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/selfupdate"
	"github.com/minhtri2710/munsu/internal/stow"
	"github.com/spf13/cobra"
)

func newStowCmd() *cobra.Command {
	var kind string
	var captain bool

	cmd := &cobra.Command{
		Use:   "stow [text...]",
		Short: "Sweep session for durable knowledge",
		Long: `Capture durable learnings or captain preferences from the current session.

Flags:
  --kind learning|captain   which file to stow (default: learning)
  --captain                  shorthand for --kind captain

Entries are inspect-then-update: if a new entry matches an existing
entry (by substring), the existing entry is replaced in place rather
than appended. Non-matching entries are appended as usual.

Examples:
  munsu stow "Go 1.26 uses range-over-func"
  munsu stow --captain "Prefer simple project layouts"
  munsu stow --kind captain "Prefer simple layouts"
`,
		Args: MinimumNArgs(0),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if captain {
				kind = stow.KindCaptain
			}
			if kind == "" {
				kind = stow.KindLearning
			}

			res, err := stow.RunKinded(ctx.Home, kind, args)
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
					Data:          contract.MessageResult{Message: fmt.Sprintf("Stowed captain preferences to %s", res.DataCaptain)},
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
	cmd.Flags().StringVar(&kind, "kind", "", "Kind of stow entry (learning|captain)")
	cmd.Flags().BoolVar(&captain, "captain", false, "Shorthand for --kind captain")
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
				resolved, err := project.ResolveRepoPath(ctx.Home, projectArg)
				if err != nil {
					return fmt.Errorf("resolving project %q: %w", projectArg, err)
				}
				projectDir = resolved
			}

			res, err := agentsmd.Ensure(projectDir, false)
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
				Kind:          "stow.ensure-agents-md",
				Status:        "success",
				Data:          contract.MessageResult{Message: msg.String()},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Self-update munsu",
		RunE: func(cmd *cobra.Command, args []string) error {
			return selfupdate.Update()
		},
	}
}
