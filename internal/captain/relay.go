// Package captain implements persistent domain supervisors (captains).
package captain

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

type watcherHooks struct {
	notification orchestrator.NotificationTransport
	activation   orchestrator.ActivationTransport
}

func NewWatcherHooks(notification orchestrator.NotificationTransport, activation orchestrator.ActivationTransport) orchestrator.WatcherHooks {
	return watcherHooks{notification: notification, activation: activation}
}
func (h watcherHooks) Reconcile(homeDir string, startup bool) error {
	return reconcileHook(homeDir, startup, h.notification)
}
func (h watcherHooks) Activate(homeDir string) { captainActivationHook(homeDir, h.activation) }

// resolveParentHome resolves the parent home directory for a captain context.
// Precedence:
//  1. MUNSU_PARENT_STATUS env var (if set and not equal to homeDir)
//  2. config/parent-home (if set and not equal to homeDir)
//  3. empty string (no parent)
//
// The function is safe to call from watcher hooks that may not inherit the
// original process environment (crash restart, plain `munsu watch --home`).
// It never returns homeDir as a valid parent (self-referencing guard).
func resolveParentHome(homeDir string) string {
	// 1. Check env var
	if p := os.Getenv("MUNSU_PARENT_STATUS"); p != "" && p != homeDir {
		return p
	}

	// 2. Fall back to config/parent-home (durable, survives env loss)
	if p, err := config.Get(homeDir, "parent-home"); err == nil && p != "" && p != homeDir {
		return p
	}

	// 3. No parent
	return ""
}

// captainActivationHook is the per-cycle activation hook running inside a
// captain home. It nudges the captain agent pane when new soldier receipts
// arrive, without waiting for General round-trip.
func captainActivationHook(homeDir string, activation orchestrator.ActivationTransport) {
	parentHome := resolveParentHome(homeDir)
	if parentHome == "" {
		return
	}
	orchestrator.ActivateOnReceiptWithTransport(homeDir, parentHome, activation)
}

// reconcileHook recovers mailbox uplinks and legacy terminal receipts for a
// captain home on watcher startup and each polling cycle.
func reconcileHook(homeDir string, startup bool, transport orchestrator.NotificationTransport) error {
	parentHome := resolveParentHome(homeDir)
	if parentHome == "" {
		return nil
	}
	if transport == nil {
		return fmt.Errorf("uplink notification transport capability is required")
	}
	if _, err := orchestrator.Recover(orchestrator.RecoverRequest{
		SenderHome: homeDir, ReceiverHome: homeDir,
		ReceiverRank: orchestrator.RankCaptain, ForceNotify: startup,
		Notify: func(ref orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
			return orchestrator.NotifyParentWithTransport(homeDir, homeDir, ref, transport)
		},
	}); err != nil {
		return err
	}
	if _, err := orchestrator.Recover(orchestrator.RecoverRequest{
		SenderHome: homeDir, ReceiverHome: parentHome,
		ReceiverRank: orchestrator.RankGeneral, ForceNotify: startup,
		Notify: func(ref orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
			return orchestrator.NotifyParentWithTransport(homeDir, parentHome, ref, transport)
		},
	}); err != nil {
		return err
	}
	// Legacy read compatibility: drain terminal receipts created before the
	// mailbox-only uplink path. New reports no longer create these artifacts.
	_, err := ReconcileTerminalReceipts(homeDir, parentHome)
	return err
}

// TerminalUplinkOutcome describes the result of reconciling one terminal receipt.
// These constants map to wakedelivery outcome strings for backward compatibility.
type TerminalUplinkOutcome string

const (
	OutcomeNoPending             TerminalUplinkOutcome = "no-pending"
	OutcomeRelayed               TerminalUplinkOutcome = "relayed"
	OutcomeAlreadyAcked          TerminalUplinkOutcome = "already-acked"
	OutcomeRelayFailed           TerminalUplinkOutcome = "relay-failed"
	OutcomeAckFailed             TerminalUplinkOutcome = "ack-failed"
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
// Delegates to orchestrator.ReconcilePending for the core logic and maps
// outcomes to captain types for backward compatibility.
func ReconcileTerminalReceipts(captainHome, parentHome string) (*ReconcileResult, error) {
	wdResult, err := orchestrator.ReconcilePending(captainHome, parentHome)
	if err != nil {
		return nil, err
	}

	result := &ReconcileResult{}
	for _, o := range wdResult.Outcomes {
		result.Outcomes = append(result.Outcomes, ReceiptReconcileOutcome{
			TaskID:  o.TaskID,
			TermKey: o.Key,
			Outcome: TerminalUplinkOutcome(o.Outcome),
			Err:     o.Err,
		})
	}
	return result, nil
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
