package fleet

import "github.com/minhtri2710/munsu/internal/home"

type CaptainEndpoint struct{ ID, Home, Scope, Project string }

type CaptainContinuityResult struct{ Accepted, Notified, Queued int }

type CaptainContinuityPort interface {
	Reconcile(parentHome string, captain CaptainEndpoint) (CaptainContinuityResult, error)
}

type CaptainWatcherPort interface {
	Status(home string) WatcherStatus
	Ensure(home string, hasChildWork bool) error
	LeaseStatus(home string) WatcherStatus
}

type CaptainMessagingPort interface {
	ReconcilePending(parentHome string, captain CaptainEndpoint, sender home.BoundSender) error
}
