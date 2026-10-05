package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
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
	Codex:    {"OPENAI_API_KEY"},
	Opencode: {"OPENAI_API_KEY"},
	Pi:       piAuthEnvVars(),
	Grok:     {"GROK_API_KEY", "XAI_API_KEY"},
	Agy:      {"ANTHROPIC_API_KEY"},
}

// claudeAuthProbeTimeout bounds `claude auth status`. The probe measured about
// 0.2s on claude 2.1.289; the bound leaves room for a slow keychain unlock while
// keeping a hung claude from stalling `munsu spawn`. It is a variable so tests
// can shorten it.
var claudeAuthProbeTimeout = 10 * time.Second

// claudeAuthConfigured asks claude itself whether it holds credentials: the
// answer is the exit status of `claude auth status`, and nothing else. A
// non-zero exit, a probe that cannot start, and a probe that outlives
// claudeAuthProbeTimeout all read as not configured. The exit status is enough:
// it already covers claude.ai logins and API keys, so no output field is needed.
// Security: the probe's output is discarded, never read, printed, or logged.
func claudeAuthConfigured() bool {
	ctx, cancel := context.WithTimeout(context.Background(), claudeAuthProbeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "claude", "auth", "status").Run() == nil
}

// piCredentialFile is a function that reports whether a Pi credential store file
// exists as a regular user-readable file. It is a variable so tests can inject
// a custom checker that uses temp directories instead of the real Pi config.
// The callback receives the path that would be checked; return true when the
// path exists, is a regular file, and is readable.
// Security: never read, print, parse, or log the file contents.
var piCredentialFile = func(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return false
	}
	// Verify the file is actually readable (stat succeeds but file may be
	// permission-denied on open). Security: never read the content.
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// piConfigDir returns the Pi agent config directory.
// Respects PI_CODING_AGENT_DIR; falls back to ~/.pi/agent.
// It is a variable so tests can inject a custom config dir seam.
var piConfigDir = func() string {
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
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
	if harnessName == Claude {
		result.AuthConfigured = PreflightAbsent
		if claudeAuthConfigured() {
			result.AuthConfigured = PreflightOK
		}
	} else if envVars, ok := preflightAuthEnv[harnessName]; !ok {
		result.AuthConfigured = PreflightUnknown
	} else {
		found := false
		for _, env := range envVars {
			if os.Getenv(env) != "" {
				found = true
				break
			}
		}
		if !found && harnessName == Pi {
			// Pi additionally supports auth via its credential store file (~/.pi/agent/auth.json).
			configDir := piConfigDir()
			if configDir != "" {
				credPath := filepath.Join(configDir, "auth.json")
				if piCredentialFile(credPath) {
					found = true
				}
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

// piAuthEnvVars returns all environment variables that Pi accepts as API keys.
// Pi reads --api-key from any of these provider-specific env vars.
// Never read, print, parse, or log the actual values of these environment variables.
func piAuthEnvVars() []string {
	return []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_OAUTH_TOKEN",
		"ANT_LING_API_KEY",
		"OPENAI_API_KEY",
		"AZURE_OPENAI_API_KEY",
		"DEEPSEEK_API_KEY",
		"NVIDIA_API_KEY",
		"GEMINI_API_KEY",
		"GROQ_API_KEY",
		"CEREBRAS_API_KEY",
		"XAI_API_KEY",
		"FIREWORKS_API_KEY",
		"TOGETHER_API_KEY",
		"OPENROUTER_API_KEY",
		"AI_GATEWAY_API_KEY",
		"ZAI_API_KEY",
		"ZAI_CODING_CN_API_KEY",
		"MISTRAL_API_KEY",
		"MINIMAX_API_KEY",
		"MOONSHOT_API_KEY",
		"OPENCODE_API_KEY",
		"KIMI_API_KEY",
		"CLOUDFLARE_API_KEY",
		"XIAOMI_API_KEY",
		"XIAOMI_TOKEN_PLAN_CN_API_KEY",
		"XIAOMI_TOKEN_PLAN_AMS_API_KEY",
		"XIAOMI_TOKEN_PLAN_SGP_API_KEY",
		"AWS_BEARER_TOKEN_BEDROCK",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
	}
}

// authHint returns an actionable error message for missing auth configuration.
func authHint(harness string) string {
	if harness == Claude {
		return fmt.Sprintf("harness %q auth not configured; run `claude auth login`, or set ANTHROPIC_API_KEY, so that `claude auth status` succeeds", harness)
	}
	envVars, ok := preflightAuthEnv[harness]
	if !ok || len(envVars) == 0 {
		return fmt.Sprintf("harness %q auth not configured (unknown auth method)", harness)
	}
	if harness == Pi {
		return fmt.Sprintf("harness %q auth not configured; set any Pi-supported API key environment variable or set up Pi's credential store (`pi --help` for details)", harness)
	}
	return fmt.Sprintf("harness %q auth not configured; set %s environment variable", harness, envVars[0])
}
