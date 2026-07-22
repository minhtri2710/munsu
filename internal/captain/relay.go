// Package captain implements persistent domain supervisors (captains).
package captain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/supervision"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/turnend"
)

func init() {
	supervision.TerminalReconcileHook = reconcileHook
}

// reconcileHook is the supervision-watcher recovery hook running inside a
// captain home. It is called ONCE at watcher startup (recovery-only), not
// every cycle.
//
// If MUNSU_PARENT_STATUS is set, it attempts to relay pending mailbox
// envelopes (Captain→General) via the legacy terminal receipt relay for
// backward compatibility. If no parent is configured, it silently returns:
// the mailbox system handles durable pending without routing.
//
// General never requires parent-home. Soldier→Captain is local-only and
// never needs to relay to a parent.
func reconcileHook(homeDir string) error {
	parentHome := os.Getenv("MUNSU_PARENT_STATUS")
	if parentHome == "" || parentHome == homeDir {
		// No parent configured — this is normal for standalone or
		// General homes. Captain→General mailbox envelopes remain
		// pending and durable; they are visible through health checks.
		// No diagnostic wake is emitted (no `_config` storm).
		return nil
	}
	_, err := ReconcileTerminalReceipts(homeDir, parentHome)
	return err
}

// TerminalUplinkOutcome describes the result of reconciling one terminal receipt.
type TerminalUplinkOutcome string

const (
	// OutcomeNoPending means no un-acked receipts were found.
	OutcomeNoPending TerminalUplinkOutcome = "no-pending"
	// OutcomeRelayed means the receipt was fully relayed to General.
	OutcomeRelayed TerminalUplinkOutcome = "relayed"
	// OutcomeAlreadyAcked means the receipt was already acknowledged.
	OutcomeAlreadyAcked TerminalUplinkOutcome = "already-acked"
	// OutcomeRelayFailed means writing the relay status/event to General failed.
	OutcomeRelayFailed TerminalUplinkOutcome = "relay-failed"
	// OutcomeAckFailed means the relay succeeded but writing the ack in captain home failed.
	OutcomeAckFailed TerminalUplinkOutcome = "ack-failed"
	// OutcomeObligationCloseFailed means relay and ack succeeded but closing the obligation failed.
	OutcomeObligationCloseFailed TerminalUplinkOutcome = "obligation-close-failed"
)

// ReceiptReconcileOutcome describes the outcome for one receipt during reconciliation.
type ReceiptReconcileOutcome struct {
	TaskID  string
	TermKey string
	Outcome TerminalUplinkOutcome
	Err     error
}

// ReconcileResult aggregates the reconciliation sweep.
type ReconcileResult struct {
	Outcomes []ReceiptReconcileOutcome
}

// Relayed returns the number of receipts that were successfully relayed.
func (r *ReconcileResult) Relayed() int {
	n := 0
	for _, o := range r.Outcomes {
		if o.Outcome == OutcomeRelayed {
			n++
		}
	}
	return n
}

// Failed returns the number of receipts that failed to relay.
func (r *ReconcileResult) Failed() int {
	n := 0
	for _, o := range r.Outcomes {
		switch o.Outcome {
		case OutcomeRelayFailed, OutcomeAckFailed, OutcomeObligationCloseFailed:
			n++
		}
	}
	return n
}

// ReconcileTerminalReceipts is the shared reconciliation seam.
// It performs the full terminal uplink for receipts pending in captainHome:
// pending receipt -> General relay status/event -> Captain ack -> ReportRelay close.
// Returns detailed per-receipt outcomes.
// Idempotent: safe to call repeatedly. On any partial failure, preserves retryable
// receipt/obligation state and emits bounded diagnostics rather than false-closing.
func ReconcileTerminalReceipts(captainHome, parentHome string) (*ReconcileResult, error) {
	pending, err := turnend.ListPendingReceipts(captainHome)
	if err != nil {
		return nil, fmt.Errorf("listing pending receipts: %w", err)
	}
	if len(pending) == 0 {
		return &ReconcileResult{}, nil
	}

	result := &ReconcileResult{}
	for _, pr := range pending {
		outcome := reconcileOne(captainHome, parentHome, pr)
		result.Outcomes = append(result.Outcomes, outcome)
	}
	return result, nil
}

