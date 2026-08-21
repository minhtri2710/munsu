package orchestrator

import (
	"github.com/minhtri2710/munsu/internal/home"
)

type ClaimedWakeRecord = home.ClaimedWakeRecord
type ClaimResult = home.ClaimResult

func ClaimWakes(h, c string, seconds, limit int) (*ClaimResult, error) {
	result, err := home.ClaimWakes(h, c, seconds, limit)
	if err == nil {
		logWakeClaimObservation(result)
	}
	return result, err
}
func AckWakes(h, id string, events []string) error { return home.AckWakes(h, id, events) }
func ResolveWake(h, leaseID, eventID, summary string) error {
	return home.ResolveWake(h, leaseID, eventID, summary)
}
