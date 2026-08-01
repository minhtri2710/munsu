package cli

import (
	"strconv"
	"strings"

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
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			consumer, _ := cmd.Flags().GetString("consumer")
			leaseSec, _ := cmd.Flags().GetInt("lease-captains")
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

			// Build wake IDs for the response
			var wakeIDs []string
			for _, w := range result.Wakes {
				wakeIDs = append(wakeIDs, w.Epoch+":"+w.Seq)
			}

			return writeContract(cmd, Response[WakeClaim]{
				SchemaVersion: SchemaVersion,
				Kind:          "wake.claim",
				Status:        "success",
				Data: WakeClaim{
					WakeID:       strings.Join(wakeIDs, ","),
					ClaimID:      result.LeaseID,
					Owner:        result.Consumer,
					State:        state,
					LeaseExpires: result.ExpiresAt,
					Reclaimed:    result.Reclaimed,
				},
			})
		}),
	}
	claimCmd.Flags().String("consumer", "", "Consumer identifier (required)")
	claimCmd.Flags().Int("lease-captains", 60, "Lease duration in captains")
	claimCmd.Flags().Int("limit", 10, "Maximum wakes to claim")

	resolveCmd := &cobra.Command{
		Use:   "resolve",
		Short: "Resolve one claimed wake with durable evidence",
		Args:  contractNoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			claimID, _ := cmd.Flags().GetString("claim-id")
			eventID, _ := cmd.Flags().GetString("event-id")
			summary, _ := cmd.Flags().GetString("summary")
			if _, err := contractOutput(cmd); err != nil {
				return err
			}
			if err := orchestrator.ResolveWake(ctx.Home, claimID, eventID, summary); err != nil {
				return operationError("invalid_argument", "Use the exact claim-id and event-id from the wake prompt", err.Error())
			}
			return writeContract(cmd, Response[WakeAck]{
				SchemaVersion: SchemaVersion,
				Kind:          "wake.resolve",
				Status:        "success",
				Data:          WakeAck{WakeID: eventID, ClaimID: claimID, State: "resolved"},
			})
		}),
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
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			leaseID := args[0]
			eventIDs := args[1:]

			if _, err := contractOutput(cmd); err != nil {
				return err
			}

			if err := orchestrator.AckWakes(ctx.Home, leaseID, eventIDs); err != nil {
				return operationError("internal", "Run `munsu wake ack "+leaseID+" ...` again", err.Error())
			}

			// Count acked
			ackedCount := strconv.Itoa(len(eventIDs))
			state := "acknowledged"

			return writeContract(cmd, Response[WakeAck]{
				SchemaVersion: SchemaVersion,
				Kind:          "wake.ack",
				Status:        "success",
				Data: WakeAck{
					WakeID:  ackedCount,
					ClaimID: leaseID,
					State:   state,
				},
			})
		}),
	}

	// Activation evidence proves new contracts are safe — drain compatibility sugar removed.
	configureContractCommand(claimCmd)
	configureContractCommand(resolveCmd)
	configureContractCommand(ackCmd)

	drainCmd := &cobra.Command{
		Use:   "drain",
		Short: "Drain queued wakes safely under a lease",
		Args:  contractNoArgs,
		RunE:  withHome(runWakeDrain),
	}
	drainCmd.Flags().String("consumer", "drain", "Consumer identifier")
	drainCmd.Flags().Int("limit", 0, "Maximum wakes to drain (0 = all)")
	configureContractCommand(drainCmd)

	cmd.AddCommand(claimCmd)
	cmd.AddCommand(resolveCmd)
	cmd.AddCommand(ackCmd)
	cmd.AddCommand(drainCmd)

	return cmd
}

// drainLeaseSeconds bounds the per-batch lease so a crashed drain leaves
// claimed wakes reclaimable after grace, never lost.
const drainLeaseSeconds = 60

// drainChunk is the per-iteration claim size; the loop is bounded by both
// the remaining budget and a hard iteration cap.
const (
	drainChunk         = 50
	maxDrainIterations = 10000
)

// runWakeDrain drains queued wakes safely: each batch is claimed under a
// lease and immediately acknowledged, so a crash between claim and ack
// leaves the wakes reclaimable rather than lost. Every drained record is
// surfaced in the structured response so claimed material evidence is
// never swallowed. Shared by `munsu wake drain` and the hidden root alias
// `munsu wake-drain`.
func runWakeDrain(cmd *cobra.Command, _ []string, ctx Ctx) error {
	consumer, _ := cmd.Flags().GetString("consumer")
	if consumer == "" {
		consumer = "drain"
	}
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 0 {
		limit = 0
	}
	if _, err := contractOutput(cmd); err != nil {
		return err
	}

	budget := limit
	drained := 0
	reclaimed := 0
	var records []WakeDrainRecord

	for iteration := 0; iteration < maxDrainIterations; iteration++ {
		want := drainChunk
		if budget > 0 && want > budget {
			want = budget
		}
		result, err := orchestrator.ClaimWakes(ctx.Home, consumer, drainLeaseSeconds, want)
		if err != nil {
			return operationError("internal", "Run `munsu wake drain` again", err.Error())
		}
		reclaimed += result.Reclaimed
		if len(result.Wakes) == 0 {
			break
		}

		eventIDs := make([]string, 0, len(result.Wakes))
		for _, w := range result.Wakes {
			eventIDs = append(eventIDs, w.Epoch+":"+w.Seq)
			records = append(records, WakeDrainRecord{
				WakeID:  w.Epoch + ":" + w.Seq,
				Kind:    w.Kind,
				Key:     w.Key,
				Payload: w.Payload,
			})
		}
		if err := orchestrator.AckWakes(ctx.Home, result.LeaseID, eventIDs); err != nil {
			// Fail loud rather than swallow: the claim stays in its lease
			// and is reclaimable after grace.
			return operationError("internal", "Lease "+result.LeaseID+" remains for reclaim; run `munsu wake drain` again", err.Error())
		}
		drained += len(eventIDs)
		if budget > 0 {
			budget -= len(eventIDs)
			if budget <= 0 {
				break
			}
		}
	}

	state := "drained"
	if drained == 0 {
		state = "empty"
	}
	remaining := 0
	if orchestrator.HasQueuedWakes(ctx.Home) {
		remaining = countQueuedWakes(ctx.Home)
	}

	return writeContract(cmd, Response[WakeDrain]{
		SchemaVersion: SchemaVersion,
		Kind:          "wake.drain",
		Status:        "success",
		Data: WakeDrain{
			Consumer:  consumer,
			State:     state,
			Drained:   drained,
			Reclaimed: reclaimed,
			Remaining: remaining,
			Records:   records,
		},
	})
}

// newWakeDrainAliasCmd returns the hidden root-level legacy alias
// `munsu wake-drain`, kept because guard remediations and supervision
// docs still instruct it. It runs the same safe lease-backed drain.
func newWakeDrainAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "wake-drain",
		Short:  "Drain queued wakes (legacy alias for `munsu wake drain`)",
		Hidden: true,
		Args:   contractNoArgs,
		RunE:   withHome(runWakeDrain),
	}
	cmd.Flags().String("consumer", "drain", "Consumer identifier")
	cmd.Flags().Int("limit", 0, "Maximum wakes to drain (0 = all)")
	configureContractCommand(cmd)
	return cmd
}
