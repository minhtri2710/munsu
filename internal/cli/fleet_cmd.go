package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/spf13/cobra"
)

func newFleetSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fleet-sync [<project>]",
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
	return &cobra.Command{
		Use:   "fleet-snapshot",
		Short: "Emit fleet snapshot JSON",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			snap, err := fleet.Snapshot(ctx.Home)
			if err != nil {
				return err
			}
			j, err := snap.JSON()
			if err != nil {
				return err
			}
			fmt.Println(j)
			return nil
		}),
	}
}

func newFleetViewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fleet-view",
		Short: "Render fleet view from snapshot",
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return fleet.View(ctx.Home)
		}),
	}
}

func newBearingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bearings",
		Short: "Compact resume report",
		Args:  MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			projectDir := ""
			if len(args) > 0 {
				projectDir = args[0]
			}
			return fleet.Bearings(ctx.Home, projectDir)
		}),
	}
}
