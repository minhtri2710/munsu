package cli

import (
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/spf13/cobra"
)

func newEventCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "event",
		Short: "Manage typed event log",
	}

	appendCmd := newEventAppendCmd()

	cmd.AddCommand(appendCmd)
	return cmd
}

func newEventAppendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "append <event-id>",
		Short: "Append a typed event to the event log",
		Args:  contractArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if _, err := contractOutput(cmd); err != nil {
				return err
			}

			eventType, _ := cmd.Flags().GetString("type")
			producer, _ := cmd.Flags().GetString("producer")
			key, _ := cmd.Flags().GetString("key")
			payload, _ := cmd.Flags().GetString("json")

			if payload == "" {
				payload, _ = cmd.Flags().GetString("toon")
			}

			if eventType == "" {
				return usageError("invalid_argument", "Run `munsu event append --help`", "Flag --type is required")
			}

			id, err := orchestrator.Append(ctx.Home, eventType, producer, key, payload)
			if err != nil {
				return operationError("internal", "Run `munsu event append` again", err.Error())
			}

			return writeContract(cmd, Response[EventAppend]{
				SchemaVersion: SchemaVersion,
				Kind:          "event.append",
				Status:        "success",
				Data: EventAppend{
					EventID:   id,
					Type:      eventType,
					Synthetic: false,
				},
			})
		}),
	}

	configureContractCommand(cmd)
	cmd.Flags().String("type", "", "Event type (required)")
	cmd.Flags().String("producer", "", "Event producer identifier")
	cmd.Flags().String("key", "", "Optional correlation/idempotency key")
	cmd.Flags().String("json", "", "JSON payload")
	cmd.Flags().String("toon", "", "TOON payload (alternative to --json)")

	return cmd
}
