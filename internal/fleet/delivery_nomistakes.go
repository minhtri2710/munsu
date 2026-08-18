package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/minhtri2710/munsu/internal/backend"
)

// MinNoMistakesVersion is the minimum compatible no-mistakes version.
// Versions below this threshold may not support required axi subcommands.
const MinNoMistakesVersion = "1.20.0"

// GateBlockerCategory is the exact reason a no-mistakes delivery preflight
// blocks a spawn.
type GateBlockerCategory string

const (
	// GateBlockerNone means no blocker: the effective gate agent is available
	// and can neutralize under the effective project settings.
	GateBlockerNone GateBlockerCategory = ""
	// GateBlockerAgentUnavailable: the effective gate agent is not on PATH.
	GateBlockerAgentUnavailable GateBlockerCategory = "agent-unavailable"
	// GateBlockerUnsupportedNeutralization: the effective gate agent is
	// installed but has no verified neutralization path under the effective
	// settings.
	GateBlockerUnsupportedNeutralization GateBlockerCategory = "unsupported-neutralization"
	// GateBlockerConfigMismatch: the effective no-mistakes config is
	// unreadable or selects a gate agent munsu cannot resolve.
	GateBlockerConfigMismatch GateBlockerCategory = "config-mismatch"
	// GateBlockerCommandFailure: the no-mistakes CLI itself failed to respond
	// (absent binary, unsupported version, or broken command surface).
	GateBlockerCommandFailure GateBlockerCategory = "command-failure"
)

// GateBlockerError is a typed no-mistakes gate blocker with an exact category
// and actionable supported delivery-mode guidance.
type GateBlockerError struct {
	Category GateBlockerCategory
	Detail   string
	Guidance string
}

func (e *GateBlockerError) Error() string {
	if e == nil {
		return "no-mistakes delivery blocked"
	}
	if e.Guidance == "" {
		return fmt.Sprintf("no-mistakes delivery blocked (%s): %s", e.Category, e.Detail)
	}
	return fmt.Sprintf("no-mistakes delivery blocked (%s): %s; %s", e.Category, e.Detail, e.Guidance)
}

// GateAgentProbe is the outcome of probing the effective no-mistakes config
// and the selected gate agent capability before spawning a no-mistakes
// Soldier. Blocker is nil when the effective gate agent can run the gate.
type GateAgentProbe struct {
	NoMistakes             ProbeResult       `json:"noMistakes"`
	DisableProjectSettings bool              `json:"disableProjectSettings"`
	Agents                 []string          `json:"agents"`
	Selected               string            `json:"selected,omitempty"`
	Blocker                *GateBlockerError `json:"blocker,omitempty"`
}

// verifiedNeutralizersUnderOptOut lists the gate agents verified (no-mistakes
// >= 1.42.0; installed reference 1.45.4) to suppress project agent
// instructions when disable_project_settings: true. opencode, copilot, and
// rovodev have no verified knob and are refused by the daemon gate even under
// the opt-out.
var verifiedNeutralizersUnderOptOut = map[string]bool{"pi": true, "codex": true, "claude": true}

// gateAgentProbeOrder mirrors the no-mistakes native-agent auto-detect
// priority (claude, codex, opencode, rovodev, pi, copilot).
var gateAgentProbeOrder = []string{"claude", "codex", "opencode", "rovodev", "pi", "copilot"}

