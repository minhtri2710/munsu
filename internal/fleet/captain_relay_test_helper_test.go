package fleet

import "github.com/minhtri2710/munsu/internal/orchestrator"

type captainNotificationTransport struct {
	acknowledged bool
	calls        int
}

func (t *captainNotificationTransport) Notify(string, orchestrator.TargetResult, string) orchestrator.UplinkNotifyResult {
	t.calls++
	return orchestrator.UplinkNotifyResult{Acknowledged: t.acknowledged, Queued: !t.acknowledged}
}
