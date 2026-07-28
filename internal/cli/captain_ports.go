package cli

import (
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

type captainContinuityAdapter struct {
	notification orchestrator.NotificationTransport
}

func (a captainContinuityAdapter) Reconcile(parentHome string, captain fleet.CaptainEndpoint) (fleet.CaptainContinuityResult, error) {
	result := fleet.CaptainContinuityResult{}
	uplink, err := orchestrator.Recover(orchestrator.RecoverRequest{
		SenderHome: captain.Home, ReceiverHome: parentHome, ReceiverRank: orchestrator.RankGeneral,
		Notify: func(ref orchestrator.NotificationRef) orchestrator.UplinkNotifyResult {
			return orchestrator.NotifyParentWithTransport(captain.Home, parentHome, ref, a.notification)
		},
	})
	if err != nil {
		return result, err
	}
	result.Accepted, result.Notified, result.Queued = uplink.Accepted, uplink.Notified, uplink.Queued
	terminal, err := orchestrator.ReconcileTerminalReceipts(captain.Home, parentHome)
	if err != nil {
		return result, err
	}
	result.Relayed, result.Failed = terminal.Relayed(), terminal.Failed()
	return result, nil
}

func (a captainContinuityAdapter) ReconcileTerminal(parentHome string, captain fleet.CaptainEndpoint) (fleet.CaptainContinuityResult, error) {
	result := fleet.CaptainContinuityResult{}
	terminal, err := orchestrator.ReconcileTerminalReceipts(captain.Home, parentHome)
	if err != nil {
		return result, err
	}
	result.Relayed, result.Failed = terminal.Relayed(), terminal.Failed()
	return result, nil
}

type captainMessagingAdapter struct{}

func (captainMessagingAdapter) ReconcilePending(parentHome string, captain fleet.CaptainEndpoint, sender home.BoundSender) error {
	return fleet.ReconcileMailboxPending(parentHome, fleet.Info{ID: captain.ID, Home: captain.Home, Scope: captain.Scope, Project: captain.Project}, sender)
}
