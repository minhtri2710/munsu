// Package harness detects the running agent harness and resolves crewmate/secondmate
// harness assignments from configuration.
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// DispatchProfile defines a named dispatch profile with natural-language match
// rules, a target harness, and optional profile selection strategy.
type DispatchProfile struct {
	// Name is the profile identifier.
	Name string `json:"name"`
	// Match is a list of natural-language patterns. The first profile whose
	// match list contains words from the task description wins.
	// A single "*" wildcard matches everything.
	Match []string `json:"match"`
	// Harness is the target harness for this profile (claude, codex, etc.).
	Harness string `json:"harness"`
	// MaxConcurrent limits concurrent assignments (0 = unlimited).
	MaxConcurrent int `json:"maxConcurrent"`
	// SelectStrategy identifies the profile selection method.
	// Supported values: "" (default/first), "quota-balanced".
	// When "quota-balanced", SelectProfile uses quota-axi data to pick
	// the least-constrained vendor. Falls back to first profile when
	// quota-axi is unavailable.
	SelectStrategy string `json:"select,omitempty"`
}

// DispatchConfig is the top-level structure for crew-dispatch.json.
type DispatchConfig struct {
	// DefaultHarness is used when no profile matches.
	DefaultHarness string `json:"defaultHarness,omitempty"`
	// Profiles is the ordered list of dispatch profiles.
	Profiles []DispatchProfile `json:"profiles,omitempty"`
}

// LoadDispatch reads and parses a crew-dispatch.json file.
func LoadDispatch(path string) (*DispatchConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading dispatch config: %w", err)
	}
	var cfg DispatchConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing dispatch config: %w", err)
	}
	return &cfg, nil
}

// ResolveDispatch finds the first profile whose match rules match the task
// description and returns the target harness. If no profile matches, returns
// the default harness. If no default is set, returns empty string.
//
// When the matching profile has SelectStrategy "quota-balanced", SelectProfile
// is called to resolve the actual harness from the available profiles.
func ResolveDispatch(cfg *DispatchConfig, taskDesc string) string {
	if taskDesc == "" {
		return cfg.DefaultHarness
	}

	taskLower := strings.ToLower(taskDesc)
	taskWords := strings.Fields(taskLower)

	for _, p := range cfg.Profiles {
		if matchesProfile(p.Match, taskLower, taskWords) {
			if p.SelectStrategy == "quota-balanced" {
				// Use quota-balanced selection among profiles with
				// the same match group (all profiles that matched).
				return SelectProfile(cfg.Profiles, p.SelectStrategy)
			}
			return p.Harness
		}
	}
	return cfg.DefaultHarness
}

// matchesProfile checks if the profile's match rules apply to the task.
func matchesProfile(rules []string, taskLower string, taskWords []string) bool {
	for _, rule := range rules {
		rule = strings.ToLower(strings.TrimSpace(rule))

		// Wildcard matches everything
		if rule == "*" {
			return true
		}

		// Check if the rule is a word in the task
		for _, w := range taskWords {
			if w == rule {
				return true
			}
		}

		// Check substring match
		if strings.Contains(taskLower, rule) {
			return true
		}
	}
	return false
}

// SelectProfile resolves the target harness from a list of profiles using
// the given selection strategy. Currently supports "quota-balanced".
// When quota-axi is unavailable or the strategy is unrecognized, returns
// the first profile's harness (graceful degradation).
func SelectProfile(profiles []DispatchProfile, strategy string) string {
	if len(profiles) == 0 {
		return ""
	}

	switch strategy {
	case "quota-balanced":
		return selectQuotaBalanced(profiles)
	default:
		// Unknown strategy: return first profile's harness.
		return profiles[0].Harness
	}
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

// selectQuotaBalanced picks the harness profile with the highest minimum
// remaining GENERAL quota. Falls back to the first profile when quota-axi
// is not on PATH, returns unparseable JSON, or no candidate is usable.
func selectQuotaBalanced(profiles []DispatchProfile) string {
	if len(profiles) == 0 {
		return ""
	}

	quotaJSON, err := runQuotaAxi()
	if err != nil {
		// quota-axi missing or failed: use first profile.
		return profiles[0].Harness
	}

	var qd quotaData
	if err := json.Unmarshal([]byte(quotaJSON), &qd); err != nil {
		return profiles[0].Harness
	}

	if len(qd.Providers) == 0 {
		return profiles[0].Harness
	}

	type candidate struct {
		index int
		min   float64
		fresh bool
	}

	var candidates []candidate
	for i, p := range profiles {
		providerName, ok := quotaProvider[p.Harness]
		if !ok {
			continue
		}

		// Find the provider in quota data.
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

		// Get general window IDs for this harness.
		genIDs := generalWindowIDs[p.Harness]
		if len(genIDs) == 0 {
			continue
		}

		// Collect percentRemaining for matching GENERAL windows.
		var remaining []float64
		for _, w := range provider.Windows {
			if w.Kind == "model" {
				continue // skip model-scoped windows
			}
			for _, gid := range genIDs {
				if w.ID == gid {
					remaining = append(remaining, w.PercentRemaining)
					break
				}
			}
		}

		if len(remaining) == 0 {
			continue // no usable windows
		}

		// Compute minimum remaining across GENERAL windows.
		min := remaining[0]
		for _, r := range remaining[1:] {
			if r < min {
				min = r
			}
		}

		candidates = append(candidates, candidate{
			index: i,
			min:   min,
			fresh: provider.State.Status == "fresh",
		})
	}

	if len(candidates) == 0 {
		return profiles[0].Harness
	}

	// Separate fresh and stale candidates.
	var freshCands, staleCands []candidate
	for _, c := range candidates {
		if c.fresh {
			freshCands = append(freshCands, c)
		} else {
			staleCands = append(staleCands, c)
		}
	}

	// Sort fresh candidates by min descending, then index ascending.
	sort.Slice(freshCands, func(i, j int) bool {
		if freshCands[i].min != freshCands[j].min {
			return freshCands[i].min > freshCands[j].min
		}
		return freshCands[i].index < freshCands[j].index
	})

	// Sort stale candidates similarly.
	sort.Slice(staleCands, func(i, j int) bool {
		if staleCands[i].min != staleCands[j].min {
			return staleCands[i].min > staleCands[j].min
		}
		return staleCands[i].index < staleCands[j].index
	})

	var best candidate
	if len(freshCands) > 0 && len(staleCands) > 0 {
		// A stale candidate wins only if its min is at least 20 points
		// higher than the best fresh candidate's min.
		margin := 20.0
		if staleCands[0].min >= freshCands[0].min+margin {
			best = staleCands[0]
		} else {
			best = freshCands[0]
		}
	} else if len(freshCands) > 0 {
		best = freshCands[0]
	} else if len(staleCands) > 0 {
		best = staleCands[0]
	} else {
		return profiles[0].Harness
	}

	return profiles[best.index].Harness
}

// runQuotaAxi executes quota-axi --json and returns the JSON output.
// Returns an error if quota-axi is not on PATH or exits non-zero.
func runQuotaAxi() (string, error) {
	cmd := exec.Command("quota-axi", "--json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("quota-axi: %w", err)
	}
	return string(output), nil
}
