package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/spf13/cobra"
)

func newHerdrCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "herdr",
		Short: "Manage herdr workspaces and sessions",
		Long: `Manage herdr workspaces: list, prune empty munsu-created workspaces.
The prune subcommand lists or closes munsu-owned herdr workspaces that have
zero live tabs, respecting a deny-list of critical workspaces.`,
	}
	cmd.AddCommand(newHerdrPruneCmd())
	return cmd
}

func newHerdrPruneCmd() *cobra.Command {
	var apply bool
	var session string

	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "List or close empty munsu-created herdr workspaces",
		Long: `List or close herdr workspaces whose label matches the current munsu home
hometag or a registered captain workspace label, and have zero live tabs.
Dry-run by default; use --apply to close.

Safety invariants (always enforced):
  - Never closes workspaces whose label does not match a known munsu hometag.
  - Never closes workspaces with any live agent.
  - Never closes workspaces referenced by live task meta (herdr_workspace_id).
  - Never closes workspaces with deny-listed labels (legacy-protected, captain-*).
  - --apply with no matching workspaces is a no-op (not an error).`,
		Args: NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			// Captain facts come from the canonical Fleet Registry; Backend owns
			// capability (label derivation) but never reads registry state.
			captains, err := fleet.ListCaptains(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing registered captains: %w", err)
			}
			captainHomes := make([]string, 0, len(captains))
			for _, c := range captains {
				if c.Home != "" {
					captainHomes = append(captainHomes, c.Home)
				}
			}
			result, err := backend.RunPrune(backend.PruneOptions{
				Session:      session,
				Apply:        apply,
				HomeDir:      ctx.Home,
				CaptainHomes: captainHomes,
			})
			if err != nil {
				return err
			}
			return writeContract(cmd, Response[backend.PruneResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "herdr.prune",
				Status:        "success",
				Data:          *result,
			})
		}),
	}

	configureContractCommand(pruneCmd)
	pruneCmd.Flags().BoolVar(&apply, "apply", false, "Close matching workspaces (default: dry-run)")
	pruneCmd.Flags().StringVar(&session, "session", "", "Herdr session name (default: HERDR_SESSION env or 'default')")

	return pruneCmd
}
