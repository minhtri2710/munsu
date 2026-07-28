package cli

import (
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/backend"
)

type sessionUplinkTransport struct {
	resolve func(string, string) (backend.Backend, string, error)
}

func newSessionUplinkTransport() orchestrator.NotificationTransport {
	return sessionUplinkTransport{resolve: backend.Resolve}
}

func (t sessionUplinkTransport) Notify(senderHome string, target orchestrator.TargetResult, payload string) orchestrator.UplinkNotifyResult {
	bk, _, err := t.resolve(senderHome, "")
	if err != nil {
		return orchestrator.UplinkNotifyResult{Queued: true}
	}
	safe, _, err := orchestrator.IsSafeInjectTarget(bk, target.Handle)
	if err != nil || !safe {
		return orchestrator.UplinkNotifyResult{Queued: true}
	}
	result := backend.SubmitPrompt(bk, target.Handle, payload)
	return orchestrator.UplinkNotifyResult{Acknowledged: result.Acknowledged(), Queued: !result.Acknowledged()}
}
