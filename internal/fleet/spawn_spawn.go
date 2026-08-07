// Package spawn implements the soldier spawn orchestration — the full
// sequence of resolving home, validating inputs, acquiring a worktree,
// launching the harness, and wiring the agent session.
package fleet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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
	Force               bool                 // --force flag; bypass captain task authority checks
	Backend             string               // --backend flag value — raw-input carrier only; enters composition via BoundaryOverrides.Backend, never consumed directly by the runner
	HarnessFlag         string               // --harness flag value; empty = resolve from config
	ModelFlag           string               // --model flag; empty = dispatch/template default
	EffortFlag          string               // --effort flag; empty = dispatch/template default
	TaskDescription     string               // optional dispatch matching text; empty = brief/id fallback
	HomeDir             string               // if empty, resolved via home.Resolve
	Endpoints           EndpointCapabilities // required endpoint lifecycle capability
	Arm                 bool
	Reopen              bool                       // allow spawning a done/blocked/already-live task
	ArmFunc             func(homeDir string) error // injectable arm function; nil = no auto-arm
	NoMistakesPreflight func(repoPath string) error
	// Authority is the composed canonical Task Authority targeting the exact
	// home the Runner resolves (the CLI composition root supplies it from
	// Ctx.TaskAuthority(); tests inject a canonical home-backed Authority). It
	// owns the canonical spawn preconditions — readiness, the generation-
	// scoped worktree/endpoint bindings, and the durable Dispatch Holds that
	// gate the spawn action. It is required for the worktree binding cutover
	// (Task 4.1): bindWorktree fails closed when it is nil. Construction stays
	// side-effect free; no package global carries it.
	Authority *taskauthority.Canonical
}

// Run executes the full spawn orchestration sequence by delegating to Runner.
//
//	resolve home → validate → brief exists → project path → worktree.AssertNotTangled
//	→ worktree.Get → resolve harness → model/effort → write .soldier-launch.sh + .soldier-brief.md + meta
//	→ start session → send brief → arm watcher
//
// On error after worktree lease, the worktree is returned to the pool (fail-closed).
func Spawn(args Args) (string, error) {
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
	probe := NoMistakesProbe()
	return probe.State == backend.Ready
}

