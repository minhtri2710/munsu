package afk

import (
	"github.com/minhtri2710/munsu/internal/classify"
	"github.com/minhtri2710/munsu/internal/lifecycle"
)

// Digest holds the result of one wake triage cycle.
// It separates general-relevant (escalate) wakes from routine ones.
type Digest struct {
	Escalated []WakeDigest
	Routines  []WakeDigest
}

// WakeDigest summarizes a single wake entry for triage output.
type WakeDigest struct {
	Kind              string
	Key               string
	Payload           string
	IsGeneralRelevant bool
}

// OneCycle drains the wake queue and classifies each entry.
// Uses the existing classify package to determine captain-relevance.
// Returns nil digest (not an error) when no wake queue exists or it is empty.
// Consent-gating (state/.afk check) is the caller's responsibility.
func OneCycle(homeDir string) (*Digest, error) {
	records, err := lifecycle.DrainWakes(homeDir)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}

	d := &Digest{}
	for _, rec := range records {
		wd := WakeDigest{
			Kind:    rec.Kind,
			Key:     rec.Key,
			Payload: rec.Payload,
			// Use classify to evaluate captain-relevance based on the payload.
			// Wake payloads from afk escalation are status-line notes
			// ("PR merged", "build broken") and match the classify patterns
			// for done/failed/needs-decision content.
			IsGeneralRelevant: classify.GeneralRelevant(rec.Payload),
		}

		if wd.IsGeneralRelevant {
			d.Escalated = append(d.Escalated, wd)
		} else {
			d.Routines = append(d.Routines, wd)
		}
	}

	return d, nil
}
