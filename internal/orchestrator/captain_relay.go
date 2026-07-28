package orchestrator

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/config"
)

type watcherHooks struct {
	notification NotificationTransport
	activation   ActivationTransport
}

func NewCaptainWatcherHooks(notification NotificationTransport, activation ActivationTransport) WatcherHooks {
	return watcherHooks{notification: notification, activation: activation}
}
func (h watcherHooks) Reconcile(homeDir string, startup bool) error {
	return ReconcileCaptainHook(homeDir, startup, h.notification)
}
func (h watcherHooks) Activate(homeDir string) { CaptainActivationHook(homeDir, h.activation) }

// resolveParentHome resolves the parent home directory for a captain context.
// Precedence:
//  1. MUNSU_PARENT_STATUS env var (if set and not equal to homeDir)
//  2. config/parent-home (if set and not equal to homeDir)
//  3. empty string (no parent)
//
// The function is safe to call from watcher hooks that may not inherit the
// original process environment (crash restart, plain `munsu watch --home`).
// It never returns homeDir as a valid parent (self-referencing guard).
func ResolveCaptainParentHome(homeDir string) string {
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
func CaptainActivationHook(homeDir string, activation ActivationTransport) {
	parentHome := ResolveCaptainParentHome(homeDir)
	if parentHome == "" {
		return
	}
	ActivateOnReceiptWithTransport(homeDir, parentHome, activation)
}

// reconcileHook recovers mailbox uplinks and legacy terminal receipts for a
// captain home on watcher startup and each polling cycle.
func ReconcileCaptainHook(homeDir string, startup bool, transport NotificationTransport) error {
	parentHome := ResolveCaptainParentHome(homeDir)
	if parentHome == "" {
		return nil
	}
	if transport == nil {
		return fmt.Errorf("uplink notification transport capability is required")
	}
	if _, err := Recover(RecoverRequest{
		SenderHome: homeDir, ReceiverHome: homeDir,
		ReceiverRank: RankCaptain, ForceNotify: startup,
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			return NotifyParentWithTransport(homeDir, homeDir, ref, transport)
		},
	}); err != nil {
		return err
	}
	if _, err := Recover(RecoverRequest{
		SenderHome: homeDir, ReceiverHome: parentHome,
		ReceiverRank: RankGeneral, ForceNotify: startup,
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			return NotifyParentWithTransport(homeDir, parentHome, ref, transport)
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
type CaptainReconcileResult struct {
	Outcomes []ReceiptReconcileOutcome
}

// Relayed returns the number of receipts that were successfully relayed.
func (r *CaptainReconcileResult) Relayed() int {
	n := 0
	for _, o := range r.Outcomes {
		if o.Outcome == OutcomeRelayed {
			n++
		}
	}
	return n
}

// Failed returns the number of receipts that failed to relay.
func (r *CaptainReconcileResult) Failed() int {
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
// Delegates to ReconcilePending for the core logic and maps
// outcomes to captain types for backward compatibility.
func ReconcileTerminalReceipts(captainHome, parentHome string) (*CaptainReconcileResult, error) {
	wdResult, err := ReconcilePending(captainHome, parentHome)
	if err != nil {
		return nil, err
	}

	result := &CaptainReconcileResult{}
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
