// Package spawn implements the soldier spawn orchestration — the full
// sequence of resolving home, validating inputs, acquiring a worktree,
// launching the harness, and wiring the agent session.
package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
	"gopkg.in/yaml.v3"
)

// Args holds all input parameters for spawning a soldier.
type Args struct {
	ID                  string
	ProjectName         string
	Kind                string
	Mode                string // --mode flag value; empty=auto-detect
	ProjectMode         string // project registry mode (raw, not defaulted); empty = resolve from registry
	Yolo                bool
	Force               bool                 // --force flag; bypass captain backlog authority checks
	Backend             string               // --backend flag value; empty = auto-detect
	HarnessFlag         string               // --harness flag value; empty = resolve from config
	ModelFlag           string               // --model flag; empty = dispatch/template default
	EffortFlag          string               // --effort flag; empty = dispatch/template default
	HomeDir             string               // if empty, resolved via home.Resolve
	Endpoints           EndpointCapabilities // required endpoint lifecycle capability
	Arm                 bool
	Reopen              bool                       // allow spawning a done/blocked/already-live task
	ArmFunc             func(homeDir string) error // injectable arm function; nil = no auto-arm
	NoMistakesPreflight func(repoPath string) error
}

// Run executes the full spawn orchestration sequence by delegating to Runner.
//
//	resolve home → validate → brief exists → project path → worktree.AssertNotTangled
//	→ worktree.Get → resolve harness → model/effort → write .soldier-launch.sh + .soldier-brief.md + meta
//	→ start session → send brief → arm watcher
//
// On error after worktree lease, the worktree is returned to the pool (fail-closed).
func Run(args Args) (string, error) {
	return NewRunner(args).Run()
}

// ValidDeliveryModes lists the accepted delivery mode values.
var ValidDeliveryModes = map[string]bool{
	"no-mistakes": true,
	"direct-PR":   true,
	"local-only":  true,
}

// ValidateDeliveryMode returns an error if the mode is not a known value.
func ValidateDeliveryMode(mode string) error {
	if mode == "" {
		return nil // empty is allowed (will use registry default)
	}
	if !ValidDeliveryModes[mode] {
		return fmt.Errorf("invalid delivery mode %q: must be one of: no-mistakes, direct-PR, local-only", mode)
	}
	return nil
}

// noMistakesOnPath returns true if the no-mistakes binary is found on PATH.
func noMistakesOnPath() bool {
	_, err := exec.LookPath("no-mistakes")
	return err == nil
}

// noMistakesAvailable checks both binary presence and version/compat readiness
// using the full capability probe. Used by auto-detection paths where silent
// fallback to direct-PR is acceptable (unlike explicit/project/config selection).
func noMistakesAvailable() bool {
	probe := fleet.NoMistakesProbe()
	return probe.State == backend.Ready
}

// EnsureDeliveryModeRunnable validates that an explicit non-empty mode is runnable.
// If mode is "no-mistakes" and the binary is not on PATH or version is incompatible,
// returns a hard error with actionable guidance.
func EnsureDeliveryModeRunnable(mode string) error {
	if mode != "no-mistakes" {
		return nil
	}
	probe := fleet.NoMistakesProbe()
	switch probe.State {
	case backend.Absent:
		return fmt.Errorf("delivery mode 'no-mistakes' requires the no-mistakes binary on PATH; run 'munsu doctor' or 'go install github.com/kunchenguid/no-mistakes@latest'")
	case backend.Unsupported:
		return fmt.Errorf("delivery mode 'no-mistakes': %s; upgrade to a compatible version", probe.Detail)
	case backend.Failed:
		return fmt.Errorf("delivery mode 'no-mistakes' compatibility check failed: %s", probe.Detail)
	case backend.Ready:
		return nil
	default:
		return fmt.Errorf("delivery mode 'no-mistakes': unexpected probe state")
	}
}

