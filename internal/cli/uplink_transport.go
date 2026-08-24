package cli

import (
	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

type sessionUplinkTransport struct {
	resolve  func(string, string) (backend.Backend, string, error)
	identity func(string) (string, error)
}

func newSessionUplinkTransport() orchestrator.NotificationTransport {
	return sessionUplinkTransport{resolve: backend.Resolve, identity: fleet.ResolveGeneralHomeBackend}
}

func (t sessionUplinkTransport) Notify(senderHome string, target orchestrator.TargetResult, payload string) orchestrator.UplinkNotifyResult {
	if t.identity == nil {
		return orchestrator.QueuedNotification()
	}
	backendName, err := t.identity(senderHome)
	if err != nil {
		return orchestrator.QueuedNotification()
	}
	bk, _, err := t.resolve(senderHome, backendName)
	if err != nil {
		return orchestrator.QueuedNotification()
	}
	safe, _, err := orchestrator.IsSafeInjectTarget(bk, target.Handle)
	if err != nil || !safe {
		return orchestrator.QueuedNotification()
	}
	result := backend.SubmitPrompt(bk, target.Handle, payload)
	if result.Acknowledged() {
		return orchestrator.AcknowledgedNotification()
	}
	if result.Status == backend.PromptBackendFailed || result.Err != nil {
		return orchestrator.FailedNotification(result.Err)
	}
	return orchestrator.QueuedNotification()
}
