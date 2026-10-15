package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// PreflightLevel indicates the status of a preflight readiness check.
type PreflightLevel string

const (
	PreflightOK      PreflightLevel = "ok"
	PreflightAbsent  PreflightLevel = "absent"
	PreflightUnknown PreflightLevel = "unknown"
)

// PreflightResult holds the readiness check results for a harness. Ready is
// the answer of the harness CLI's own readiness command; ReadyDetail is what
// it printed when it is not ready.
type PreflightResult struct {
	AdapterKnown PreflightLevel
	BinaryOnPath PreflightLevel
	Ready        PreflightLevel
	ReadyDetail  string
	ModelValid   PreflightLevel
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

// readinessProbeTimeout bounds a harness's readiness command so a hung CLI
// cannot stall `munsu spawn`. It is a variable so tests can shorten it.
var readinessProbeTimeout = 10 * time.Second

// readinessProbe is the harness's own command that says whether it can run: a
// non-zero exit, or output containing notReady, means it cannot.
type readinessProbe struct {
	args     []string
	notReady string
}

var readinessProbes = map[string]readinessProbe{
	// claude 2.1.300 with no login exits 1: "Not logged in. Run claude auth login to authenticate."
	Claude: {args: []string{"auth", "status", "--text"}},
	// pi 1.2.0 with no credentials exits 0: "No models available. Use /login to log into a provider via OAuth or API key."
	// Words: Human, typed, 2026-10-15: "sao lại phải dùng env?? chạy cli của harness agent là được rồi, nếu harness đó không chạy được thì báo lại"
	Pi: {args: []string{"--list-models"}, notReady: "No models available"},
}

// runReadinessProbe runs the harness's readiness command and returns "" when
// the harness is ready, else what the CLI printed. A ready claude prints the
// account, so output is kept only on failure. With seat set it asks as a fenced
// claude seat logs in: CLAUDE_CONFIG_DIR is a fresh empty dir in the seat build
// dir and CLAUDE_SECURESTORAGE_CONFIG_DIR names the Human's keychain item, so a
// claude that stops honoring the variable fails here rather than at a login
// screen. The dir is never a seat's and never under a temp root: an unfenced
// claude must not load settings a seat can write.
func runReadinessProbe(binary string, probe readinessProbe, seat *ClaudeSeat) string {
	command := strings.Join(append([]string{binary}, probe.args...), " ")
	ctx, cancel := context.WithTimeout(context.Background(), readinessProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, probe.args...)
	cmd.WaitDelay = time.Second
	if seat != nil {
		if err := prepareSeatBuildDir(seat.BuildDir); err != nil {
			return fmt.Sprintf("preparing the seat build dir for `%s`: %v", command, err)
		}
		dir, err := os.MkdirTemp(seat.BuildDir, "claude-auth-")
		if err != nil {
			return fmt.Sprintf("preparing the seat config dir for `%s`: %v", command, err)
		}
		defer os.RemoveAll(dir)
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir, "CLAUDE_SECURESTORAGE_CONFIG_DIR="+seat.Config.SecureStorage)
	}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	switch {
	case ctx.Err() != nil:
		return fmt.Sprintf("`%s` did not finish within %s", command, readinessProbeTimeout)
	case err != nil:
		if text == "" {
			text = err.Error()
		}
		return fmt.Sprintf("`%s` failed: %s", command, text)
	case probe.notReady != "" && strings.Contains(text, probe.notReady):
		return fmt.Sprintf("`%s` reported: %s", command, text)
	}
	return ""
}

// CheckReady runs the readiness command of harnessName with the binary at
// path. It returns nil when the harness has none or reports ready.
func CheckReady(harnessName, path string) error {
	probe, ok := readinessProbes[harnessName]
	if !ok {
		return nil
	}
	if detail := runReadinessProbe(path, probe, nil); detail != "" {
		return &PreflightError{Harness: harnessName, Reason: "not-ready", Detail: detail}
	}
	return nil
}

// ClaudeSeat is how a fenced claude seat logs in, for the readiness probe: the
// Human's configuration, and the munsu home's SeatBuildDir.
type ClaudeSeat struct {
	Config   ClaudeConfig
	BuildDir string
}

// Preflight checks the readiness of a harness before spawning.
// Checks in order: adapter known → binary on PATH → the harness CLI's own
// readiness command → model valid. Each level returns ok/absent/unknown
// honestly. claudeSeat, when set, is the configuration a fenced claude seat
// logs in with (see runReadinessProbe).
func Preflight(harnessName string, claudeSeat *ClaudeSeat) (*PreflightResult, error) {
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

	// 3. The harness CLI's own readiness command
	result.Ready = PreflightUnknown
	if probe, ok := readinessProbes[harnessName]; ok && result.BinaryOnPath == PreflightOK {
		result.Ready = PreflightOK
		if detail := runReadinessProbe(binary, probe, claudeSeat); detail != "" {
			result.Ready = PreflightAbsent
			result.ReadyDetail = detail
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
	Detail  string
}

func (e *PreflightError) Error() string {
	switch e.Reason {
	case "adapter-unknown":
		return fmt.Sprintf("harness %q is not a known adapter; must be one of %v", e.Harness, KnownHarnesses)
	case "binary-absent":
		return fmt.Sprintf("harness %q binary not found on PATH; install the %s CLI to use it", e.Harness, e.Harness)
	case "not-ready":
		return fmt.Sprintf("harness %q cannot run: %s", e.Harness, e.Detail)
	default:
		return fmt.Sprintf("harness %q preflight failed: %s", e.Harness, e.Reason)
	}
}