// ResolveDeliveryMode resolves the effective delivery mode following this precedence:
//  1. explicitMode — non-empty --mode flag value
//  2. projectMode — mode from project registry (if non-empty)
//  3. config/default-mode — optional config file under homeDir
//  4. Auto — no-mistakes on PATH → no-mistakes, else → direct-PR (with message)
//
// Rules:
//   - An explicit --mode=no-mistakes with missing binary is a hard error.
//   - An explicit direct-PR/local-only is OK even when no-mistakes binary exists.
//   - A registry/config/auto no-mistakes with missing binary falls through to direct-PR.
func ResolveDeliveryMode(homeDir string, explicitMode string, projectMode string) (string, error) {
	// 1. Explicit --mode flag
	if explicitMode != "" {
		if err := ValidateDeliveryMode(explicitMode); err != nil {
			return "", err
		}
		// Hard error if user explicitly asked for no-mistakes but binary is missing
		if err := EnsureDeliveryModeRunnable(explicitMode); err != nil {
			return "", err
		}
		return explicitMode, nil
	}

	// 2. Project registry mode
	if projectMode != "" {
		if err := ValidateDeliveryMode(projectMode); err != nil {
			return "", err
		}
		// If registry explicitly set no-mistakes and binary is missing → hard error
		if err := EnsureDeliveryModeRunnable(projectMode); err != nil {
			return "", err
		}
		return projectMode, nil
	}

	// 3. config/default-mode (optional)
	if homeDir != "" {
		cfg, err := config.Get(homeDir, "default-mode")
		if err == nil && cfg != "" {
			if err := ValidateDeliveryMode(cfg); err != nil {
				return "", err
			}
			if err := EnsureDeliveryModeRunnable(cfg); err != nil {
				return "", err
			}
			return cfg, nil
		}
	}

	// 4. Auto: no-mistakes on PATH and compatible → no-mistakes, else → direct-PR
	if noMistakesAvailable() {
		return "no-mistakes", nil
	}
	// Binary on PATH but incompatible version: inform the user why.
	if noMistakesOnPath() {
		probe := fleet.NoMistakesProbe()
		fmt.Fprintf(os.Stderr, "warning: no-mistakes found on PATH but not compatible: %s; defaulting to direct-PR. Upgrade no-mistakes or run 'munsu doctor'\n", probe.Detail)
		return "direct-PR", nil
	}

	// 5. When require-no-mistakes config is set, refuse fallback
	if homeDir != "" {
		if _, err := config.Get(homeDir, "require-no-mistakes"); err == nil {
			return "", fmt.Errorf("config/require-no-mistakes is set but no-mistakes binary is absent or incompatible on this system")
		}
	}

	fmt.Fprintln(os.Stderr, "warning: no-mistakes not found on PATH; defaulting to direct-PR delivery mode. Install with: go install github.com/kunchenguid/no-mistakes@latest, or run 'munsu doctor'")
	return "direct-PR", nil
}

// noMistakesConfig is the compatibility-relevant subset of global config.
type noMistakesConfig struct {
	Agents            []string
	AgentArgsOverride map[string][]string
}

type noMistakesConfigFile struct {
	Agent             any                 `yaml:"agent"`
	AgentArgsOverride map[string][]string `yaml:"agent_args_override"`
}

func loadNoMistakesConfig() (noMistakesConfig, error) {
	home := os.Getenv("NM_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return noMistakesConfig{}, err
		}
		home = filepath.Join(userHome, ".no-mistakes")
	}
	data, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		return noMistakesConfig{}, err
	}
	var raw noMistakesConfigFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return noMistakesConfig{}, err
	}
	agents := parseConfiguredAgents(raw.Agent)
	if len(agents) == 0 {
		agents = []string{"auto"}
	}
	return noMistakesConfig{Agents: agents, AgentArgsOverride: raw.AgentArgsOverride}, nil
}

func parseConfiguredAgents(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		agents := make([]string, 0, len(typed))
		for _, item := range typed {
			if agent, ok := item.(string); ok && agent != "" {
				agents = append(agents, agent)
			}
		}
		return agents
	default:
		return nil
	}
}

func agentAvailable(agent string) bool {
	binary := agent
	switch agent {
	case "claude", "codex", "pi", "opencode", "copilot":
	case "rovodev":
		binary = "acli"
	default:
		return false
	}
	_, err := exec.LookPath(binary)
	return err == nil
}

