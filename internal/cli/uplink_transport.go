package cli

import (
	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/uplink"
)

type sessionUplinkTransport struct {
	resolve func(string, string) (backend.Backend, string, error)
}

func newSessionUplinkTransport() uplink.NotificationTransport {
	return sessionUplinkTransport{resolve: backend.Resolve}
}

func (t sessionUplinkTransport) Notify(senderHome string, target afk.TargetResult, payload string) uplink.NotifyResult {
	bk, _, err := t.resolve(senderHome, "")
	if err != nil {
		return uplink.NotifyResult{Queued: true}
	}
	safe, _, err := afk.IsSafeInjectTarget(bk, target.Handle)
	if err != nil || !safe {
		return uplink.NotifyResult{Queued: true}
	}
	result := backend.SubmitPrompt(bk, target.Handle, payload)
	return uplink.NotifyResult{Acknowledged: result.Acknowledged(), Queued: !result.Acknowledged()}
}
