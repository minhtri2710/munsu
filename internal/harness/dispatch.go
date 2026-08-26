package harness

import (
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
)

type DispatchCandidate = config.DispatchCandidate

type DispatchProfile = config.DispatchProfile

// DispatchConfig is the top-level structure for soldier-dispatch.json.
// Supports defaultHarness/profiles and default/rules shapes.
type DispatchConfig struct {
	// DefaultHarness is used when no profile matches (munsu shape).
	DefaultHarness string `json:"defaultHarness,omitempty"`
	// DefaultModel is the default model when no profile supplies one.
	DefaultModel string `json:"defaultModel,omitempty"`
	// DefaultEffort is the default effort when no profile supplies one.
	DefaultEffort string `json:"defaultEffort,omitempty"`
	// Default is the object form: {"harness","model","effort"}.
	// Normalized into DefaultHarness/DefaultModel/DefaultEffort on load.
	Default *DispatchCandidate `json:"default,omitempty"`
	// Profiles is the ordered list of dispatch profiles (munsu shape).
	Profiles []DispatchProfile `json:"profiles,omitempty"`
	// Rules is an alternate ordered rule list; normalized into Profiles on load.
	Rules []DispatchProfile `json:"rules,omitempty"`
}

// DispatchSelection is the concrete harness/model/effort chosen for a spawn.
type DispatchSelection struct {
	Harness string
	Model   string
	Effort  string
}

// ResolveDispatchSelection returns harness + model + effort for a task description.
// Precedence for model/effort: matched profile → config defaults → empty.
func ResolveDispatchSelection(cfg *DispatchConfig, taskDesc string) DispatchSelection {
	if cfg == nil {
		return DispatchSelection{}
	}
	sel := DispatchSelection{
		Harness: cfg.DefaultHarness,
		Model:   cfg.DefaultModel,
		Effort:  cfg.DefaultEffort,
	}

	taskLower := strings.ToLower(taskDesc)
	taskWords := strings.Fields(taskLower)

	if taskDesc == "" {
		return sel
	}

	for _, p := range cfg.Profiles {
		matchRules := p.Match
		if len(matchRules) == 0 && p.When != "" {
			matchRules = []string{p.When}
		}
		if !matchesProfile(matchRules, taskLower, taskWords) {
			continue
		}
		if p.SelectStrategy == "quota-balanced" {
			// Prefer candidates from this profile's Use list; else all profiles.
			if len(p.Use) > 0 {
				selQuota := newQuotaSelector("quota-balanced")
				picked := selQuota.selectCandidate(p.Use)
				sel.Harness = picked.Harness
				if picked.Model != "" {
					sel.Model = picked.Model
				} else if p.Model != "" {
					sel.Model = p.Model
				}
				if picked.Effort != "" {
					sel.Effort = picked.Effort
				} else if p.Effort != "" {
					sel.Effort = p.Effort
				}
				return sel
			}
			// No Use list: resolve across all profiles.
			cands := make([]DispatchCandidate, 0, len(cfg.Profiles))
			for _, q := range cfg.Profiles {
				cands = append(cands, DispatchCandidate{
					Harness: q.Harness,
					Model:   q.Model,
					Effort:  q.Effort,
				})
			}
			selQuota := newQuotaSelector("quota-balanced")
			picked := selQuota.selectCandidate(cands)
			sel.Harness = picked.Harness
			// Fill model/effort from the pick directly.
			if picked.Model != "" {
				sel.Model = picked.Model
			}
			if picked.Effort != "" {
				sel.Effort = picked.Effort
			}
			return sel
		}
		sel.Harness = p.Harness
		if p.Model != "" {
			sel.Model = p.Model
		}
		if p.Effort != "" {
			sel.Effort = p.Effort
		}
		// Multi-candidate use without select strategy: first candidate.
		if len(p.Use) > 0 && p.Harness == "" {
			c := p.Use[0]
			sel.Harness = c.Harness
			if c.Model != "" {
				sel.Model = c.Model
			}
			if c.Effort != "" {
				sel.Effort = c.Effort
			}
		}
		return sel
	}
	return sel
}

// matchesProfile checks if the profile's match rules apply to the task.
func matchesProfile(rules []string, taskLower string, taskWords []string) bool {
	for _, rule := range rules {
		rule = strings.ToLower(strings.TrimSpace(rule))

		// Wildcard matches everything
		if rule == "*" {
			return true
		}
		if rule == "" {
			continue
		}

		// Check if the rule is a word in the task
		for _, w := range taskWords {
			if w == rule {
				return true
			}
		}

		// Full rule as substring of the task
		if strings.Contains(taskLower, rule) {
			return true
		}

		// Long "when" prose: match any contiguous 3+ word phrase so distinctive
		// clauses like "deep architectural redesign" still hit.
		ruleWords := strings.Fields(rule)
		if len(ruleWords) >= 3 {
			for n := len(ruleWords); n >= 3; n-- {
				for i := 0; i+n <= len(ruleWords); i++ {
					phrase := strings.Join(ruleWords[i:i+n], " ")
					if strings.Contains(taskLower, phrase) {
						return true
					}
				}
			}
		}
	}
	return false
}
