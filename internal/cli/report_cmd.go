package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/delivery"
	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/wakedelivery"
	"github.com/spf13/cobra"
)

// materialStates are the status states that warrant waking a parent supervisor.
var materialStates = map[string]bool{
	"done":           true,
	"failed":         true,
	"needs-decision": true,
	"blocked":        true,
}

// newReportCmd creates the `munsu report` command for rank-aware uplink status reporting.
func newReportCmd() *cobra.Command {
	return newReportCmdWithInjector(wakedelivery.InjectToParentPane)
}

// newReportCmdWithInjector creates the report command with an injectable injectFn
// for deterministic testing of injection outcomes.
func newReportCmdWithInjector(injectFn func(role, homeDir, parentHome, taskID, msg, state string, syntheticID uint64) afk.InjectResult) *cobra.Command {
	var key string
	var ring string // "auto" | "ring" | "no-ring"

	cmd := &cobra.Command{
		Use:   "report <state> <msg>",
		Short: "Report status up the rank hierarchy (report up, send down)",
		Long: `Report status with rank-aware routing via MUNSU_ROLE:

  soldier  -> appends to task .status in the current home (MUNSU_HOME)
  captain  -> appends to General home state/captain:<id>.status (MUNSU_PARENT_STATUS)
  general  -> local append only (same as soldier)
Also writes to the typed event log, enqueues a wake for material states
(done, failed, needs-decision, blocked), and injects a sentinel message
directly into the parent terminal pane when the composer is safe.

Use 'munsu send' for downlink steering; 'munsu report' for uplink status.`,
		Args: ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			state := args[0]
			msg := args[1]

			// Validate state
			if !task.IsValidStatusState(state) {
				return usageError("invalid_argument",
					fmt.Sprintf("Valid states: %s", strings.Join(task.ValidStatusStates, ", ")),
					fmt.Sprintf("Invalid status state %q", state))
			}

			// Resolve role and identity from env
			role := os.Getenv("MUNSU_ROLE")
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

			parentHome := os.Getenv("MUNSU_PARENT_STATUS")

			// Determine target home for identity capture based on role
			targetHome := homeDir // default: local
			switch role {
			case "captain":
				if parentHome == "" {
					return fmt.Errorf("report: MUNSU_PARENT_STATUS not set for captain role")
				}
				targetHome = parentHome
			}

			// 0. For soldier material states with a PR URL in the message:
			// capture and persist delivery identity before any terminal report
			// success. Provider/identity failure fails closed: no status, receipt,
			// ack reset, event, or wake is produced. Retry is idempotent.
			//
			// Only "done" gates on provider confirmation. Blocked/failed/needs-decision
			// reports skip identity capture — they are intermediate/error states, not terminal.
			if role == "soldier" && materialStates[state] {
				if state == "done" {
					if err := delivery.VerifyDoneIdentity(targetHome, taskID, msg); err != nil {
						return fmt.Errorf("report: %w", err)
					}
					if err := delivery.CaptureTerminalIdentity(targetHome, taskID, msg); err != nil {
						return fmt.Errorf("report: %w", err)
					}
				} else if state != "blocked" && state != "failed" && state != "needs-decision" {
					if err := delivery.CaptureTerminalIdentity(targetHome, taskID, msg); err != nil {
						return fmt.Errorf("report: %w", err)
					}
				}
			}

			// 1. Deliver wake through the consolidated pipeline:
			// status → receipt → events → wake. Handles fail-closed ordering
			// and best-effort event append internally.
			receipt, err := wakedelivery.DeliverWake(wakedelivery.DeliverRequest{
				HomeDir:    homeDir,
				ParentHome: parentHome,
				TaskID:     taskID,
				State:      state,
				Message:    msg,
				Key:        key,
				Role:       role,
			})
			if err != nil {
				return fmt.Errorf("report: %w", err)
			}

			// 1.6. For soldier review-ready/idle states: emit a durable ready event
			// and flush one pending command automatically.
			if role == "soldier" && state == "review-ready" {
				meta, metaErr := task.ReadMeta(homeDir, taskID)
				if metaErr == nil {
					metaGeneration := meta["generation"]
					captain.EmitReadyEvent(homeDir, taskID, "", metaGeneration)

					senderIdentity, _, _ := mailbox.ReadHomeIdentity(homeDir)
					if senderIdentity == "" {
						senderIdentity = filepath.Base(homeDir)
					}
					if fr := captain.FlushPendingSoldierCommands(homeDir, taskID, senderIdentity); fr.Err != nil {
						fmt.Fprintf(os.Stderr, "review-ready flush: %v\n", fr.Err)
					}
				}
			}

			// 2. Attempt parent pane injection for material states
			ringDecision := resolveRingPolicy(ring, homeDir)
			var injectResult *contract.ReportInjection
			watcherID := identifyWatcher(homeDir)
			if ringDecision == "ring" && materialStates[state] {
				result := injectFn(role, homeDir, parentHome, taskID, msg, state, receipt.EventID)
				injectResult = &contract.ReportInjection{
					Outcome: string(result.Outcome),
					Verdict: result.Verdict,
					Target:  result.Target,
					EventID: receipt.EventID,
					Error:   result.Error,
				}
			}

			// Receipt timestamp mirrors when the captain receipt was written.
			var receiptTimestamp int64
			if receipt.ReceiptWritten {
				receiptTimestamp = time.Now().Unix()
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "report",
				Status:        "success",
				Data: contract.MessageResult{
					Message:          fmt.Sprintf("reported %s: %s (role=%s, task=%s)", state, msg, role, taskID),
					Injection:        injectResult,
					EnqueueTimestamp: receipt.EnqueueUnix,
					ReceiptTimestamp: receiptTimestamp,
					WatcherIdentity:  watcherID,
				},
			})
		},
	}

	configureContractCommand(cmd)
	cmd.Flags().StringVar(&key, "key", "", "Optional status key/slug for correlation and idempotency")
	cmd.Flags().StringVar(&ring, "ring", "auto", "Ring policy: auto, ring, no-ring")
	return cmd
}

// newNotifyCmd creates the `munsu notify` alias for `munsu report`.
func newNotifyCmd() *cobra.Command {
	reportCmd := newReportCmd()
	notifyCmd := &cobra.Command{
		Use:   "notify <state> <msg>",
		Short: "Alias for 'munsu report'",
		Long:  `'munsu notify' is an alias for 'munsu report'. See 'munsu report --help'.`,
		Args:  ExactArgs(2),
		RunE:  reportCmd.RunE,
	}
	notifyCmd.Flags().AddFlagSet(reportCmd.Flags())
	return notifyCmd
}

func resolveRingPolicy(ring, homeDir string) string {
	switch ring {
	case "ring":
		return "ring"
	case "no-ring":
		return "no-ring"
	default: // auto
		if afk.ShouldBatch(homeDir) {
			return "no-ring"
		}
		return "ring"
	}
}
