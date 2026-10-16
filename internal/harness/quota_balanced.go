package harness

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// quotaProvider maps harness names to their quota-axi provider identifiers.
var quotaProvider = map[string]string{
	"claude": "claude",
	"codex":  "codex",
	"pi":     "pi",
	"grok":   "grok",
}

// generalWindowIDs maps harness names to their GENERAL quota window IDs
// (model-scoped windows like "model:codex_bengalfox:*" are excluded).
var generalWindowIDs = map[string][]string{
	"claude": {"five_hour", "seven_day"},
	"codex":  {"five_hour", "weekly"},
	"pi":     {"five_hour", "seven_day"},
	"grok":   {"five_hour", "seven_day"},
}

// quotaData represents the JSON output from quota-axi --json.
type quotaData struct {
	Providers []quotaProviderData `json:"providers"`
}

// quotaProviderData represents one provider's quota state.
type quotaProviderData struct {
	Provider string            `json:"provider"`
	State    quotaState        `json:"state"`
	Windows  []quotaWindowData `json:"windows"`
}

// quotaState represents the state sub-object.
type quotaState struct {
	Status string `json:"status"`
}

// quotaWindowData represents a quota window.
type quotaWindowData struct {
	ID               string  `json:"id"`
	Kind             string  `json:"kind"`
	PercentRemaining float64 `json:"percentRemaining"`
}

// quotaBalanced runs the configured quota-axi and picks a candidate from its
// report. Any failure is returned, never answered with the first candidate.
func quotaBalanced(tool string, candidates []DispatchCandidate) (DispatchCandidate, error) {
	output, err := exec.Command(tool, "--json").Output()
	if err != nil {
		return DispatchCandidate{}, fmt.Errorf("quota-axi %s: %w", tool, err)
	}
	return pickByQuota(candidates, output)
}

// pickByQuota prefers the candidate with the highest minimum remaining GENERAL
// quota, earliest on a tie. A stale report wins over a fresh one only when its
// minimum is at least staleClearMargin points higher.
func pickByQuota(candidates []DispatchCandidate, quotaJSON []byte) (DispatchCandidate, error) {
	var qd quotaData
	if err := json.Unmarshal(quotaJSON, &qd); err != nil {
		return DispatchCandidate{}, fmt.Errorf("quota-axi output: %w", err)
	}

	type scored struct {
		index int
		min   float64
	}
	var fresh, stale *scored
	for i, c := range candidates {
		name, ok := quotaProvider[c.Harness]
		if !ok {
			continue
		}
		var provider *quotaProviderData
		for j := range qd.Providers {
			if qd.Providers[j].Provider == name {
				provider = &qd.Providers[j]
				break
			}
		}
		if provider == nil {
			continue
		}
		found := false
		var min float64
		for _, w := range provider.Windows {
			if w.Kind == "model" {
				continue
			}
			for _, gid := range generalWindowIDs[c.Harness] {
				if w.ID == gid && (!found || w.PercentRemaining < min) {
					found, min = true, w.PercentRemaining
				}
			}
		}
		if !found {
			continue
		}
		best := &stale
		if provider.State.Status == "fresh" {
			best = &fresh
		}
		if *best == nil || min > (*best).min {
			*best = &scored{index: i, min: min}
		}
	}

	const staleClearMargin = 20.0
	pick := fresh
	if pick == nil || (stale != nil && stale.min >= fresh.min+staleClearMargin) {
		pick = stale
	}
	if pick == nil {
		return DispatchCandidate{}, fmt.Errorf("quota-axi reported no general quota window for any candidate harness")
	}
	return candidates[pick.index], nil
}
