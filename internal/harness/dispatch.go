package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DispatchProfile defines a named dispatch profile with natural-language match
// rules and a target harness.
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
func ResolveDispatch(cfg *DispatchConfig, taskDesc string) string {
	if taskDesc == "" {
		return cfg.DefaultHarness
	}

	taskLower := strings.ToLower(taskDesc)
	taskWords := strings.Fields(taskLower)

	for _, p := range cfg.Profiles {
		if matchesProfile(p.Match, taskLower, taskWords) {
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
