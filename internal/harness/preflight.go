package harness

import (
	"fmt"
	"os"
	"os/exec"
)

// PreflightLevel indicates the status of a preflight readiness check.
type PreflightLevel string

const (
	PreflightOK      PreflightLevel = "ok"
	PreflightAbsent  PreflightLevel = "absent"
	PreflightUnknown PreflightLevel = "unknown"
)

// PreflightResult holds the readiness check results for a harness.
type PreflightResult struct {
	AdapterKnown   PreflightLevel
	BinaryOnPath   PreflightLevel
	AuthConfigured PreflightLevel
	ModelValid     PreflightLevel
}

// preflightBinaryNames maps harness names to their CLI binary names for PATH checks.
var preflightBinaryNames = map[string]string{
	Claude:   "claude",
	Codex:    "codex",
	Opencode: "opencode",
	Pi:       "pi",
	Grok:     "grok",
	Agy:      "agy",
}

// preflightAuthEnv maps harness names to environment variables that hold
// their API authentication credentials.
var preflightAuthEnv = map[string][]string{
	Claude:   {"ANTHROPIC_API_KEY"},
	Codex:    {"OPENAI_API_KEY"},
	Opencode: {"OPENAI_API_KEY"},
	Pi:       {"OPENAI_API_KEY"},
	Grok:     {"GROK_API_KEY", "XAI_API_KEY"},
	Agy:      {"ANTHROPIC_API_KEY"},
}

// Preflight checks the readiness of a harness before spawning.
// Checks in order: adapter known → binary on PATH → auth credentials → model valid.
// Each level returns ok/absent/unknown honestly.
func Preflight(harnessName string) (*PreflightResult, error) {
	result := &PreflightResult{}

	// 1. Adapter known
	if _, ok := GetAdapter(harnessName); !ok {
		result.AdapterKnown = PreflightAbsent
		return result, &PreflightError{
			Harness: harnessName,
			Reason:  "adapter-unknown",
		}
	}
	result.AdapterKnown = PreflightOK

	// 2. Binary on PATH
	binary, ok := preflightBinaryNames[harnessName]
	if !ok {
		result.BinaryOnPath = PreflightUnknown
	} else if _, err := exec.LookPath(binary); err != nil {
		result.BinaryOnPath = PreflightAbsent
	} else {
		result.BinaryOnPath = PreflightOK
	}

	// 3. Auth credentials
	envVars, ok := preflightAuthEnv[harnessName]
	if !ok {
		result.AuthConfigured = PreflightUnknown
	} else {
		found := false
		for _, env := range envVars {
			if os.Getenv(env) != "" {
				found = true
				break
			}
		}
		if !found {
			result.AuthConfigured = PreflightAbsent
		} else {
			result.AuthConfigured = PreflightOK
		}
	}

	// 4. Model valid: can't validate model availability without API calls.
	// Return unknown since there's no reliable way to check model support
	// at preflight time without the actual model value and an API call.
	result.ModelValid = PreflightUnknown

	return result, nil
}

// PreflightError is a structured error for preflight failures.
type PreflightError struct {
	Harness string
	Reason  string
}

func (e *PreflightError) Error() string {
	switch e.Reason {
	case "adapter-unknown":
		return fmt.Sprintf("harness %q is not a known adapter; must be one of %v", e.Harness, KnownHarnesses)
	case "binary-absent":
		return fmt.Sprintf("harness %q binary not found on PATH; install the %s CLI to use it", e.Harness, e.Harness)
	case "auth-absent":
		return authHint(e.Harness)
	default:
		return fmt.Sprintf("harness %q preflight failed: %s", e.Harness, e.Reason)
	}
}

// authHint returns an actionable error message for missing auth configuration.
func authHint(harness string) string {
	envVars, ok := preflightAuthEnv[harness]
	if !ok || len(envVars) == 0 {
		return fmt.Sprintf("harness %q auth not configured (unknown auth method)", harness)
	}
	return fmt.Sprintf("harness %q auth not configured; set %s environment variable", harness, envVars[0])
}
