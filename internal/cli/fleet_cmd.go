package cli

import (
	"fmt"

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
	return &cobra.Command{
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
			for _, s := range result.Synced {
				fmt.Printf("synced: %s\n", s)
			}
			for _, s := range result.Stuck {
				fmt.Printf("STUCK: %s\n", s)
			}
			for _, e := range result.Errors {
				fmt.Printf("error: %s\n", e)
			}
			return nil
		}),
	}
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
			snap, err := fleet.Snapshot(ctx.Home)
			if err != nil {
				return err
			}
			j, err := snap.JSON()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), j)
			return nil
		}),
	}
	cmd.Flags().Int("version", 1, "Snapshot schema version")
	cmd.Flags().String("fields", "", "Optional row fields for version 2")
	cmd.Flags().Bool("full", false, "Include full truncated content for version 2")
	cmd.Flags().String("output", "toon", "Output format for version 2 (toon|json)")
	return cmd
}

func newFleetViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "view",
		Short: "Render fleet view from snapshot",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return fleet.View(ctx.Home)
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
			return fleet.Bearings(ctx.Home, projectDir)
		}),
	}
}

// newFleetSyncTopCmd is a hidden compatibility alias for 'munsu fleet sync'.
func newFleetSyncTopCmd() *cobra.Command {
	cmd := newFleetSyncCmd()
	cmd.Use = "fleet-sync [<project>]"
	cmd.Hidden = true
	cmd.Deprecated = "use 'munsu fleet sync' instead"
	return cmd
}

// newFleetSnapshotTopCmd is a hidden compatibility alias for 'munsu fleet snapshot'.
func newFleetSnapshotTopCmd() *cobra.Command {
	cmd := newFleetSnapshotCmd()
	cmd.Use = "fleet-snapshot"
	cmd.Hidden = true
	cmd.Deprecated = "use 'munsu fleet snapshot' instead"
	return cmd
}

// newFleetViewTopCmd is a hidden compatibility alias for 'munsu fleet view'.
func newFleetViewTopCmd() *cobra.Command {
	cmd := newFleetViewCmd()
	cmd.Use = "fleet-view"
	cmd.Hidden = true
	cmd.Deprecated = "use 'munsu fleet view' instead"
	return cmd
}

// newBearingsTopCmd is a hidden compatibility alias for 'munsu fleet bearings'.
func newBearingsTopCmd() *cobra.Command {
	cmd := newFleetBearingsCmd()
	cmd.Use = "bearings"
	cmd.Hidden = true
	cmd.Deprecated = "use 'munsu fleet bearings' instead"
	return cmd
}
