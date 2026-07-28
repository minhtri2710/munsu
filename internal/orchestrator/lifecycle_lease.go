package orchestrator

import (
	"github.com/minhtri2710/munsu/internal/home"
	"time"
)

type ClaimedWakeRecord = home.ClaimedWakeRecord
type ClaimResult = home.ClaimResult

func LeaseDir(h string) string          { return home.LeaseDir(h) }
func LeaseFilePath(h, id string) string { return home.LeaseFilePath(h, id) }
func ClaimWakes(h, c string, seconds, limit int) (*ClaimResult, error) {
	return home.ClaimWakes(h, c, seconds, limit)
}
func AckWakes(h, id string, events []string) error { return home.AckWakes(h, id, events) }
func reclaimExpiredLeases(h string) int            { return home.ReclaimExpiredLeases(h) }
func ClaimExpiryGrace() time.Duration              { return home.ClaimExpiryGrace() }
