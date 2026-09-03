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
render fleet views, and print compact resume reports.`,
	}
	cmd.AddCommand(newFleetSyncCmd())
	cmd.AddCommand(newFleetSnapshotCmd())
	cmd.AddCommand(newFleetViewCmd())
	cmd.AddCommand(newFleetBearingsCmd())
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

func newFleetBearingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bearings [<project-dir>]",
		Short: "Compact resume report",
		Long: `Print a compact resume report for the fleet or a single project.

The report shows the aggregate health of all registered projects, listing
in-flight tasks with their project, phase, and last known status.
When a project directory argument is given, only that project is shown.`,
		Args: MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			projectDir := ""
			if len(args) > 0 {
				projectDir = args[0]
			}
			return fleet.Bearings(ctx.Home, projectDir, snapshotDeps())
		}),
	}
}