// reconcileOne processes a single pending receipt through the full relay chain.
// On partial failure, the receipt/obligation is preserved for retry and a typed
// failure outcome is returned instead of aborting.
func reconcileOne(captainHome, parentHome string, pr turnend.PendingReceipt) ReceiptReconcileOutcome {
	base := ReceiptReconcileOutcome{TaskID: pr.TaskID, TermKey: pr.TermKey}

	// Check if already acked (defensive — ListPendingReceipts filters these,
	// but filesystem race or concurrent ack could change state).
	if turnend.IsReceiptAcked(captainHome, pr.TaskID, pr.TermKey) {
		base.Outcome = OutcomeAlreadyAcked
		return base
	}

	// Resolve captain ID from provenance marker.
	captainID, err := readCaptainID(captainHome)
	if err != nil {
		base.Outcome = OutcomeRelayFailed
		base.Err = fmt.Errorf("reading captain id: %w", err)
		return base
	}

	// Step 1: Relay status to General.
	relayTaskID := fmt.Sprintf("captain:%s.relay-%s", captainID, pr.TaskID)
	relayLine := fmt.Sprintf("%s: soldier %s [key=%s]", pr.State, pr.TaskID, pr.TermKey)
	if err := task.AppendStatus(parentHome, relayTaskID, relayLine); err != nil {
		base.Outcome = OutcomeRelayFailed
		base.Err = fmt.Errorf("writing general relay status for %s/%s: %w", pr.TaskID, pr.TermKey, err)
		return base
	}

	// Step 2: Relay event to General for permanent durability.
	now := time.Now().UnixNano()
	eventContent := fmt.Sprintf("terminal_uplink_task=%s key=%s captain=%s relayed_at=%d\n",
		pr.TaskID, pr.TermKey, captainID, now)
	eventPath := filepath.Join(parentHome, "state", relayTaskID+".turnend")
	if err := os.MkdirAll(filepath.Dir(eventPath), 0755); err == nil {
		os.WriteFile(eventPath, []byte(eventContent), 0644)
	}

	// Step 3: Write ack in captain home (marks receipt as acknowledged).
	if err := turnend.WriteAck(captainHome, pr.TaskID, pr.TermKey); err != nil {
		base.Outcome = OutcomeAckFailed
		base.Err = fmt.Errorf("writing ack for %s/%s: %w", pr.TaskID, pr.TermKey, err)
		return base
	}

	// Step 4: Complete per-task obligation in captain home.
	if _, err := turnend.CompleteTaskObligation(captainHome, pr.TaskID, turnend.ReportRelay); err != nil {
		base.Outcome = OutcomeObligationCloseFailed
		base.Err = fmt.Errorf("completing obligation for %s: %w", pr.TaskID, err)
		return base
	}

	base.Outcome = OutcomeRelayed
	return base
}

// RelayTerminalReceipts is retained for backward compatibility.
// Prefer ReconcileTerminalReceipts for typed outcomes.
func RelayTerminalReceipts(captainHome, parentHome string) (int, error) {
	result, err := ReconcileTerminalReceipts(captainHome, parentHome)
	if err != nil {
		return 0, err
	}
	return result.Relayed(), nil
}

// readCaptainID reads the captain ID from the provenance marker.
func readCaptainID(captainHome string) (string, error) {
	markerPath := filepath.Join(captainHome, ProvenanceMarkerName)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		// Fallback: try to extract from directory name
		return filepath.Base(captainHome), nil
	}
	// Format: munsu-v2 <id>
	parts := strings.Fields(string(data))
	if len(parts) >= 2 {
		return parts[1], nil
	}
	return filepath.Base(captainHome), nil
}
