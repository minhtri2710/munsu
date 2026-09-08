package orchestrator

import (
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
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

// RepresentativeWakeKey returns the wake key that best represents this cycle
// for repetition detection: the most frequent key, preferring escalated
// (general-relevant) wakes over routine ones. The bool is false when the cycle
// held no wakes. Frequency — not queue position — is what the wedge detector's
// repeat check needs, so a key that dominates the cycle is chosen even when it
// is not the first entry.
func (d *Digest) RepresentativeWakeKey() (string, bool) {
	if k, ok := mostFrequentKey(d.Escalated); ok {
		return k, true
	}
	return mostFrequentKey(d.Routines)
}

// mostFrequentKey returns the most frequent Key among entries. Ties resolve to
// the earliest-seen key (the entries are in cycle order and the comparison is
// strict), keeping the result deterministic. The bool is false when empty.
func mostFrequentKey(entries []WakeDigest) (string, bool) {
	if len(entries) == 0 {
		return "", false
	}
	counts := make(map[string]int, len(entries))
	for _, e := range entries {
		counts[e.Key]++
	}
	best := entries[0].Key
	for _, e := range entries {
		if counts[e.Key] > counts[best] {
			best = e.Key
		}
	}
	return best, true
}

// OneCycle drains the wake queue and classifies each entry.
// Uses the existing classify package to determine captain-relevance.
// Returns nil digest (not an error) when no wake queue exists or it is empty.
// Consent-gating (state/.afk check) is the caller's responsibility.
// Process-event wakes are left in the queue: they belong to the supervision
// watcher's consumer, never to a digest.
func OneCycle(homeDir string) (*Digest, error) {
	records, err := home.DrainWakesExcludingKind(homeDir, home.ProcessEventWakeKind)
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
