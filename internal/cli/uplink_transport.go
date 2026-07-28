package cli

import (
	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

type sessionUplinkTransport struct {
	resolve func(string, string) (backend.Backend, string, error)
}

func newSessionUplinkTransport() orchestrator.NotificationTransport {
	return sessionUplinkTransport{resolve: backend.Resolve}
}

func (t sessionUplinkTransport) Notify(senderHome string, target afk.TargetResult, payload string) orchestrator.UplinkNotifyResult {
	bk, _, err := t.resolve(senderHome, "")
	if err != nil {
		return orchestrator.UplinkNotifyResult{Queued: true}
	}
	safe, _, err := afk.IsSafeInjectTarget(bk, target.Handle)
	if err != nil || !safe {
		return orchestrator.UplinkNotifyResult{Queued: true}
	}
	result := backend.SubmitPrompt(bk, target.Handle, payload)
	return orchestrator.UplinkNotifyResult{Acknowledged: result.Acknowledged(), Queued: !result.Acknowledged()}
}
