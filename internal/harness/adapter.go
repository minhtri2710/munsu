// Package harness detects the running agent harness, resolves soldier/captain
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

// CaptainLaunchContract describes a verified shell-free captain invocation.
type CaptainLaunchContract struct {
	Supported  bool
	CwdAtHome  bool
	Separator  string
	ProjectArg bool
	PromptArg  bool
}

// Adapter describes a verified agent harness with detection, launch,
// supervision facts, and state artifact cleanup. Each adapter is populated
// from empirically verified observations (see the captain provisioning skill).
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

	// CaptainLaunch is populated only when the general CLI contract is verified.
	CaptainLaunch CaptainLaunchContract

	// TrustDialog describes the trust/permission dialog behavior on first launch.
	// ReadyPatterns is a list of substrings that indicate the agent is ready
	// for input. When empty, DefaultReadyPatterns is used.
	ReadyPatterns []string

	// TrustPatterns is a list of substrings that indicate a first-run trust/permission
	// dialog that should be auto-dismissed.
	TrustPatterns []string

	// FailurePatterns is a list of substrings that indicate a launch failure
	// (auth errors, model unavailable, etc.) so the handshake can abort early
	// instead of waiting for the full ready timeout.
	FailurePatterns []string

	// TrustDialog describes the trust/permission dialog behavior on first launch.
	TrustDialog string

	// SupervisionProtocol identifies the supervision protocol for this harness.
	SupervisionProtocol string

	// StateArtifacts lists the state file suffixes (excluding the task ID prefix)
	// that this harness creates in the munsu state directory.
	// These are cleaned up during teardown alongside munsu-native artifacts.
	StateArtifacts []string
}

// Adapters is the registry
// This is the authoritative source of harness metadata; Templates and detection
// functions derive from it.
var Adapters = map[string]Adapter{
	Claude: {
		Name:       Claude,
		EnvMarkers: []string{"CLAUDE_CODE"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "claude"},
			{Name: "claude-code"},
			{Name: "claude code"},
		},
		BusyPattern:     `esc to interrupt`,
		ReadyPatterns:   []string{">", "ready"},
		TrustPatterns:   nil,
		FailurePatterns:   []string{"Auth required", "not authenticated"},
		ExitCommand:     `/exit`,
		InterruptKeys:   `Escape`,
		SkillInvocation: `/`,
		TurnEndHook:     `Stop hook (exit 2 + stderr); Primary-only global ~/.claude/hooks/`,
		LaunchTemplate: Template{
			ModelFlag:    "--model",
			DefaultModel: "claude-sonnet-4-20250515",
		},
		TrustDialog:         `Trust or bypass-permissions confirmation on first launch per worktree`,
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
		BusyPattern:     `esc to interrupt`,
		ReadyPatterns:   nil,
		TrustPatterns:   nil,
		FailurePatterns:   []string{"AuthenticationError", "Incorrect API key"},
		ExitCommand:     `/quit`,
		InterruptKeys:   `Escape`,
		SkillInvocation: `$`,
		TurnEndHook:     `Stop hook (exit 2 + stderr); Primary-only ~/.codex/hooks.json`,
		LaunchTemplate: Template{
			ModelFlag:     "--model",
			EffortFlag:    "--effort",
			DefaultModel:  "gpt-5.2-codex",
			DefaultEffort: "80",
		},
		TrustDialog:         `Directory trust dialog on first run per repo root; accept with Enter`,
		SupervisionProtocol: `codex`,
	},
	Opencode: {
		Name:       Opencode,
		EnvMarkers: []string{"OPENCODE"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "opencode"},
			{Name: "opencode", Substr: true},
		},
		BusyPattern:     `esc interrupt`,
		ReadyPatterns:   nil,
		TrustPatterns:   nil,
		FailurePatterns:   []string{"AuthenticationError", "Incorrect API key"},
		ExitCommand:     `/exit`,
		InterruptKeys:   `Escape Escape`,
		SkillInvocation: `/`,
		TurnEndHook:     `Passive session.idle plugin; Primary-only .opencode/plugins/`,
		LaunchTemplate: Template{
			ModelFlag:     "--model",
			EffortFlag:    "--effort",
			DefaultModel:  "gpt-5.2-codex",
			DefaultEffort: "80",
		},
		TrustDialog:         `No trust dialog`,
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
		BusyPattern:     `Working\.\.\.`,
		ReadyPatterns:   []string{">", "Agent:", "What would you like", "checkpoint", "thinking off", "◆"},
		TrustPatterns:   []string{"Trust project folder", "→ Trust", "Do not trust"},
		FailurePatterns:   []string{"OPENAI_API_KEY", "AuthenticationError"},
		ExitCommand:     `/quit`,
		InterruptKeys:   `Escape`,
		SkillInvocation: `/`,
		TurnEndHook:     `agent_settled extension with deliverAs: "followUp"; Primary-only .pi/extensions/`,
		LaunchTemplate: Template{
			ModelFlag:  "--model",
			EffortFlag: "--thinking",
		},
		CaptainLaunch: CaptainLaunchContract{
			Supported:  true,
			CwdAtHome:  true,
			ProjectArg: false,
			PromptArg:  true,
		},
		SupervisionProtocol: `pi`,
		TrustDialog:         `Project trust dialog on first run per path; accept with Enter`,
		StateArtifacts:      []string{"pi-ext.ts"},
	},
	Grok: {
		Name:       Grok,
		EnvMarkers: []string{"GROK_VM_ID", "GROK_AGENT"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "grok", Substr: true},
		},
		BusyPattern:     `Ctrl\+c:cancel`,
		ReadyPatterns:   nil,
		TrustPatterns:   nil,
		FailurePatterns:   []string{"Invalid API key"},
		IdlePattern:     `Shift\+Tab:mode`,
		ExitCommand:     `Ctrl+Q Ctrl+Q`,
		InterruptKeys:   `Ctrl+C`,
		SkillInvocation: `/`,
		TurnEndHook:     `Passive Stop hook (global ~/.grok/hooks/); Primary-only global hooks with workspace token`,
		LaunchTemplate: Template{
			ModelFlag:  "--model",
			EffortFlag: "--reasoning-effort",
		},
		TrustDialog:         `No trust dialog when launched from a git repo root`,
		SupervisionProtocol: `grok`,
		StateArtifacts:      []string{"grok-turnend-token"},
	},
	Agy: {
		Name:       Agy,
		EnvMarkers: []string{"ANTIGRAVITY_LS_ADDRESS", "ANTIGRAVITY_AGENT"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "agy"},
			{Name: "antigravity"},
		},
		BusyPattern:     `Thinking\.\.\.`,
		ReadyPatterns:   []string{"esc to cancel", "Ready for your prompt", "What would you like"},
		TrustPatterns:   []string{"Do you trust", "Yes, I trust this folder"},
		FailurePatterns:   []string{"Failed to authenticate"},
		IdlePattern:     `Press shift\+tab to cycle modes`,
		ExitCommand:     `Ctrl+Q Ctrl+Q`,
		InterruptKeys:   `Ctrl+C`,
		SkillInvocation: `/`,
		TurnEndHook:     `Stop hook (stdout decision JSON); Primary-only .agents/hooks.json`,
		LaunchTemplate: Template{
			ModelFlag: "--model",
			ExtraArgs: []string{"--dangerously-skip-permissions"},
		},
		TrustDialog:         `File/command permission dialog per session; pre-approved commands persisted in settings.json`,
		SupervisionProtocol: `agy`,
	},
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

