package cli

import (
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/herdrprune"
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
hometag and have zero live tabs. Dry-run by default; use --apply to close.

Safety invariants (always enforced):
  - Never closes workspaces whose label does not match a known munsu hometag.
  - Never closes workspaces with any live agent.
  - Never closes workspaces referenced by live task meta (herdr_workspace_id).
  - Never closes workspaces with deny-listed labels (firstmate, captain-*).
  - --apply with no matching workspaces is a no-op (not an error).`,
		Args: NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			result, err := herdrprune.RunPrune(herdrprune.PruneOptions{
				Session: session,
				Apply:   apply,
				HomeDir: ctx.Home,
			})
			if err != nil {
				return err
			}
			return writeContract(cmd, contract.Response[herdrprune.PruneResult]{
				SchemaVersion: contract.SchemaVersion,
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
