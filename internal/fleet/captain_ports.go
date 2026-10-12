package fleet

import "github.com/minhtri2710/munsu/internal/home"

type CaptainEndpoint struct{ ID, Home, Scope, Project string }

type CaptainWatcherPort interface {
	Status(home string) WatcherStatus
	Ensure(home string, hasChildWork bool) error
}

type CaptainMessagingPort interface {
	ReconcilePending(parentHome string, captain CaptainEndpoint, sender home.BoundSender) error
}