// StateArtifactsForHarness returns the harness-specific state file suffixes
// that should be cleaned up during teardown for the given harness name.
// Returns an empty slice if the harness is unknown or has no artifacts.
func StateArtifactsForHarness(name string) []string {
	a, ok := Adapters[name]
	if !ok {
		return nil
	}
	// Return a copy to prevent mutation of the registry
	artifacts := make([]string, len(a.StateArtifacts))
	copy(artifacts, a.StateArtifacts)
	return artifacts
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

// DefaultReadyPatterns are ready-check patterns used for any harness without
// specific ReadyPatterns in its adapter.
var DefaultReadyPatterns = []string{">", "$"}

// IsTrustPrompt reports whether capture contains a harness-specific trust
// prompt that should be auto-dismissed with Enter.
func IsTrustPrompt(capture, harnessName string) bool {
	a, ok := GetAdapter(harnessName)
	if !ok {
		return false
	}
	for _, p := range a.TrustPatterns {
		if strings.Contains(capture, p) {
			return true
		}
	}
	return false
}

// GetReadyPatterns returns the ready patterns for a given harness, or defaults
// when the harness has no specific patterns set.
func GetReadyPatterns(harnessName string) []string {
	a, ok := GetAdapter(harnessName)
	if !ok || len(a.ReadyPatterns) == 0 {
		return DefaultReadyPatterns
	}
	return a.ReadyPatterns
}

// HasReadyPattern reports whether capture contains any ready pattern for
// the given harness. Returns false for unknown harnesses.
func HasReadyPattern(capture, harnessName string) bool {
	patterns := GetReadyPatterns(harnessName)
	for _, p := range patterns {
		if strings.Contains(capture, p) {
			return true
		}
	}
	return false
}

// GetFailurePatterns returns the failure patterns for a given harness, or nil
// when the harness has no specific failure patterns set.
func GetFailurePatterns(harnessName string) []string {
	a, ok := GetAdapter(harnessName)
	if !ok {
		return nil
	}
	return a.FailurePatterns
}

// HasFailurePattern reports whether capture contains any failure pattern for
// the given harness. Returns false for unknown harnesses.
func HasFailurePattern(capture, harnessName string) bool {
	patterns := GetFailurePatterns(harnessName)
	for _, p := range patterns {
		if strings.Contains(capture, p) {
			return true
		}
	}
	return false
}