// ProbeNoMistakesGateAgent probes the effective no-mistakes config and the
// selected gate agent capability before spawning a no-mistakes Soldier. It
// reports the exact blocker category: command-failure (CLI unavailable),
// agent-unavailable (configured gate agent not on PATH), config-mismatch
// (unknown/unreadable effective config), or unsupported-neutralization (the
// gate agent cannot neutralize under the effective project settings).
func ProbeNoMistakesGateAgent(repoPath string, cfg noMistakesConfig, available func(string) bool, probe func() ProbeResult) GateAgentProbe {
	p := GateAgentProbe{DisableProjectSettings: projectSettingsDisabled(repoPath)}
	p.NoMistakes = probe()
	if p.NoMistakes.State != backend.Ready {
		p.Blocker = noMistakesCommandBlocker(p.NoMistakes)
		return p
	}

	agents, mismatch := resolveGateAgents(cfg)
	if mismatch != "" {
		p.Blocker = &GateBlockerError{
			Category: GateBlockerConfigMismatch,
			Detail:   mismatch,
			Guidance: "set a supported gate agent in ~/.no-mistakes/config.yaml: pi, codex, claude, opencode, rovodev, copilot, or acp:<target>",
		}
		return p
	}
	p.Agents = agents

	availableAgents := make([]string, 0, len(agents))
	for _, agent := range agents {
		if available(agent) {
			availableAgents = append(availableAgents, agent)
		}
	}
	if len(availableAgents) == 0 {
		p.Blocker = &GateBlockerError{
			Category: GateBlockerAgentUnavailable,
			Detail:   fmt.Sprintf("no configured gate agent is on PATH (configured: %s)", strings.Join(agents, ", ")),
			Guidance: "install one of the configured gate agents, or select an installed agent in ~/.no-mistakes/config.yaml",
		}
		return p
	}
	p.Selected = availableAgents[0]

	// Under the trusted opt-out the daemon enforces the neutralization gate
	// for the whole fallback set; without it, munsu guards the run when the
	// repo ships agent instructions the gate agent would adopt.
	if !p.DisableProjectSettings && !repoHasAgentInstructions(repoPath) {
		return p
	}

	if p.DisableProjectSettings {
		var failing []string
		for _, agent := range availableAgents {
			if !agentNeutralizes(agent, true, cfg.AgentArgsOverride) {
				failing = append(failing, agent)
			}
		}
		if len(failing) > 0 {
			p.Blocker = &GateBlockerError{
				Category: GateBlockerUnsupportedNeutralization,
				Detail:   fmt.Sprintf("gate agent(s) %s cannot neutralize under disable_project_settings: true (verified: pi, codex, claude)", strings.Join(failing, ", ")),
				Guidance: "select pi, codex, or claude in ~/.no-mistakes/config.yaml, or remove disable_project_settings from .no-mistakes.yaml",
			}
		}
		return p
	}

	neutralized := false
	for _, agent := range availableAgents {
		if agentNeutralizes(agent, false, cfg.AgentArgsOverride) {
			neutralized = true
			break
		}
	}
	if !neutralized {
		p.Blocker = &GateBlockerError{
			Category: GateBlockerUnsupportedNeutralization,
			Detail:   fmt.Sprintf("no available gate agent (%s) can neutralize this repository's AGENTS.md/CLAUDE.md without disable_project_settings", strings.Join(availableAgents, ", ")),
			Guidance: "enable disable_project_settings: true in .no-mistakes.yaml, select codex or claude with preserved neutralization in ~/.no-mistakes/config.yaml, or pass --mode direct-PR",
		}
	}
	return p
}

// noMistakesCommandBlocker maps a non-Ready binary/version probe to the
// command-failure blocker category.
func noMistakesCommandBlocker(result ProbeResult) *GateBlockerError {
	guidance := "run 'munsu doctor' or 'go install github.com/kunchenguid/no-mistakes@latest'"
	return &GateBlockerError{
		Category: GateBlockerCommandFailure,
		Detail:   fmt.Sprintf("no-mistakes CLI not runnable: %s", result.String()),
		Guidance: guidance,
	}
}

// resolveGateAgents expands the effective configured gate agents, expanding
// "auto" to the no-mistakes native probe order. Unknown agent names return a
// config-mismatch detail.
func resolveGateAgents(cfg noMistakesConfig) ([]string, string) {
	configured := cfg.Agents
	if len(configured) == 0 {
		configured = []string{"auto"}
	}
	var out []string
	for _, agent := range configured {
		if agent == "auto" {
			out = append(out, gateAgentProbeOrder...)
			continue
		}
		if !knownGateAgent(agent) {
			return nil, fmt.Sprintf("no-mistakes config selects unknown gate agent %q", agent)
		}
		out = append(out, agent)
	}
	return out, ""
}

func knownGateAgent(agent string) bool {
	switch agent {
	case "claude", "codex", "opencode", "rovodev", "pi", "copilot":
		return true
	}
	return strings.HasPrefix(agent, "acp:")
}

// repoHasAgentInstructions reports whether the repo ships project agent
// instruction files the gate agent would load without the trusted opt-out.
func repoHasAgentInstructions(repoPath string) bool {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join(repoPath, name)); err == nil {
			return true
		}
	}
	return false
}

