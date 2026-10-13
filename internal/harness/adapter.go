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

// SoldierLaunchContract describes a verified shell-free soldier invocation:
// the prompt is the last argument, after Separator when one is set. It is
// separate from CaptainLaunchContract, which captain launch reads.
type SoldierLaunchContract struct {
	Supported bool
	// Args is the permission posture argv of a soldier launch.
	Args []string
	// SkillDeny is the argv that blocks OrchestrationSkill in the launch
	// itself. Empty when the harness blocks it through files the launch writes.
	SkillDeny []string
	Separator string
	// WorktreeFiles lists the worktree files, as slash-format relative paths,
	// that a launch of this harness writes. The launch manifest binds them
	// next to the core launch artifacts, and the soldier worktree's
	// worktree-scoped excludes file lists them so a launch never dirties
	// the worktree.
	WorktreeFiles []string
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

	// LaunchTemplate contains the CLI flag conventions and defaults for spawning.
	LaunchTemplate Template

	// CaptainLaunch is populated only when the general CLI contract is verified.
	CaptainLaunch CaptainLaunchContract

	// SoldierLaunch is populated only when the soldier CLI contract is verified;
	// a harness without it is refused for soldier launch.
	SoldierLaunch SoldierLaunchContract

	// QuestionDeny is the soldier launch argv that denies the harness's
	// ask-the-user tool; decision-hold is the only question path. Empty means
	// the deny cannot be expressed, so a soldier launch of this harness is
	// refused.
	QuestionDeny []string

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

	// StateArtifacts lists the state file suffixes (excluding the task ID prefix)
	// that this harness creates in the munsu state directory.
	// These are cleaned up during teardown alongside munsu-native artifacts.
	StateArtifacts []string
}

// Adapters is the registry and the authoritative source of harness metadata.
var Adapters = map[string]Adapter{
	Claude: {
		Name:       Claude,
		EnvMarkers: []string{"CLAUDECODE"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "claude"},
			{Name: "claude-code"},
			{Name: "claude code"},
		},
		// Measured on claude 2.1.289 under the soldier posture: the footer
		// "bypass permissions on" is in the busy and the idle capture.
		ReadyPatterns: []string{"bypass permissions on"},
		// No trust patterns: the folder-trust dialog defaults to "No, exit",
		// so an Enter would exit claude.
		TrustPatterns: nil,
		// The folder-trust dialog of an untrusted project repository; short
		// enough to fit one line at 38 columns.
		FailurePatterns: []string{"Quick safety check"},
		LaunchTemplate: Template{
			ModelFlag: "--model",
		},
		SoldierLaunch: SoldierLaunchContract{
			Supported: true,
			Args:      []string{"--permission-mode", "bypassPermissions"},
			// --disallowedTools is variadic: the question deny and the skill
			// deny are separate flags, and the separator ends the list.
			SkillDeny: []string{"--disallowedTools", "Skill(" + OrchestrationSkill + ")"},
			Separator: "--",
		},
		QuestionDeny:   []string{"--disallowedTools", "AskUserQuestion"},
		StateArtifacts: []string{ClaudeConfigDirSuffix},
	},
	Codex: {
		Name:       Codex,
		EnvMarkers: []string{"CODECLIMB"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "codex"},
			{Name: "codeclimb"},
			{Name: "codex", Substr: true},
		},
		ReadyPatterns:   nil,
		TrustPatterns:   nil,
		FailurePatterns: []string{"AuthenticationError", "Incorrect API key"},
	},
	Opencode: {
		Name:       Opencode,
		EnvMarkers: []string{"OPENCODE"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "opencode"},
			{Name: "opencode", Substr: true},
		},
		ReadyPatterns:   nil,
		TrustPatterns:   nil,
		FailurePatterns: []string{"AuthenticationError", "Incorrect API key"},
	},
	Pi: {
		Name:       Pi,
		EnvMarkers: []string{"PI_CODING_AGENT_DIR", "PI_CODING_AGENT"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "pi"},
			{Name: "pi-coding-agent"},
			{Name: "pi-coding", Substr: true},
		},
		ReadyPatterns:   []string{">", "Agent:", "What would you like", "checkpoint", "thinking off", "◆"},
		TrustPatterns:   []string{"Trust project folder", "→ Trust", "Do not trust"},
		FailurePatterns: []string{"OPENAI_API_KEY", "AuthenticationError"},
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
		SoldierLaunch: SoldierLaunchContract{Supported: true, WorktreeFiles: []string{PiProjectSettingsRelPath}},
		// pi's --exclude-tools disables tools by name after every other
		// selection; ask_user_question is the tool of the installed
		// rpiv-ask-user-question extension.
		QuestionDeny:   []string{"--exclude-tools", "ask_user_question"},
		StateArtifacts: []string{"pi-ext.ts", PiAgentDirSuffix},
	},
	Grok: {
		Name:       Grok,
		EnvMarkers: []string{"GROK_VM_ID", "GROK_AGENT"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "grok", Substr: true},
		},
		ReadyPatterns:   nil,
		TrustPatterns:   nil,
		FailurePatterns: []string{"Invalid API key"},
		LaunchTemplate: Template{
			ModelFlag:  "--model",
			EffortFlag: "--reasoning-effort",
		},
		StateArtifacts: []string{"grok-turnend-token"},
	},
	Agy: {
		Name:       Agy,
		EnvMarkers: []string{"ANTIGRAVITY_LS_ADDRESS", "ANTIGRAVITY_AGENT"},
		ProcessMatchers: []ProcessNameMatcher{
			{Name: "agy"},
			{Name: "antigravity"},
		},
		ReadyPatterns:   []string{"esc to cancel", "Ready for your prompt", "What would you like"},
		TrustPatterns:   []string{"Do you trust", "Yes, I trust this folder"},
		FailurePatterns: []string{"Failed to authenticate"},
		LaunchTemplate: Template{
			ModelFlag: "--model",
			ExtraArgs: []string{"--dangerously-skip-permissions"},
		},
	},
}

// matchProcessNameFromAdapter checks a process name against the adapter registry.
// It first tries exact matches, then substring matches.
func matchProcessNameFromAdapter(name string) string {
	name = strings.ToLower(name)
	for _, harnessName := range KnownHarnesses {
		a := Adapters[harnessName]
		for _, m := range a.ProcessMatchers {
			if !m.Substr && name == strings.ToLower(m.Name) {
				return a.Name
			}
		}
	}
	for _, harnessName := range KnownHarnesses {
		a := Adapters[harnessName]
		for _, m := range a.ProcessMatchers {
			if m.Substr && strings.Contains(name, strings.ToLower(m.Name)) {
				return a.Name
			}
		}
	}
	return ""
}

// ProcessMatches reports whether a running process name belongs to the named
// harness. It defers to matchProcessNameFromAdapter so exact-before-substring
// precedence has one owner; an empty or unknown harness never matches.
func ProcessMatches(harnessName, process string) bool {
	return harnessName != "" && matchProcessNameFromAdapter(strings.TrimSpace(process)) == harnessName
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
	for _, harnessName := range KnownHarnesses {
		a := Adapters[harnessName]
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
