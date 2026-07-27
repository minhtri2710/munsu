package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/spf13/cobra"
)

func newConsumeReadyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "consume-ready <task-id>",
		Short: "Consume ready events and flush pending commands for a soldier task",
		Long: `Consume ready events for a soldier task. This scans for durable ready event
markers, validates them, and flushes one pending command per valid ready event.

Call this when the captain receives a "ready-event:" notification from a soldier
(typically injected at turn boundaries). This is the captain-side handler for
event-driven busy soldier delivery.

Recovery path: captain converge also scans for ready events automatically.
The converge path is recovery-only — the primary path is the soldier's
'munsu ready' call which injects a pulse and this command consumes it.

Flags:
  --sender <identity>  Override sender identity (default: home basename)`,
		Args: ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			taskID := args[0]

			// Derive sender identity.
			senderIdentity, _ := cmd.Flags().GetString("sender")
			if senderIdentity == "" {
				senderIdentity, _, _ = mailbox.ReadHomeIdentity(ctx.Home)
				if senderIdentity == "" {
					senderIdentity = filepath.Base(ctx.Home)
				}
			}

			// Read generation from meta for staleness check.
			meta, err := task.ReadMeta(ctx.Home, taskID)
			if err != nil {
				return fmt.Errorf("consume-ready: reading meta for %s: %w", taskID, err)
			}
			metaGeneration := meta["generation"]

			flushed, err := captain.ConsumeAllReadyEvents(ctx.Home, taskID, senderIdentity, metaGeneration, newSessionSoldierEndpoints())
			if err != nil {
				return fmt.Errorf("consume-ready: %w", err)
			}

			// Also reconcile soldier pending (remove matched acks).
			if recErr := captain.ReconcileSoldierPending(ctx.Home, senderIdentity); recErr != nil {
				fmt.Fprintf(os.Stderr, "consume-ready: reconcile warning: %v\n", recErr)
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "consume-ready",
				Status:        "success",
				Data: contract.MessageResult{
					Message: fmt.Sprintf("consumed ready events for %s: %d command(s) flushed", taskID, flushed),
				},
			})
		}),
	}

	configureContractCommand(cmd)
	cmd.Flags().String("sender", "", "Sender identity (default: home basename)")
	return cmd
}
