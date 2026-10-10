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
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"gopkg.in/yaml.v3"
)

// Args holds all input parameters for spawning a soldier.
type Args struct {
	ID                  string
	ProjectName         string
	Yolo                bool
	Force               bool                 // --force flag; bypass captain task authority checks
	Backend             string               // --backend flag value — optional assertion against the resolved project snapshot
	HarnessFlag         string               // --harness flag value; empty = resolve from config
	ModelFlag           string               // --model flag; empty = dispatch/template default
	EffortFlag          string               // --effort flag; empty = dispatch/template default
	TaskDescription     string               // optional dispatch matching text; empty = brief/id fallback
	HomeDir             string               // if empty, resolved via home.Resolve
	Endpoints           EndpointCapabilities // required endpoint lifecycle capability
	Arm                 bool
	ArmFunc             func(homeDir string) error // injectable arm function; nil = no auto-arm
	NoMistakesPreflight func(repoPath string, step taskauthority.DeliveryStep) error
	// Authority is the composed canonical Task Authority targeting the exact
	// home the Runner resolves (the CLI composition root supplies it from
	// Ctx.TaskAuthority(); tests inject a canonical home-backed Authority). It
	// owns the canonical spawn preconditions — readiness, the generation-
	// scoped worktree/endpoint bindings, and the durable Dispatch Holds that
	// gate the spawn action. It is required for the worktree binding cutover
	// (Task 4.1): bindWorktree fails closed when it is nil. Construction stays
	// side-effect free; no package global carries it.
	Authority *taskauthority.Canonical

	// IncarnationMint is an optional opaque incarnation generator for the
	// endpoint binding. When nil, a crypto/rand generator is used. Tests
	// inject a deterministic func. The generated value is persisted by
	// taskauthority as an opaque generation-bound identity and reused (not
	// re-minted) on retry/recovery of the same launch operation.
	IncarnationMint IncarnationMintFunc
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

// ResolveBriefProject resolves one declared project's immutable snapshot once
// and returns the delivery mode derived from its captured review and forge
// steps, plus the project's tamper check. When selectMode is false, the task's
// recorded DeliveryContract owns the delivery mode and only the tamper check is
// returned.
func ResolveBriefProject(homeDir, projectName string, selectMode bool) (mode, tamperCheck string, err error) {
	snap, err := ResolveProjectSnapshot(homeDir, projectName)
	if err != nil {
		return "", "", classifySnapshotError(projectName, err)
	}
	resolved := snap.Config()
	if !selectMode {
		return "", resolved.TamperCheck, nil
	}
	return taskauthority.DeliveryModeForSteps(deliveryStep(resolved.ReviewStep), deliveryStep(resolved.ForgeStep)), resolved.TamperCheck, nil
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
	switch {
	case strings.HasPrefix(agent, "acp:"):
		// acp:<target> gate agents run through the user-installed acpx
		// binary; the target's own availability cannot be verified from here.
		binary = "acpx"
	case agent == "claude", agent == "codex", agent == "pi", agent == "opencode", agent == "copilot":
	case agent == "rovodev":
		binary = "acli"
	default:
		return false
	}
	_, err := exec.LookPath(binary)
	return err == nil
}

// projectSettingsDisabled reports whether repoPath/.no-mistakes.yaml sets
// disable_project_settings: true (gate boundary). When true, the no-mistakes
// daemon does not load project AGENTS.md/settings into gate agents and
// enforces the neutralization gate: pi, codex, and claude are verified under
// the opt-out (no-mistakes >= 1.42.0), all other agents are refused.
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

func defaultNoMistakesPreflight(repoPath string, step taskauthority.DeliveryStep) error {
	probe := ProbeNoMistakesTool(toolEntryOf(step))
	if probe.State != backend.Ready {
		return noMistakesCommandBlocker(probe)
	}
	cfg, err := loadNoMistakesConfig()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = noMistakesConfig{} // fresh host: no config → daemon defaults (agent: auto)
		} else {
			return &GateBlockerError{
				Category: GateBlockerConfigMismatch,
				Detail:   fmt.Sprintf("reading no-mistakes config: %v", err),
				Guidance: "fix ~/.no-mistakes/config.yaml (or $NM_HOME/config.yaml)",
			}
		}
	}
	gateProbe := ProbeNoMistakesGateAgent(repoPath, cfg, agentAvailable, func() ProbeResult { return probe })
	if gateProbe.Blocker != nil {
		return gateProbe.Blocker
	}
	if _, _, err := projectGate(repoPath); err != nil {
		return err
	}
	return nil
}

// projectGate is the one owner of "the project's gate repo": the repository the
// primary's no-mistakes remote names, which must sit directly under
// NMHome()/repos, and the gate state database beside it. A fenced soldier
// cannot run `no-mistakes init` (it writes the common dir's .git/config), so a
// primary with no such gate is a blocker, not something the soldier repairs.
func projectGate(primary string) (repo, state string, err error) {
	notInit := func(detail string) error {
		return &GateBlockerError{
			Category: GateBlockerNotInitialized,
			Detail:   detail,
			Guidance: fmt.Sprintf("run `no-mistakes init` in %s", primary),
		}
	}
	out, cmdErr := exec.Command("git", "-C", primary, "config", "--get", "remote.no-mistakes.url").Output()
	if cmdErr != nil {
		return "", "", notInit("the primary has no no-mistakes remote")
	}
	repos, err := canonicalPath(filepath.Join(NMHome(), "repos"))
	if err != nil {
		return "", "", notInit(fmt.Sprintf("the no-mistakes repos directory is unreadable: %v", err))
	}
	repo, err = canonicalPath(strings.TrimSpace(string(out)))
	if err != nil || filepath.Dir(repo) != repos {
		return "", "", notInit(fmt.Sprintf("the no-mistakes remote %q is not a gate repo under %s", strings.TrimSpace(string(out)), repos))
	}
	return repo, filepath.Join(filepath.Dir(repos), "state.sqlite"), nil
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

// preflightDelivery runs the delivery-level mode preflight before
// worktree acquisition. It verifies environmental readiness for the
// resolved delivery mode (e.g. gh auth, remotes).
func (r *Runner) preflightDelivery() error {
	result, err := Preflight(r.effectiveMode, r.projPath, r.forgeStep)
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
