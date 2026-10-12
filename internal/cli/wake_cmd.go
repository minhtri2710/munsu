package cli

import (
	"strconv"

	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/spf13/cobra"
)

func newWakeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wake",
		Short: "Manage wake queue with lease-based claim/ack",
	}

	claimCmd := &cobra.Command{
		Use:   "claim",
		Short: "Claim wakes from the queue under a lease",
		Args:  contractNoArgs,
		RunE: refuseInSoldierSeat("wake claim", withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			consumer, _ := cmd.Flags().GetString("consumer")
			leaseSec, _ := cmd.Flags().GetInt("lease-seconds")
			limit, _ := cmd.Flags().GetInt("limit")

			if consumer == "" {
				return usageError("invalid_argument", "Run `munsu wake claim --consumer <id>`", "--consumer is required")
			}

			if _, err := contractOutput(cmd); err != nil {
				return err
			}

			result, err := orchestrator.ClaimWakes(ctx.Home, consumer, leaseSec, limit)
			if err != nil {
				return operationError("internal", "Run `munsu wake claim --consumer "+consumer+"` again", err.Error())
			}

			state := "claimed"
			if len(result.Wakes) == 0 {
				state = "empty"
			} else if result.Reclaimed > 0 {
				state = "replayed"
			}

			// Build the actionable content for the response.
			wakes := make([]WakeClaimedWake, 0, len(result.Wakes))
			for _, w := range result.Wakes {
				wakes = append(wakes, WakeClaimedWake{
					WakeID:  w.Epoch + ":" + w.Seq,
					Kind:    w.Kind,
					Key:     w.Key,
					Payload: w.Payload,
				})
			}

			return writeContract(cmd, Response[WakeClaim]{
				SchemaVersion: SchemaVersion,
				Kind:          "wake.claim",
				Status:        "success",
				Data: WakeClaim{
					ClaimID:      result.LeaseID,
					Owner:        result.Consumer,
					State:        state,
					LeaseExpires: result.ExpiresAt,
					Reclaimed:    result.Reclaimed,
					Wakes:        wakes,
				},
			})
		})),
	}
	claimCmd.Flags().String("consumer", "", "Consumer identifier (required)")
	claimCmd.Flags().Int("lease-seconds", 60, "Lease duration in seconds")
	claimCmd.Flags().Int("limit", 10, "Maximum wakes to claim")

	resolveCmd := &cobra.Command{
		Use:   "resolve",
		Short: "Resolve one claimed wake with durable evidence",
		Args:  contractNoArgs,
		RunE: refuseInSoldierSeat("wake resolve", withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			claimID, _ := cmd.Flags().GetString("claim-id")
			eventID, _ := cmd.Flags().GetString("event-id")
			summary, _ := cmd.Flags().GetString("summary")
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			unaccepted, err := orchestrator.ResolveWake(ctx.Home, claimID, eventID, summary)
			if err != nil {
				return operationError("invalid_argument", "Use the exact claim-id and event-id from the wake prompt", err.Error())
			}
			return writeContract(cmd, Response[WakeAck]{
				SchemaVersion: SchemaVersion,
				Kind:          "wake.resolve",
				Status:        "success",
				Data:          newWakeAck(eventID, claimID, "resolved", unaccepted),
			})
		})),
	}
	resolveCmd.Flags().String("claim-id", "", "Exact wake lease ID")
	resolveCmd.Flags().String("event-id", "", "Exact wake event ID")
	resolveCmd.Flags().String("summary", "", "Non-empty resolution summary")

	ackCmd := &cobra.Command{
		Use:   "ack <lease-id> <event-id...>",
		Short: "Acknowledge claimed wakes by event ID (epoch:seq)",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return usageError("invalid_argument", "Run `munsu wake ack <lease-id> <event-id...>`", "Requires lease-id and at least one event-id")
			}
			return nil
		},
		RunE: refuseInSoldierSeat("wake ack", withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			leaseID := args[0]
			eventIDs := args[1:]

			if _, err := contractOutput(cmd); err != nil {
				return err
			}

			unaccepted, err := orchestrator.AckWakes(ctx.Home, leaseID, eventIDs)
			if err != nil {
				return operationError("internal", "Run `munsu wake ack "+leaseID+" ...` again", err.Error())
			}

			// Count acked
			ackedCount := strconv.Itoa(len(eventIDs))
			state := "acknowledged"

			return writeContract(cmd, Response[WakeAck]{
				SchemaVersion: SchemaVersion,
				Kind:          "wake.ack",
				Status:        "success",
				Data:          newWakeAck(ackedCount, leaseID, state, unaccepted),
			})
		})),
	}

	// Activation evidence proves new contracts are safe — drain compatibility sugar removed.
	configureContractCommand(claimCmd)
	configureContractCommand(resolveCmd)
	configureContractCommand(ackCmd)

	cmd.AddCommand(claimCmd)
	cmd.AddCommand(resolveCmd)
	cmd.AddCommand(ackCmd)

	return cmd
}

// newWakeAck builds the settle result. The wake is settled whether or not its
// report's work finished; unfinished reports ride along with the hint.
func newWakeAck(wakeID, claimID, state string, unaccepted []orchestrator.UnacceptedReport) WakeAck {
	ack := WakeAck{WakeID: wakeID, ClaimID: claimID, State: state}
	for _, u := range unaccepted {
		ack.UnacceptedReports = append(ack.UnacceptedReports, WakeUnacceptedReport{Key: u.Key, Payload: u.Payload, Ref: u.Ref, Reason: u.Reason})
	}
	if len(unaccepted) > 0 {
		ack.Hint = orchestrator.UnacceptedReportsHint
	}
	return ack
}