// agentNeutralizes reports whether the named gate agent has a verified
// neutralization path under the effective settings. Under the trusted opt-out
// (disable_project_settings: true), pi/codex/claude neutralize with their
// default knob (verified no-mistakes >= 1.42.0); without the opt-out only
// codex (project_doc_max_bytes=0 preserved) and claude (project/local
// setting-sources excluded) are verified.
func agentNeutralizes(agent string, optOut bool, overrides map[string][]string) bool {
	if optOut {
		return verifiedNeutralizersUnderOptOut[agent]
	}
	switch agent {
	case "codex":
		return codexNeutralizationPreserved(overrides[agent])
	case "claude":
		return claudeNeutralizationPreserved(overrides[agent])
	}
	return false // pi, opencode, copilot, rovodev, acp: no verified knob without the opt-out
}

// ProbeResult captures the result of a no-mistakes capability probe.
type ProbeResult struct {
	State   backend.State `json:"state"`
	Path    string        `json:"path,omitempty"`
	Version string        `json:"version,omitempty"`
	Detail  string        `json:"detail,omitempty"`
}

// String returns a human-readable summary of the probe result.
func (p ProbeResult) String() string {
	switch p.State {
	case backend.Absent:
		return "no-mistakes: absent"
	case backend.Unsupported:
		return fmt.Sprintf("no-mistakes: unsupported (version %s)", p.Version)
	case backend.Ready:
		return fmt.Sprintf("no-mistakes: ready (%s at %s)", p.Version, p.Path)
	case backend.Failed:
		return fmt.Sprintf("no-mistakes: failed (%s)", p.Detail)
	default:
		return fmt.Sprintf("no-mistakes: unknown state")
	}
}

// NoMistakesProbe probes no-mistakes availability end-to-end:
// binary presence, version parsing, version compatibility, and axi command surface.
// It does not modify any state and is safe to call repeatedly.
func NoMistakesProbe() ProbeResult {
	path, err := exec.LookPath("no-mistakes")
	if err != nil {
		return ProbeResult{
			State:  backend.Absent,
			Detail: "no-mistakes not found on PATH",
		}
	}

	out, err := exec.Command("no-mistakes", "--version").Output()
	if err != nil {
		return ProbeResult{
			State:  backend.Failed,
			Path:   path,
			Detail: fmt.Sprintf("cannot check version: %v", err),
		}
	}
	ver := strings.TrimSpace(string(out))
	if ver == "" {
		return ProbeResult{
			State:  backend.Failed,
			Path:   path,
			Detail: "no-mistakes --version returned empty output",
		}
	}

	cleanVer := strings.TrimPrefix(ver, "v")
	// Handle format: "no-mistakes version v1.40.0 (87a5477) ..."
	// Strip leading "no-mistakes version " if present
	if strings.HasPrefix(cleanVer, "no-mistakes version ") {
		cleanVer = cleanVer[len("no-mistakes version "):]
		cleanVer = strings.TrimPrefix(cleanVer, "v")
	}
	// Extract first version component (e.g. "1.40.0" from "1.40.0 (87a5477)")
	if idx := strings.IndexAny(cleanVer, " ("); idx > 0 {
		cleanVer = cleanVer[:idx]
	}

	parsed, err := semver.NewVersion(cleanVer)
	if err != nil {
		return ProbeResult{
			State:   backend.Failed,
			Path:    path,
			Version: ver,
			Detail:  fmt.Sprintf("cannot parse version %q: %v", ver, err),
		}
	}

	minVer, err := semver.NewVersion(MinNoMistakesVersion)
	if err != nil {
		return ProbeResult{
			State:  backend.Failed,
			Path:   path,
			Detail: fmt.Sprintf("invalid minimum version %q: %v", MinNoMistakesVersion, err),
		}
	}

	if parsed.LessThan(minVer) {
		return ProbeResult{
			State:   backend.Unsupported,
			Path:    path,
			Version: parsed.String(),
			Detail:  fmt.Sprintf("no-mistakes version %s < minimum %s", parsed.String(), MinNoMistakesVersion),
		}
	}

	// Verify the axi command surface is available by probing axi status help.
	axiOut, err := exec.Command("no-mistakes", "axi", "status", "--help").Output()
	if err != nil {
		return ProbeResult{
			State:   backend.Failed,
			Path:    path,
			Version: parsed.String(),
			Detail:  fmt.Sprintf("axi command surface not available: %v", err),
		}
	}
	if !strings.Contains(string(axiOut), "status") {
		return ProbeResult{
			State:   backend.Failed,
			Path:    path,
			Version: parsed.String(),
			Detail:  "axi status subcommand not recognized",
		}
	}

	return ProbeResult{
		State:   backend.Ready,
		Path:    path,
		Version: parsed.String(),
		Detail:  "found on PATH, version compatible, axi surface available",
	}
}
