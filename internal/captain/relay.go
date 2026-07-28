package captain

import "github.com/minhtri2710/munsu/internal/orchestrator"

type TerminalUplinkOutcome = orchestrator.TerminalUplinkOutcome
type ReceiptReconcileOutcome = orchestrator.ReceiptReconcileOutcome
type ReconcileResult = orchestrator.CaptainReconcileResult

const (
	OutcomeNoPending             = orchestrator.OutcomeNoPending
	OutcomeRelayed               = orchestrator.OutcomeRelayed
	OutcomeAlreadyAcked          = orchestrator.OutcomeAlreadyAcked
	OutcomeRelayFailed           = orchestrator.OutcomeRelayFailed
	OutcomeAckFailed             = orchestrator.OutcomeAckFailed
	OutcomeObligationCloseFailed = orchestrator.OutcomeObligationCloseFailed
)

func NewWatcherHooks(notification orchestrator.NotificationTransport, activation orchestrator.ActivationTransport) orchestrator.WatcherHooks {
	return orchestrator.NewCaptainWatcherHooks(notification, activation)
}
func ReconcileTerminalReceipts(captainHome, parentHome string) (*ReconcileResult, error) {
	return orchestrator.ReconcileTerminalReceipts(captainHome, parentHome)
}
func RelayTerminalReceipts(captainHome, parentHome string) (int, error) {
	return orchestrator.RelayTerminalReceipts(captainHome, parentHome)
}

func resolveParentHome(homeDir string) string { return orchestrator.ResolveCaptainParentHome(homeDir) }
func captainActivationHook(homeDir string, activation orchestrator.ActivationTransport) {
	orchestrator.CaptainActivationHook(homeDir, activation)
}
func reconcileHook(homeDir string, startup bool, transport orchestrator.NotificationTransport) error {
	return orchestrator.ReconcileCaptainHook(homeDir, startup, transport)
}
