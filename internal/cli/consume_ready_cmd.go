package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

func newConsumeReadyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "consume-ready <task-id>",
		Short: "Flush one pending command to a review-ready soldier task",
		Long: `Flush one pending command to a soldier task whose last status line is
review-ready: the soldier's 'munsu report review-ready' is its ready signal.
A command already sent and awaiting the soldier's ack is never sent again.

Call this when a soldier reports review-ready. This is the captain-side handler
for busy soldier delivery.

Flags:
  --sender <identity>  Override sender identity (default: home basename)`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			taskID := args[0]

			// Derive sender identity.
			senderIdentity, _ := cmd.Flags().GetString("sender")
			if senderIdentity == "" {
				var identErr error
				if senderIdentity, _, identErr = home.ReadHomeIdentity(ctx.Home); identErr != nil {
					return fmt.Errorf("consume-ready: deriving sender identity: %w", identErr)
				}
			}

			flushed, err := fleet.ConsumeReady(ctx.Home, taskID, senderIdentity, newSessionSoldierEndpoints())
			if err != nil {
				return fmt.Errorf("consume-ready: %w", err)
			}

			// Also reconcile soldier pending (remove matched acks).
			if recErr := fleet.ReconcileSoldierPending(ctx.Home, senderIdentity); recErr != nil {
				fmt.Fprintf(os.Stderr, "consume-ready: reconcile warning: %v\n", recErr)
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "consume-ready",
				Status:        "success",
				Data: MessageResult{
					Message: fmt.Sprintf("consumed ready for %s: %d command(s) flushed", taskID, flushed),
				},
			})
		}),
	}

	configureContractCommand(cmd)
	cmd.Flags().String("sender", "", "Sender identity (default: home basename)")
	return cmd
}
