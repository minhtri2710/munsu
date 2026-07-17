// Package harness detects the running agent harness, resolves crewmate/secondmate
// harness assignments, and provides the adapter registry for all verified harnesses.
package harness

import (
	"os"
	"strings"
)

// ProcessNameMatcher describes how to match a process name for harness detection.
type ProcessNameMatcher struct {
	// Name is the process executable name to match.
	Name string
	// Substr uses strings.Contains instead of exact equality when true.
	Substr bool
}

// Adapter describes a verified agent harness with detection, launch, and
// supervision facts. Each adapter is populated from empirically verified
// observations (see firstmate harness-adapters skill).
type Adapter struct {
	// Name is the harness identifier (matching the package-level constants).
	Name string

	// EnvMarkers is a list of environment variable names that identify the
	// harness when set to any non-empty value.
	EnvMarkers []string

	// ProcessMatchers lists process name patterns used for ancestry-based
	// detection, checked in order (exact matches first, then substring).
	ProcessMatchers []ProcessNameMatcher

	// BusyPattern is the regex pattern for detecting a busy agent pane.
	BusyPattern string

	// IdlePattern is the regex pattern for detecting an idle agent pane.
	IdlePattern string

	// ExitCommand is the command string to exit the harness.
	ExitCommand string

	// InterruptKeys describes the key sequence to interrupt a running turn.
	InterruptKeys string

	// SkillInvocation is the prefix character used for skill invocation
	// (e.g. "/" for claude/grok, "$" for codex).
	SkillInvocation string

	// TurnEndHook describes the turn-end hook mechanism for this harness.
	TurnEndHook string

	// LaunchTemplate contains the CLI flag conventions and defaults for spawning.
	LaunchTemplate Template

	// TrustDialog describes the trust/permission dialog behavior on first launch.
	TrustDialog string

	// SupervisionProtocol identifies the supervision protocol for this harness.
	SupervisionProtocol string
}

// Adapters is the registry of all verified harness adapters, keyed by harness name.
// This is the authoritative source of harness metadata; Templates and detection
// functions derive from it.
var Adapters = map[string]Adapter{
	Claude: {
		Name:        Claude,
		EnvMarkers:  []string{"CLAUDE_CODE"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "claude"},
			{Name: "claude-code"},
			{Name: "claude code"},
		},
		BusyPattern:       `esc to interrupt`,
		ExitCommand:       `/exit`,
		InterruptKeys:     `Escape`,
		SkillInvocation:   `/`,
		TurnEndHook:       `Stop hook (exit 2 + stderr); Primary-only global ~/.claude/hooks/`,
		LaunchTemplate: Template{
			ModelFlag:    "--model",
			DefaultModel: "claude-sonnet-4-20250515",
		},
		TrustDialog:        `Trust or bypass-permissions confirmation on first launch per worktree`,
		SupervisionProtocol: `claude`,
	},
	Codex: {
		Name:       Codex,
		EnvMarkers: []string{"CODECLIMB"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "codex"},
			{Name: "codeclimb"},
			{Name: "codex", Substr: true},
		},
		BusyPattern:       `esc to interrupt`,
		ExitCommand:       `/quit`,
		InterruptKeys:     `Escape`,
		SkillInvocation:   `$`,
		TurnEndHook:       `Stop hook (exit 2 + stderr); Primary-only ~/.codex/hooks.json`,
		LaunchTemplate: Template{
			ModelFlag:     "--model",
			EffortFlag:    "--effort",
			DefaultModel:  "gpt-5.2-codex",
			DefaultEffort: "80",
		},
		TrustDialog:        `Directory trust dialog on first run per repo root; accept with Enter`,
		SupervisionProtocol: `codex`,
	},
	Opencode: {
		Name:       Opencode,
		EnvMarkers: []string{"OPENCODE"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "opencode"},
			{Name: "opencode", Substr: true},
		},
		BusyPattern:       `esc interrupt`,
		ExitCommand:       `/exit`,
		InterruptKeys:     `Escape Escape`,
		SkillInvocation:   `/`,
		TurnEndHook:       `Passive session.idle plugin; Primary-only .opencode/plugins/`,
		LaunchTemplate: Template{
			ModelFlag:     "--model",
			EffortFlag:    "--effort",
			DefaultModel:  "gpt-5.2-codex",
			DefaultEffort: "80",
		},
		TrustDialog:        `No trust dialog`,
		SupervisionProtocol: `opencode`,
	},
	Pi: {
		Name:       Pi,
		EnvMarkers: []string{"PI_CODING_AGENT_DIR", "PI_CODING_AGENT"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "pi"},
			{Name: "pi-coding-agent"},
			{Name: "pi-coding", Substr: true},
		},
		BusyPattern:       `Working\.\.\.`,
		ExitCommand:       `/quit`,
		InterruptKeys:     `Escape`,
		SkillInvocation:   `/`,
		TurnEndHook:       `agent_settled extension with deliverAs: "followUp"; Primary-only .pi/extensions/`,
		LaunchTemplate: Template{
			ModelFlag:  "--model",
			EffortFlag: "--thinking",
		},
		TrustDialog:        `Project trust dialog on first run per path; accept with Enter`,
		SupervisionProtocol: `pi`,
	},
	Grok: {
		Name:       Grok,
		EnvMarkers: []string{"GROK_VM_ID", "GROK_AGENT"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "grok", Substr: true},
		},
		BusyPattern:       `Ctrl\+c:cancel`,
		IdlePattern:       `Shift\+Tab:mode`,
		ExitCommand:       `Ctrl+Q Ctrl+Q`,
		InterruptKeys:     `Ctrl+C`,
		SkillInvocation:   `/`,
		TurnEndHook:       `Passive Stop hook (global ~/.grok/hooks/); Primary-only global hooks with workspace token`,
		LaunchTemplate: Template{
			ModelFlag:  "--model",
			EffortFlag: "--reasoning-effort",
		},
		TrustDialog:        `No trust dialog when launched from a git repo root`,
		SupervisionProtocol: `grok`,
	},
	// Agy is not part of the 5-verified-harness Phase C scope but remains in
	// KnownHarnesses for backward compatibility. Its adapter is registered
	// separately via the existing Templates map and detection code.
}

// matchProcessNameFromAdapter checks a process name against the adapter registry.
// It first tries exact matches, then substring matches.
func matchProcessNameFromAdapter(name string) string {
	name = strings.ToLower(name)
	for _, a := range Adapters {
		for _, m := range a.ProcessMatchers {
			matchName := strings.ToLower(m.Name)
			if m.Substr {
				if strings.Contains(name, matchName) {
					return a.Name
				}
			} else {
				if name == matchName {
					return a.Name
				}
			}
		}
	}
	return ""
}

// GetAdapter returns the adapter for the given harness name, or false if not found.
func GetAdapter(name string) (Adapter, bool) {
	a, ok := Adapters[name]
	return a, ok
}

// detectEnvFromAdapter checks well-known environment variable markers from the
// adapter registry. Returns the harness name or empty string.
func detectEnvFromAdapter() string {
	for _, a := range Adapters {
		for _, env := range a.EnvMarkers {
			if os.Getenv(env) != "" {
				return a.Name
			}
		}
	}
	return ""
}
