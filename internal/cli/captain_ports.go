package cli

import (
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
)

type captainMessagingAdapter struct{}

func (captainMessagingAdapter) ReconcilePending(parentHome string, captain fleet.CaptainEndpoint, sender home.BoundSender) error {
	return fleet.ReconcileMailboxPending(parentHome, fleet.Info{ID: captain.ID, Home: captain.Home, Scope: captain.Scope, Project: captain.Project}, sender)
}