// projectSettingsDisabled reports whether repoPath/.no-mistakes.yaml sets
// disable_project_settings: true (gate boundary). When true, the
// no-mistakes daemon does not load project AGENTS.md/settings into gate agents,
// so pi (and other non-codex/claude agents) are compatible without agent-side
// neutralization flags.
func projectSettingsDisabled(repoPath string) bool {
	data, err := os.ReadFile(filepath.Join(repoPath, ".no-mistakes.yaml"))
	if err != nil {
		return false
	}
	var raw struct {
		DisableProjectSettings bool `yaml:"disable_project_settings"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false
	}
	return raw.DisableProjectSettings
}

func checkNoMistakesCompatibility(repoPath string, cfg noMistakesConfig, available func(string) bool) error {
	hasInstructions := false
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join(repoPath, name)); err == nil {
			hasInstructions = true
			break
		}
	}
	if !hasInstructions {
		return nil
	}

	// Trusted gate path: trusted gate config disables project instructions so the
	// selected pipeline agent never adopts repo AGENTS.md identity.
	if projectSettingsDisabled(repoPath) {
		return nil
	}

	agents := cfg.Agents
	if len(agents) == 0 {
		agents = []string{"auto"}
	}
	if len(agents) == 1 && agents[0] == "auto" {
		agents = []string{"claude", "codex", "opencode", "rovodev", "pi", "copilot"}
	}
	for _, agent := range agents {
		if !available(agent) {
			continue
		}
		switch agent {
		case "codex":
			if codexNeutralizationPreserved(cfg.AgentArgsOverride[agent]) {
				return nil
			}
		case "claude":
			if claudeNeutralizationPreserved(cfg.AgentArgsOverride[agent]) {
				return nil
			}
		}
	}
	return fmt.Errorf("delivery mode 'no-mistakes' is incompatible with this repository's AGENTS.md/CLAUDE.md because no verified neutralization-capable gate agent is selected and available, and .no-mistakes.yaml does not set disable_project_settings: true; use '--mode direct-PR', install/select codex or claude in ~/.no-mistakes/config.yaml, or enable disable_project_settings in .no-mistakes.yaml")
}

func codexNeutralizationPreserved(args []string) bool {
	for i, arg := range args {
		value := ""
		switch {
		case strings.Contains(arg, "project_doc_max_bytes="):
			value = arg[strings.Index(arg, "project_doc_max_bytes=")+len("project_doc_max_bytes="):]
		case arg == "-c" && i+1 < len(args) && strings.Contains(args[i+1], "project_doc_max_bytes="):
			value = args[i+1][strings.Index(args[i+1], "project_doc_max_bytes=")+len("project_doc_max_bytes="):]
		}
		value = strings.Trim(value, "\"'")
		if value != "" && value != "0" {
			return false
		}
	}
	return true
}

func claudeNeutralizationPreserved(args []string) bool {
	for i, arg := range args {
		var value string
		switch {
		case arg == "--setting-sources" && i+1 < len(args):
			value = args[i+1]
		case strings.HasPrefix(arg, "--setting-sources="):
			value = strings.TrimPrefix(arg, "--setting-sources=")
		}
		for _, source := range strings.Split(value, ",") {
			if source == "project" || source == "local" {
				return false
			}
		}
	}
	return true
}

func defaultNoMistakesPreflight(repoPath string) error {
	cfg, err := loadNoMistakesConfig()
	if err != nil {
		return fmt.Errorf("reading no-mistakes config: %w", err)
	}
	return checkNoMistakesCompatibility(repoPath, cfg, agentAvailable)
}

// preflightDelivery runs the delivery-level mode preflight before
// worktree acquisition. It verifies environmental readiness for the
// resolved delivery mode (e.g. gh auth, remotes).
func (r *Runner) preflightDelivery() error {
	result, err := fleet.Preflight(r.effectiveMode, r.projPath)
	if err != nil {
		return fmt.Errorf("delivery preflight: %w", err)
	}
	if !result.Feasible {
		return fmt.Errorf("delivery preflight for %q blocked: %s", r.effectiveMode, formatPreflightFailures(result.Checks))
	}
	return nil
}

func formatPreflightFailures(checks []fleet.Check) string {
	var b strings.Builder
	for _, c := range checks {
		if !c.OK {
			if b.Len() > 0 {
				b.WriteString("; ")
			}
			b.WriteString(c.Name)
			if c.Detail != "" {
				b.WriteString(": ")
				b.WriteString(c.Detail)
			}
		}
	}
	return b.String()
}

// effectiveModeForSpawn resolves the effective delivery mode for a spawn operation.
// It falls back to fleet.Mode when ProjectMode is not set in args.
func effectiveModeForSpawn(homeDir string, args Args) (string, error) {
	projectMode := args.ProjectMode
	if projectMode == "" {
		if m, _, err := fleet.Mode(homeDir, args.ProjectName); err == nil {
			projectMode = m
		}
	}
	return ResolveDeliveryMode(homeDir, args.Mode, projectMode)
}
