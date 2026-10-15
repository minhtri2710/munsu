package cli

import (
	"fmt"
	"io"
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
				message := "No projects to sync."
				if projectName != "" {
					if _, findErr := fleet.Find(ctx.Home, projectName); findErr != nil {
						message = fmt.Sprintf("no registered project named %q", projectName)
					}
				}
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "fleet.sync",
					Status:        "success",
					Data:          MessageResult{Message: message},
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
			snap, err := fleet.Snapshot(ctx.Home, snapshotDeps())
			if err != nil {
				return err
			}
			renderFleetView(cmd.OutOrStdout(), snap)
			return nil
		}),
	}
}

// renderFleetView renders the fleet snapshot as Markdown.
func renderFleetView(w io.Writer, snap *fleet.FleetSnapshot) {
	m := outputMarks(w)
	fmt.Fprintf(w, "# Fleet View — %s\n\n", snap.Time)
	fmt.Fprintf(w, "Tasks: %d\n\n", len(snap.Tasks))

	for _, ts := range snap.Tasks {
		phase := fleet.PhaseFromProjection(ts)

		src := ts.Source
		if src == "" {
			src = "primary"
		}
		fmt.Fprintf(w, "- **%s** (repo: %s) [%s]\n", ts.ID, ts.Project, src)
		fmt.Fprintf(w, "  kind: %s | mode: %s | yolo: %s\n", ts.Kind, ts.Mode, ts.Yolo)
		var modelEffortParts []string
		if ts.Harness != "" {
			modelEffortParts = append(modelEffortParts, fmt.Sprintf("harness: %s", ts.Harness))
		}
		if ts.Model != "" {
			modelEffortParts = append(modelEffortParts, fmt.Sprintf("model: %s", ts.Model))
		}
		if len(modelEffortParts) > 0 {
			fmt.Fprintf(w, "  %s\n", strings.Join(modelEffortParts, " | "))
		}

		displayStatus := ts.CurrentDescription
		if displayStatus == "" {
			displayStatus = ts.LastStatus
		}
		fmt.Fprintf(w, "  pane: %s (%s)\n", ts.Window, m.mark(phase, phase))
		if displayStatus != "" {
			fmt.Fprintf(w, "  status: %s\n", displayStatus)
		}
		fmt.Fprintln(w)
	}
}
