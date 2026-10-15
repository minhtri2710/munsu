package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/spf13/cobra"
)

func newFleetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "Manage fleet operations",
		Long: `Manage fleet operations: sync project clones, emit snapshots,
and render fleet views.`,
	}
	cmd.AddCommand(newFleetSyncCmd())
	cmd.AddCommand(newFleetSnapshotCmd())
	cmd.AddCommand(newFleetViewCmd())
	return cmd
}

func newFleetSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync [<project>]",
		Short: "Fast-forward refresh project clones",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			projectName := ""
			if len(args) > 0 {
				projectName = args[0]
			}
			result, err := fleet.Sync(ctx.Home, projectName)
			if err != nil {
				return err
			}
			if len(result.Synced) == 0 && len(result.Stuck) == 0 && len(result.Errors) == 0 {
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "fleet.sync",
					Status:        "success",
					Data:          MessageResult{Message: "No projects to sync."},
				})
			}
			var b strings.Builder
			for _, s := range result.Synced {
				b.WriteString(fmt.Sprintf("synced: %s\n", s))
			}
			for _, s := range result.Stuck {
				b.WriteString(fmt.Sprintf("STUCK: %s\n", s))
			}
			for _, e := range result.Errors {
				b.WriteString(fmt.Sprintf("error: %s\n", e))
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "fleet.sync",
				Status:        "success",
				Data:          MessageResult{Message: strings.TrimSpace(b.String())},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}
func newFleetSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Emit fleet snapshot JSON",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return runFleetSnapshot(cmd, ctx)
		}),
	}
	cmd.Flags().String("fields", "", "Optional row fields")
	cmd.Flags().Bool("full", false, "Include full truncated content")
	cmd.Flags().String("output", OutputTOON, "Output format (toon|json)")
	return cmd
}

func newFleetViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view",
		Short: "Render fleet view from snapshot",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return fleet.View(ctx.Home, snapshotDeps())
		}),
	}
}
