package cli

import (
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

type captainWatcherAdapter struct{}

func (captainWatcherAdapter) Status(home string) fleet.WatcherStatus {
	return fleet.WatcherStatus(orchestrator.WatcherStatusSummary(home))
}

func (captainWatcherAdapter) Ensure(home string, hasChildWork bool) error {
	return orchestrator.EnsureWatcher(home, hasChildWork)
}
