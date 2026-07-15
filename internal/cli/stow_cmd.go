package cli

import (
	"fmt"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/agentsmd"
	"github.com/minhtri2710/munsu/internal/home"
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
		RunE: func(cmd *cobra.Command, args []string) error {
			if captain {
				kind = stow.KindCaptain
			}
			if kind == "" {
				kind = stow.KindLearning
			}

			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return err
			}

			res, err := stow.RunKinded(homeDir, kind, args)
			if err != nil {
				return err
			}

			switch {
			case res.DataLearnings != "":
				fmt.Printf("Stowed learnings to %s\n", res.DataLearnings)
			case res.DataCaptain != "":
				fmt.Printf("Stowed captain preferences to %s\n", res.DataCaptain)
			default:
				fmt.Println("Nothing to stow (no text provided)")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "", "Kind of stow entry (learning|captain)")
	cmd.Flags().BoolVar(&captain, "captain", false, "Shorthand for --kind captain")
	return cmd
}

func newEnsureAgentsMdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ensure-agents-md <project>",
		Short: "Ensure project AGENTS.md and CLAUDE.md symlink",
		Long: `Create or update AGENTS.md and CLAUDE.md symlink for a project.
Adds the self-governance section if missing.

The <project> argument can be a project name (resolved from the registry)
or an absolute path to a project directory.`,
		Args: ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectArg := args[0]

			// Resolve project name to path if not already an absolute path
			projectDir := projectArg
			if !filepath.IsAbs(projectArg) {
				homeDir, err := home.Resolve(homeOverride)
				if err != nil {
					return fmt.Errorf("resolving home: %w", err)
				}
				resolved, err := project.ResolveRepoPath(homeDir, projectArg)
				if err != nil {
					return fmt.Errorf("resolving project %q: %w", projectArg, err)
				}
				projectDir = resolved
			}

			res, err := agentsmd.Ensure(projectDir, false)
			if err != nil {
				return err
			}
			fmt.Printf("Ensured AGENTS.md at %s\n", res.AGENTSMD)
			if res.CLAUDEMDSym != "" {
				fmt.Printf("Created CLAUDE.md symlink at %s\n", res.CLAUDEMDSym)
			}
			if res.SelfGovernSec {
				fmt.Println("Added '## Maintaining this file' section")
			}
			return nil
		},
	}
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
