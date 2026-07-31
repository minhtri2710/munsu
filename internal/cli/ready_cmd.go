package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

func newReadyCmd() *cobra.Command {
	var eventID string

	cmd := &cobra.Command{
		Use:   "ready",
		Short: "Emit a durable ready signal (soldier review-ready/idle turn boundary)",
		Long: `Emit a durable ready signal indicating the soldier has reached a turn boundary
(review-ready, idle) and is ready for new commands.

The ready signal is persisted as a marker file in state/.ready/<taskID>/<eventID>.ready
and is consumed by the captain (via 'munsu consume-ready <task-id>') to flush one
pending command.

Call this at every real turn boundary (review-ready/idle), not at terminal done/merge.

The notification to the captain happens through the existing report/sentinel mechanism
('munsu report review-ready'), which injects a signal into the captain's pane. The
captain agent then calls 'munsu consume-ready <task-id>' to process ready events.

Soldier usage:
  munsu ready --event-id <unique-id>

The --event-id should be unique per turn boundary (e.g., a timestamp or turn counter).`,
		Args: cobra.NoArgs,
		RunE: withHome(func(cmd *cobra.Command, _ []string, ctx Ctx) error {
			taskID := os.Getenv("MUNSU_TASK_ID")
			homeDir := os.Getenv("MUNSU_HOME")

			if homeDir == "" {
				return operationError("invalid_environment",
					"Run inside a munsu-managed task (MUNSU_HOME must be set)",
					"MUNSU_HOME is not set")
			}
			if taskID == "" {
				return operationError("invalid_environment",
					"Run inside a munsu-managed task (MUNSU_TASK_ID must be set)",
					"MUNSU_TASK_ID is not set")
			}

			// Resolve endpoint generation from the authoritative aggregate, with legacy meta fallback.
			fallbackGeneration := ""
			if meta, err := home.ReadMeta(homeDir, taskID); err == nil {
				fallbackGeneration = meta["generation"]
			}
			metaGeneration, err := home.CurrentTaskGeneration(homeDir, taskID, fallbackGeneration)
			if err != nil {
				return fmt.Errorf("ready: reading task aggregate: %w", err)
			}

			// Emit the durable ready event marker.
			// The ready marker is written atomically (temp-file + rename).
			readyEvent, err := fleet.EmitReadyEvent(homeDir, taskID, eventID, metaGeneration)
			if err != nil {
				return fmt.Errorf("ready: emit: %w", err)
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "ready",
				Status:        "success",
				Data: MessageResult{
					Message: fmt.Sprintf("ready event emitted: task=%s event=%s gen=%s",
						taskID, readyEvent.EventID, metaGeneration),
				},
			})
		}),
	}

	configureContractCommand(cmd)
	cmd.Flags().StringVar(&eventID, "event-id", "", "Explicit event ID (auto-generated if empty)")
	return cmd
}
