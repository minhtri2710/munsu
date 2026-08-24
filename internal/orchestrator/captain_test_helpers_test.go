package orchestrator

type captainNotificationTransport struct {
	acknowledged bool
	calls        int
}

func (t *captainNotificationTransport) Notify(string, TargetResult, string) UplinkNotifyResult {
	t.calls++
	if t.acknowledged {
		return UplinkNotifyResult{Outcome: UplinkNotifyAcknowledged}
	}
	return UplinkNotifyResult{Outcome: UplinkNotifyQueued}
}
