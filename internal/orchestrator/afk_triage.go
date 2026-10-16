package orchestrator

import (
	"math"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// afkTriageLeaseSeconds bounds how long a crashed daemon's claimed wakes stay
// out of the queue; a live daemon acks them within the same cycle.
const afkTriageLeaseSeconds = 60

// Digest holds the result of one wake triage cycle.
// It separates general-relevant (escalate) wakes from routine ones.
// LeaseID holds EventIDs until the caller acks them once the digest is durable.
type Digest struct {
	Escalated []WakeDigest
	Routines  []WakeDigest
	LeaseID   string
	EventIDs  []string
}

// WakeDigest summarizes a single wake entry for triage output.
type WakeDigest struct {
	Kind              string
	Key               string
	Payload           string
	Ref               string
	IsGeneralRelevant bool
}

// OneCycle claims every queued wake under a lease and classifies each entry.
// The wakes stay leased until the caller acks Digest.EventIDs, so a crash
// before the digest is written returns them to the queue when the lease
// expires. Returns nil digest (not an error) when nothing is queued.
// Consent-gating (state/.afk check) is the caller's responsibility.
// Process-event wakes are left in the queue: they belong to the supervision
// watcher's consumer, never to a digest.
func OneCycle(homeDir string) (*Digest, error) {
	claim, err := home.ClaimWakes(homeDir, "munsu:afk", afkTriageLeaseSeconds, math.MaxInt)
	if err != nil {
		return nil, err
	}
	if len(claim.Wakes) == 0 {
		return nil, nil
	}

	d := &Digest{LeaseID: claim.LeaseID}
	for _, rec := range claim.Wakes {
		d.EventIDs = append(d.EventIDs, rec.Epoch+":"+rec.Seq)
		wd := WakeDigest{
			Kind:    rec.Kind,
			Key:     rec.Key,
			Payload: rec.Payload,
			Ref:     rec.Ref,
			// Use classify to evaluate captain-relevance based on the payload.
			// Wake payloads are status-line notes; only needs-decision,
			// blocked and failed content escalates.
			IsGeneralRelevant: domain.GeneralRelevant(rec.Payload),
		}

		if wd.IsGeneralRelevant {
			d.Escalated = append(d.Escalated, wd)
		} else {
			d.Routines = append(d.Routines, wd)
		}
	}

	return d, nil
}
