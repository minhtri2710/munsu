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

type WatcherInfo struct {
	Status                 WatcherStatus
	CaptainID, CaptainHome string
}

// ConvergeWatcherStatus observes the watcher lease for each registered captain.
// The General uses this to observe Captain watcher health without reading
// Captain task state files. The lease file is the authoritative source of
// watcher identity for a home.
func ConvergeWatcherStatus(registered []Info, watcher CaptainWatcherPort) []WatcherInfo {
	var results []WatcherInfo
	if watcher == nil {
		return results
	}
	for _, captain := range registered {
		if captain.Home != "" {
			results = append(results, WatcherInfo{Status: watcher.LeaseStatus(captain.Home), CaptainID: captain.ID, CaptainHome: captain.Home})
		}
	}
	return results
}
