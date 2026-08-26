// Package harness detects the running agent harness and resolves soldier/captain
// harness assignments from configuration.
package harness

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
)

// QuotaAxiProvider sources quota data for balanced selection.
type QuotaAxiProvider interface {
	// Run executes the quota provider and returns raw JSON output.
	Run() (string, error)
}

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

// defaultQuotaProvider runs quota-axi --json via the real CLI tool.
type defaultQuotaProvider struct{}

func (p *defaultQuotaProvider) Run() (string, error) {
	cmd := exec.Command("quota-axi", "--json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("quota-axi: %w", err)
	}
	return string(output), nil
}

// quotaSelector selects a provider harness from candidates using quota data.
// Unexported until two real provider adapters validate the interface shape.
type quotaSelector interface {
	// selectCandidate returns the best candidate from the list.
	// Returns empty DispatchCandidate if the list is empty.
	selectCandidate(candidates []DispatchCandidate) DispatchCandidate
}

// quotaBalancedSelector selects candidates based on quota-axi data,
// preferring the provider with the highest minimum remaining GENERAL quota.
// Falls back to first candidate when quota data is unavailable.
type quotaBalancedSelector struct {
	provider QuotaAxiProvider
}

// selectCandidate picks the best candidate using quota-axi data.
// Falls back to first candidate on any error or when no data is available.
func (s *quotaBalancedSelector) selectCandidate(candidates []DispatchCandidate) DispatchCandidate {
	if len(candidates) == 0 {
		return DispatchCandidate{}
	}

	quotaJSON, err := s.provider.Run()
	if err != nil {
		return candidates[0]
	}

	var qd quotaData
	if err := json.Unmarshal([]byte(quotaJSON), &qd); err != nil {
		return candidates[0]
	}

	if len(qd.Providers) == 0 {
		return candidates[0]
	}

	type candidate struct {
		index int
		min   float64
		fresh bool
	}

	var scored []candidate
	for i, c := range candidates {
		providerName, ok := quotaProvider[c.Harness]
		if !ok {
			continue
		}

		var provider *quotaProviderData
		for j := range qd.Providers {
			if qd.Providers[j].Provider == providerName {
				provider = &qd.Providers[j]
				break
			}
		}
		if provider == nil {
			continue
		}

		genIDs := generalWindowIDs[c.Harness]
		if len(genIDs) == 0 {
			continue
		}

		var remaining []float64
		for _, w := range provider.Windows {
			if w.Kind == "model" {
				continue
			}
			for _, gid := range genIDs {
				if w.ID == gid {
					remaining = append(remaining, w.PercentRemaining)
					break
				}
			}
		}

		if len(remaining) == 0 {
			continue
		}

		min := remaining[0]
		for _, r := range remaining[1:] {
			if r < min {
				min = r
			}
		}

		scored = append(scored, candidate{
			index: i,
			min:   min,
			fresh: provider.State.Status == "fresh",
		})
	}

	if len(scored) == 0 {
		return candidates[0]
	}

	var freshCands, staleCands []candidate
	for _, c := range scored {
		if c.fresh {
			freshCands = append(freshCands, c)
		} else {
			staleCands = append(staleCands, c)
		}
	}

	sort.Slice(freshCands, func(i, j int) bool {
		if freshCands[i].min != freshCands[j].min {
			return freshCands[i].min > freshCands[j].min
		}
		return freshCands[i].index < freshCands[j].index
	})

	sort.Slice(staleCands, func(i, j int) bool {
		if staleCands[i].min != staleCands[j].min {
			return staleCands[i].min > staleCands[j].min
		}
		return staleCands[i].index < staleCands[j].index
	})

	const staleClearMargin = 20.0

	var best candidate
	if len(freshCands) > 0 && len(staleCands) > 0 {
		if staleCands[0].min >= freshCands[0].min+staleClearMargin {
			best = staleCands[0]
		} else {
			best = freshCands[0]
		}
	} else if len(freshCands) > 0 {
		best = freshCands[0]
	} else if len(staleCands) > 0 {
		best = staleCands[0]
	} else {
		return candidates[0]
	}

	return candidates[best.index]
}

// firstMatchSelector always picks the first candidate (deterministic fallback).
type firstMatchSelector struct{}

func (s *firstMatchSelector) selectCandidate(candidates []DispatchCandidate) DispatchCandidate {
	if len(candidates) == 0 {
		return DispatchCandidate{}
	}
	return candidates[0]
}

// newQuotaSelector creates the appropriate selector for the given strategy.
func newQuotaSelector(strategy string) quotaSelector {
	switch strategy {
	case "quota-balanced":
		return &quotaBalancedSelector{provider: &defaultQuotaProvider{}}
	default:
		return &firstMatchSelector{}
	}
}
