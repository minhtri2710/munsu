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
			version, _ := cmd.Flags().GetInt("version")
			if version != 1 && version != 2 {
				return usageError("unsupported_input", "Run `munsu fleet snapshot --version 2`", "Only fleet snapshot versions 1 and 2 are supported")
			}
			if version == 2 {
				return runFleetSnapshotV2(cmd, ctx)
			}
			snap, err := fleet.Snapshot(ctx.Home, snapshotDeps())
			if err != nil {
				return err
			}
			return writeContract(cmd, Response[fleet.FleetSnapshot]{
				SchemaVersion: SchemaVersion,
				Kind:          "fleet.snapshot.v1",
				Status:        "success",
				Data:          *snap,
			})
		}),
	}
	cmd.Flags().Int("version", 1, "Snapshot schema version")
	cmd.Flags().String("fields", "", "Optional row fields for version 2")
	cmd.Flags().Bool("full", false, "Include full truncated content for version 2")
	cmd.Flags().String("output", OutputTOON, "Output format for version 2 (toon|json)")
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
