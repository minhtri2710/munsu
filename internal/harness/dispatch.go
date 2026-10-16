package harness

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
)

type DispatchCandidate = config.DispatchCandidate

type DispatchProfile = config.DispatchProfile

// DispatchConfig is the resolved dispatch input for one spawn.
type DispatchConfig struct {
	// DefaultHarness is used when no profile matches.
	DefaultHarness string
	// DefaultModel is the default model when no profile supplies one.
	DefaultModel string
	// DefaultEffort is the default effort when no profile supplies one.
	DefaultEffort string
	// Profiles is the ordered list of dispatch profiles.
	Profiles []DispatchProfile
	// QuotaTool is the project's Ready quota-axi executable, or "" when the
	// project configures no quota tool.
	QuotaTool string
}

// DispatchSelection is the concrete harness/model/effort chosen for a spawn,
// with the matched profile's charter scaffold.
type DispatchSelection struct {
	Harness  string
	Model    string
	Effort   string
	Scaffold string
}

// ResolveDispatchSelection returns harness + model + effort for a task description.
// Precedence for model/effort: matched profile → config defaults → empty.
// Scaffold comes only from a matched profile. A quota-axi failure is returned.
func ResolveDispatchSelection(cfg *DispatchConfig, taskDesc string) (DispatchSelection, error) {
	if cfg == nil {
		return DispatchSelection{}, nil
	}
	sel := DispatchSelection{
		Harness: cfg.DefaultHarness,
		Model:   cfg.DefaultModel,
		Effort:  cfg.DefaultEffort,
	}

	taskLower := strings.ToLower(taskDesc)
	taskWords := strings.Fields(taskLower)

	if taskDesc == "" {
		return sel, nil
	}

	for _, p := range cfg.Profiles {
		if !matchesProfile(p.Match, taskLower, taskWords) {
			continue
		}
		sel.Scaffold = p.Scaffold
		sel.Harness = p.Harness
		if p.Model != "" {
			sel.Model = p.Model
		}
		if p.Effort != "" {
			sel.Effort = p.Effort
		}
		// A configured quota tool orders the use candidates; without one a
		// profile with no harness of its own takes the first.
		if len(p.Use) > 0 && (cfg.QuotaTool != "" || p.Harness == "") {
			c := p.Use[0]
			if cfg.QuotaTool != "" {
				var err error
				if c, err = quotaBalanced(cfg.QuotaTool, p.Use); err != nil {
					return DispatchSelection{}, fmt.Errorf("dispatch profile %q: %w", p.Name, err)
				}
			}
			sel.Harness = c.Harness
			if c.Model != "" {
				sel.Model = c.Model
			}
			if c.Effort != "" {
				sel.Effort = c.Effort
			}
		}
		return sel, nil
	}
	return sel, nil
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

		// Long prose rule: match any contiguous 3+ word phrase so distinctive
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
