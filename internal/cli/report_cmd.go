package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/delivery"
	"github.com/minhtri2710/munsu/internal/event"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/mailbox"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/turnend"
	"github.com/spf13/cobra"
)

// materialStates are the status states that warrant waking a parent supervisor.
var materialStates = map[string]bool{
	"done":           true,
	"failed":         true,
	"needs-decision": true,
	"blocked":        true,
}

// injectedEvents tracks synthetic event IDs that have already been injected
// into the parent pane to prevent duplicate injection of the same event.
var injectedEvents sync.Map

// newReportCmd creates the `munsu report` command for rank-aware uplink status reporting.
func newReportCmd() *cobra.Command {
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
			statusLine := state + ": " + msg
			if key != "" {
				statusLine += " [key=" + key + "]"
			}

			// Determine target home and event/wake home based on role
			targetHome := homeDir // default: local
			eventHome := homeDir
			wakeHome := homeDir

			switch role {
			case "captain":
				if parentHome == "" {
					return fmt.Errorf("report: MUNSU_PARENT_STATUS not set for captain role")
				}
				targetHome = parentHome
				eventHome = parentHome
				wakeHome = parentHome
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

			// 1. Durable task status append
			if err := task.AppendStatus(targetHome, taskID, statusLine); err != nil {
				return fmt.Errorf("report: appending status: %w", err)
			}

			// 1.5. For soldier material states: write a durable Captain receipt
			// and initialize per-task obligations so teardown blocks until relay.
			// The receipt lives in captain-owned state under parentHome (MUNSU_PARENT_STATUS).
			if role == "soldier" && materialStates[state] && parentHome != "" {
				termKey := key
				if termKey == "" {
					termKey = "default"
				}
				// Write durable receipt in captain-owned state
				if err := turnend.WriteReceipt(parentHome, taskID, termKey, state, msg); err != nil {
					return operationError("receipt_write_failed",
						"Check MUNSU_PARENT_STATUS path permissions and structure",
						fmt.Sprintf("report: writing captain receipt: %v", err))
				}
				// Initialize per-task obligations (idempotent)
				if err := turnend.InitTaskObligations(parentHome, taskID, termKey); err != nil {
					return operationError("obligations_init_failed",
						"Check MUNSU_PARENT_STATUS path permissions and structure",
						fmt.Sprintf("report: init task obligations: %v", err))
				}
			}

			// 1.6. For soldier review-ready/idle states: emit a durable ready event
			// and flush one pending command automatically. This is the core integration
			// of event-driven busy soldier delivery: when the soldier reaches a turn
			// boundary (review-ready), it signals readiness and the next queued command
			// (if any) is sent via NotificationRef without blocking.
			//
			// No polling. No Captain status spam. The ready event marker also allows
			// converge/consume-ready to find it as a recovery path.
			if role == "soldier" && state == "review-ready" {
				// Emit durable ready event marker.
				meta, metaErr := task.ReadMeta(homeDir, taskID)
				if metaErr == nil {
					metaGeneration := meta["generation"]
					captain.EmitReadyEvent(homeDir, taskID, "", metaGeneration)

					// Flush one pending command (if any). Best-effort: if no pending,
					// this is a no-op. If backend fails, pending is retained for retry.
					senderIdentity, _, _ := mailbox.ReadHomeIdentity(homeDir)
					if senderIdentity == "" {
						senderIdentity = filepath.Base(homeDir)
					}
					if fr := captain.FlushPendingSoldierCommands(homeDir, taskID, senderIdentity); fr.Err != nil {
						// Failures are expected when there's no pending or backend is
						// temporarily unavailable. The pending remains for retry on the
						// next review-ready report or via converge/consume-ready.
						fmt.Fprintf(os.Stderr, "review-ready flush: %v\n", fr.Err)
					}
				}
			}

			// 2. Append to typed event log using synthetic event ID
			syntheticID := event.SyntheticEventID()
			if err := event.AppendWithID(eventHome, syntheticID, "task.status", taskID, key, statusLine); err != nil {
				fmt.Fprintf(os.Stderr, "warning: report: event append: %v\n", err)
			}

			// 3. For material states, enqueue a wake in the supervisor's queue
			var enqueueTimestamp int64
			if materialStates[state] {
				wakePayload := fmt.Sprintf("%s: %s [event=%d]", taskID, statusLine, syntheticID)
				enqueueTimestamp = time.Now().Unix()
				lifecycle.EnqueueWake(wakeHome, "signal", taskID, wakePayload)
			}

			// 4. Attempt parent pane injection for material states
			// When ring=ring or ring=auto (and not AFK-batching), inject a sentinel
			// message into the parent's terminal pane if the composer is empty.
			// The wake queue is the primary mechanism; injection is best-effort.
			ringDecision := resolveRingPolicy(ring, homeDir)
			var injectResult *contract.ReportInjection
			watcherID := identifyWatcher(homeDir)
			if ringDecision == "ring" && materialStates[state] {
				result := injectToParentPane(role, homeDir, parentHome, taskID, msg, state, syntheticID)
				injectResult = &contract.ReportInjection{
					Outcome: string(result.Outcome),
					Verdict: result.Verdict,
					Target:  result.Target,
					EventID: syntheticID,
					Error:   result.Error,
				}
			}

			// Determine receipt timestamp from the captain receipt (if written).
			var receiptTimestamp int64
			if role == "soldier" && materialStates[state] && parentHome != "" {
				receiptTimestamp = time.Now().Unix()
			}

			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "report",
				Status:        "success",
				Data: contract.MessageResult{
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

// injectToParentPane resolves the parent pane target and injects a sentinel
// message. Returns the typed InjectResult with outcome diagnostics.
func injectToParentPane(role, homeDir, parentHome, taskID, msg, state string, syntheticID uint64) afk.InjectResult {
	// Dedup: skip if this event was already injected.
	eventKey := fmt.Sprintf("%s/%s/%d", taskID, state, syntheticID)
	if _, loaded := injectedEvents.LoadOrStore(eventKey, true); loaded {
		return afk.InjectResult{Outcome: afk.OutcomeInjected, Target: "(deduped)"}
	}

	// Resolve the parent pane target.
	// For captains, resolve from the general's home (parentHome).
	// For soldiers, resolve from the current home (the soldier's home).
	targetHome := homeDir
	if role == "captain" && parentHome != "" {
		targetHome = parentHome
	}
	target, err := afk.ResolveTargetWithSource(targetHome)
	if err != nil || target.Handle == "" || target.Source == afk.Unsupported {
		return afk.InjectResult{
			Outcome: afk.OutcomeEndpointDead,
			Target:  target.Handle,
			Error:   fmt.Sprintf("target resolution: %v no-handle=%v unsupported=%v", err, target.Handle == "", target.Source == afk.Unsupported),
		}
	}

	// Obtain a session backend for SendKeys and Capture.
	bk, _, err := session.Resolve(homeDir, "")
	if err != nil {
		return afk.InjectResult{
			Outcome: afk.OutcomeEndpointDead,
			Target:  target.Handle,
			Error:   fmt.Sprintf("backend resolve: %v", err),
		}
	}

	// session.Backend satisfies afk.PaneCapture.
	var afkCap afk.PaneCapture = bk

	injectMsg := fmt.Sprintf("[report] %s: %s (task=%s)", state, msg, taskID)
	markedMsg := afk.Mark(injectMsg)
	if syntheticID > 0 {
		markedMsg = fmt.Sprintf("%s [event=%d]", markedMsg, syntheticID)
	}

	safe, verdict, err := afk.IsSafeInjectTarget(afkCap, target.Handle)
	if err != nil {
		return afk.InjectResult{Outcome: afk.OutcomeEndpointDead, Verdict: verdict.String(), Target: target.Handle, Error: err.Error()}
	}
	if !safe {
		return afk.InjectResult{Outcome: afk.OutcomeUnsafe, Verdict: verdict.String(), Target: target.Handle}
	}

	result := session.SubmitPrompt(bk, target.Handle, markedMsg)
	if !result.Acknowledged() {
		return afk.InjectResult{Outcome: afk.OutcomeBackendFailed, Verdict: verdict.String(), Target: target.Handle, Error: string(result.Status)}
	}
	return afk.InjectResult{Outcome: afk.OutcomeInjected, Verdict: verdict.String(), Target: target.Handle}

}