// EnsureDeliveryModeRunnable validates that an explicit non-empty mode is runnable.
// If mode is "no-mistakes" and the binary is not on PATH or version is incompatible,
// returns a hard error with actionable guidance.
func EnsureDeliveryModeRunnable(mode string) error {
	if mode != "no-mistakes" {
		return nil
	}
	probe := NoMistakesProbe()
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

// ResolveDeliveryMode resolves the effective delivery mode following this
// precedence:
//  1. explicitMode — non-empty --mode flag value
//  2. resolvedDefaultMode — typed base/project/snapshot default mode (if non-empty)
//  3. Auto — no-mistakes on PATH → no-mistakes, else → direct-PR (with message)
//
// Only validation and the runtime capability probe live here: config authority
// comes exclusively from the resolved values passed in. The typed surface is
// the single authority for the default mode and require-no-mistakes.
//
// Rules:
//   - An explicit --mode=no-mistakes with missing binary is a hard error.
//   - A typed default of no-mistakes with missing binary is a hard error.
//   - An explicit direct-PR/local-only is OK even when no-mistakes binary exists.
//   - Auto no-mistakes with missing binary falls through to direct-PR, unless
//     resolvedRequireNoMistakes is set (refuse, do not silently fall back).
func ResolveDeliveryMode(explicitMode string, resolvedDefaultMode string, resolvedRequireNoMistakes bool) (string, error) {
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

	// 2. Typed default mode (resolved base/project/snapshot)
	if resolvedDefaultMode != "" {
		if err := ValidateDeliveryMode(resolvedDefaultMode); err != nil {
			return "", err
		}
		// Hard error if the resolved default is no-mistakes and binary is missing
		if err := EnsureDeliveryModeRunnable(resolvedDefaultMode); err != nil {
			return "", err
		}
		return resolvedDefaultMode, nil
	}

	// 3. Auto: no-mistakes on PATH and compatible → no-mistakes, else → direct-PR
	if noMistakesAvailable() {
		return "no-mistakes", nil
	}
	// Binary on PATH but incompatible version: inform the user why.
	if noMistakesOnPath() {
		probe := NoMistakesProbe()
		fmt.Fprintf(os.Stderr, "warning: no-mistakes found on PATH but not compatible: %s; defaulting to direct-PR. Upgrade no-mistakes or run 'munsu doctor'\n", probe.Detail)
		return "direct-PR", nil
	}

	// 4. Typed require-no-mistakes is set → refuse fallback
	if resolvedRequireNoMistakes {
		return "", fmt.Errorf("require-no-mistakes is set but no-mistakes binary is absent or incompatible on this system")
	}

	fmt.Fprintln(os.Stderr, "warning: no-mistakes not found on PATH; defaulting to direct-PR delivery mode. Install with: go install github.com/kunchenguid/no-mistakes@latest, or run 'munsu doctor'")
	return "direct-PR", nil
}

// ResolveDeliveryModeFromBase resolves the effective delivery mode for a home
// without project context from the typed fleet base surface. The base
// defaultMode and requireNoMistakes are the single authority. A missing base
// document is the supported fresh-home state: resolution degrades solely to
// the runtime capability probe with no typed default. Any other load error
// (malformed, schema, permission, I/O) fails closed and propagates.
func ResolveDeliveryModeFromBase(homeDir, explicitMode string) (string, error) {
	base, err := config.LoadFleetBase(homeDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolving fleet base config: %w", err)
		}
		// Fresh home with no base document: no typed default authority, so
		// resolution falls solely to the runtime capability probe.
		return ResolveDeliveryMode(explicitMode, "", false)
	}
	resolvedRequireNoMistakes := false
	if base.Config.RequireNoMistakes != nil {
		resolvedRequireNoMistakes = *base.Config.RequireNoMistakes
	}
	return ResolveDeliveryMode(explicitMode, normalizeSnapshotDeliveryMode(base.Config.DefaultMode), resolvedRequireNoMistakes)
}

// ResolveDeliveryModeFromProject resolves the effective delivery mode for a
// declared project from exactly one immutable project snapshot. Any resolution
// error (unknown project, malformed base/overlay, registry or I/O failure)
// returns a typed failure; there is no fallback to the fleet base or to
// auto-detection. Used by project-scoped callers (e.g. munsu brief).
func ResolveDeliveryModeFromProject(homeDir, projectName, explicitMode string) (string, error) {
	snap, err := ResolveProjectSnapshot(homeDir, projectName, config.BoundaryOverrides{})
	if err != nil {
		return "", classifySnapshotError(projectName, err)
	}
	resolved := snap.Config()
	return ResolveDeliveryMode(explicitMode, normalizeSnapshotDeliveryMode(resolved.DefaultMode), resolved.RequireNoMistakes)
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
	result, err := Preflight(r.effectiveMode, r.projPath)
	if err != nil {
		return fmt.Errorf("delivery preflight: %w", err)
	}
	if !result.Feasible {
		return fmt.Errorf("delivery preflight for %q blocked: %s", r.effectiveMode, formatPreflightFailures(result.Checks))
	}
	return nil
}

func formatPreflightFailures(checks []Check) string {
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

// effectiveModeForSpawn resolves the effective delivery mode for a spawn
// operation on the legacy (non-typed-config) path. The registry mode is the
// only default authority here; no typed require-no-mistakes exists to refuse
// fallback, so the flat competing authority is not consulted.
func effectiveModeForSpawn(homeDir string, args Args) (string, error) {
	projectMode := args.ProjectMode
	if projectMode == "" {
		if m, _, err := Mode(homeDir, args.ProjectName); err == nil {
			projectMode = m
		}
	}
	return ResolveDeliveryMode(args.Mode, projectMode, false)
}
