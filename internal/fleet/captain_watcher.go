package fleet

type WatcherStatus string

const (
	WatcherRunning WatcherStatus = "running"
	WatcherStopped WatcherStatus = "stopped"
	WatcherAbsent  WatcherStatus = "absent"
)

func inFlightSoldierPath(captainHome string) bool {
	ids, err := inFlightSoldierIDs(captainHome)
	return err == nil && len(ids) > 0
}
