package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
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
	transport := newSessionUplinkTransport()
	return newReportCmdWithNotifier(func(senderHome, receiverHome string, ref orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
		return orchestrator.NotifyParentWithTransport(senderHome, receiverHome, ref, transport)
	})
}

func newReportCmdWithNotifier(notify func(senderHome, receiverHome string, ref orchestrator.NotificationRef) orchestrator.UplinkNotifyResult) *cobra.Command {
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
			if !home.IsValidStatusState(state) {
				return usageError("invalid_argument",
					fmt.Sprintf("Valid states: %s", strings.Join(home.ValidStatusStates, ", ")),
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
					if err := fleet.VerifyDoneIdentity(targetHome, taskID, msg); err != nil {
						return fmt.Errorf("report: %w", err)
					}
					if err := fleet.CaptureTerminalIdentity(targetHome, taskID, msg); err != nil {
						return fmt.Errorf("report: %w", err)
					}
				} else if state != "blocked" && state != "failed" && state != "needs-decision" {
					if err := fleet.CaptureTerminalIdentity(targetHome, taskID, msg); err != nil {
						return fmt.Errorf("report: %w", err)
					}
				}
			}

			var receipt *orchestrator.WakeReceipt
			var uplinkResult *orchestrator.ReportResult
			if materialStates[state] && (role == "soldier" || role == "captain") {
				senderIdentity := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(taskID)
				senderRank := orchestrator.Rank(role)
				if role == "captain" {
					if identity, _, err := orchestrator.ReadHomeIdentity(homeDir); err == nil {
						senderIdentity = identity
					}
				}
				receiverIdentity, _, err := orchestrator.ReadHomeIdentity(parentHome)
				if err != nil {
					return fmt.Errorf("report: deriving receiver identity: %w", err)
				}
				receiverRank := orchestrator.RankCaptain
				if role == "captain" {
					receiverRank = orchestrator.RankGeneral
				}
				uplinkResult, err = orchestrator.Report(orchestrator.ReportRequest{
					SenderHome: senderHomeForRole(role, homeDir, parentHome), ReceiverHome: parentHome,
					SenderRank: senderRank, SenderIdentity: senderIdentity,
					ReceiverRank: receiverRank, ReceiverID: receiverIdentity,
					TaskID: taskID, Key: key, State: state, Message: msg,
					Notify: func(ref orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
						if resolveRingPolicy(ring, homeDir) == "no-ring" {
							return orchestrator.UplinkNotifyResult{Queued: true}
						}
						return notify(homeDir, parentHome, ref)
					},
				})
				if err != nil {
					return fmt.Errorf("report: %w", err)
				}
			} else {
				var err error
				receipt, err = orchestrator.DeliverWake(orchestrator.DeliverRequest{
					HomeDir: targetHome, ParentHome: parentHome, TaskID: taskID,
					State: state, Message: msg, Key: key, Role: role,
				})
				if err != nil {
					return fmt.Errorf("report: %w", err)
				}
			}

			// 1.6. For soldier review-ready/idle states: emit a durable ready event
			// and flush one pending command automatically.
			if role == "soldier" && state == "review-ready" {
				fallbackGeneration := ""
				if meta, metaErr := home.ReadMeta(homeDir, taskID); metaErr == nil {
					fallbackGeneration = meta["generation"]
				}
				metaGeneration, genErr := home.CurrentTaskGeneration(homeDir, taskID, fallbackGeneration)
				if genErr != nil {
					return fmt.Errorf("report: reading task aggregate: %w", genErr)
				}
				fleet.EmitReadyEvent(homeDir, taskID, "", metaGeneration)

				senderIdentity, _, _ := orchestrator.ReadHomeIdentity(homeDir)
				if senderIdentity == "" {
					senderIdentity = filepath.Base(homeDir)
				}
				if fr := fleet.FlushPendingSoldierCommands(homeDir, taskID, senderIdentity, newSessionSoldierEndpoints()); fr.Err != nil {
					fmt.Fprintf(os.Stderr, "review-ready flush: %v\n", fr.Err)
				}
			}

			var injectResult *ReportInjection
			watcherID := identifyWatcher(homeDir)
			var enqueueTimestamp, receiptTimestamp int64
			if uplinkResult != nil {
				outcome := "queued"
				if uplinkResult.Notified {
					outcome = "notified"
				}
				injectResult = &ReportInjection{Outcome: outcome}
				receiptTimestamp = time.Now().Unix()
			} else if receipt != nil {
				enqueueTimestamp = receipt.EnqueueUnix
				if receipt.ReceiptWritten {
					receiptTimestamp = time.Now().Unix()
				}
			}

			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "report",
				Status:        "success",
				Data: MessageResult{
					Message:          fmt.Sprintf("reported %s: %s (role=%s, task=%s)", state, msg, role, taskID),
					Injection:        injectResult,
					EnqueueTimestamp: enqueueTimestamp,
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

func senderHomeForRole(role, homeDir, parentHome string) string {
	if role == "soldier" {
		return parentHome
	}
	return homeDir
}

func resolveRingPolicy(ring, homeDir string) string {
	switch ring {
	case "ring":
		return "ring"
	case "no-ring":
		return "no-ring"
	default: // auto
		if orchestrator.ShouldBatch(homeDir) {
			return "no-ring"
		}
		return "ring"
	}
}
