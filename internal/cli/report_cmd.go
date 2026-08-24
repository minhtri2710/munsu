package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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

			// Ship tasks cannot use "resolved" as delivery completion.
			// Only "done" is the valid terminal delivery state for ship tasks.
			if state == "resolved" {
				meta, metaErr := home.ReadMeta(targetHome, taskID)
				if metaErr == nil && meta["kind"] == "ship" {
					return fmt.Errorf("report: ship task %s cannot use 'resolved' as delivery completion; use 'done' instead", taskID)
				}
			}

			// A soldier's terminal scout report is also the authoritative lifecycle
			// signal for the generation owned by its parent Captain. Commit that
			// transition before sending the uplink so teardown can observe the
			// canonical terminal phase without requiring --force.
			if role == "soldier" && state == "done" {
				if isScoutTask(parentHome, taskID) {
					if err := enforceScoutReportBudget(parentHome, taskID, time.Now()); err != nil {
						if budgetErr := new(orchestrator.ScoutBudgetError); errors.As(err, &budgetErr) {
							_ = home.AppendStatus(parentHome, taskID, "failed: scout runtime deadline evidence: "+formatScoutBudgetEvidence(budgetErr.Evidence))
						}
						return fmt.Errorf("report: scout completion rejected: %w", err)
					}
				}
				if err := completeScoutReport(parentHome, taskID); err != nil {
					return fmt.Errorf("report: completing scout lifecycle: %w", err)
				}
			}

			// Delivery truth is never captured or committed through the terminal
			// report path: terminal reports and retirement consume canonical
			// delivery authorization/outcome truth, and delivery execution runs
			// exclusively through the Fleet journaled Deliver operation.

			var receipt *orchestrator.WakeReceipt
			var uplinkResult *orchestrator.ReportResult
			if role == "soldier" && state == "done" && isScoutTask(parentHome, taskID) {
				var err error
				receipt, err = orchestrator.DeliverWake(orchestrator.DeliverRequest{
					HomeDir: homeDir, ParentHome: parentHome, TaskID: taskID,
					State: state, Message: msg, Key: key, Role: role,
				})
				if err != nil {
					return fmt.Errorf("report: delivering scout terminal wake: %w", err)
				}
			} else if materialStates[state] && (role == "soldier" || role == "captain") {
				senderIdentity := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(taskID)
				senderRank := orchestrator.Rank(role)
				if role == "captain" {
					if identity, _, err := orchestrator.ReadHomeIdentity(homeDir); err == nil {
						senderIdentity = identity
					}
				}
				// The receiving home's own provenance is the only authority on
				// its rank: a captain home under Captain dispatch, the General
				// home under direct General dispatch.
				receiverIdentity, receiverRank, err := orchestrator.ReadHomeIdentity(parentHome)
				if err != nil {
					return fmt.Errorf("report: deriving receiver identity: %w", err)
				}
				uplinkResult, err = orchestrator.Report(orchestrator.ReportRequest{
					SenderHome: senderHomeForRole(role, homeDir, parentHome), ReceiverHome: parentHome,
					SenderRank: senderRank, SenderIdentity: senderIdentity,
					ReceiverRank: receiverRank, ReceiverID: receiverIdentity,
					TaskID: taskID, Key: key, State: state, Message: msg,
					Notify: func(ref orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
						if resolveRingPolicy(ring, homeDir) == "no-ring" {
							return orchestrator.QueuedNotification()
						}
						return notify(homeDir, parentHome, ref)
					},
				})
				if err != nil {
					// This failure landed after the durable commit, so it must
					// not read as "the report did not happen": the receiver
					// has it. Re-running report is the repair rather than a
					// hazard -- it supersedes this record instead of adding a
					// second one, verified in
					// TestReReportingAfterANotifyFailureSupersedesInsteadOfDoubleWriting
					// -- and under direct General dispatch no recovery pass
					// retries the notification, so it is the only repair there.
					if errors.Is(err, orchestrator.ErrReportDurable) {
						return fmt.Errorf("report: %w -- the receiver already holds this report; re-running report for this state supersedes it rather than adding a second one, and is how to retry", err)
					}
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
				metaGeneration, genErr := currentTaskGeneration(homeDir, taskID, fallbackGeneration)
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

func isScoutTask(homeDir, taskID string) bool {
	h, err := home.Open(homeDir)
	if err != nil {
		return false
	}
	auth, err := taskauthority.NewCanonical(h)
	if err != nil {
		return false
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return false
	}
	agg, err := auth.Get(tid)
	return err == nil && agg.Definition.Kind == "scout"
}

func formatScoutBudgetEvidence(e orchestrator.ScoutBudgetEvidence) string {
	return fmt.Sprintf("outcome=%s budget_secs=%d started_at_unix=%d observed_at_unix=%d elapsed_secs=%d", e.Outcome, e.BudgetSecs, e.StartedAtUnix, e.ObservedAtUnix, e.ElapsedSecs)
}

func enforceScoutReportBudget(homeDir, taskID string, now time.Time) error {
	h, err := home.Open(homeDir)
	if err != nil {
		return err
	}
	auth, err := taskauthority.NewCanonical(h)
	if err != nil {
		return err
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return err
	}
	agg, err := auth.Get(tid)
	if err != nil {
		return err
	}
	if agg.Definition.Kind != "scout" {
		return nil
	}
	// Task-only/local report flows may not have a launch submission record; keep
	// their existing report semantics. When launch evidence exists, its timestamp
	// is authoritative and malformed evidence fails closed.
	if agg.LaunchEvidence == nil {
		return nil
	}
	if agg.LaunchEvidence.SubmittedAt <= 0 {
		return fmt.Errorf("%w: task=%s", orchestrator.ErrScoutBudgetMissingTimestamp, taskID)
	}
	_, err = orchestrator.EnforceScoutRuntimeBudget(agg.Definition.ScoutRuntimeBudgetSecs, agg.LaunchEvidence.SubmittedAt, now.Unix(), now)
	return err
}

func completeScoutReport(homeDir, taskID string) error {
	h, err := home.Open(homeDir)
	if errors.Is(err, home.ErrNotInitialized) {
		return nil
	}
	if err != nil {
		return err
	}
	auth, err := taskauthority.NewCanonical(h)
	if err != nil {
		return err
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return err
	}
	agg, err := auth.Get(tid)
	if errors.Is(err, taskauthority.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if agg.Definition.Kind != "scout" || agg.Phase == taskauthority.PhaseDone || agg.Phase == taskauthority.PhaseResolved || agg.Phase == taskauthority.PhaseRetired {
		return nil
	}
	req := taskauthority.CanonicalCompleteRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		To:           taskauthority.PhaseDone, Reason: "report: scout done",
	}
	op, err := newCanonicalOperation("report-scout-done", req)
	if err != nil {
		return err
	}
	if _, err := auth.Complete(op, req); err != nil {
		return err
	}
	updated, err := auth.Get(tid)
	if err != nil {
		return err
	}
	if err := projectTaskMeta(homeDir, updated, nil); err != nil {
		return err
	}
	return home.AppendStatus(homeDir, taskID, "done: report scout done")
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
