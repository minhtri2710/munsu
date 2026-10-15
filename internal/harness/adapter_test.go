package harness

import (
	"strings"
	"testing"
)

func TestAdapters_AllVerifiedPresent(t *testing.T) {
	for _, name := range []string{Claude, Codex, Opencode, Pi, Grok, Agy} {
		a, ok := GetAdapter(name)
		if !ok {
			t.Errorf("missing adapter for verified harness %q", name)
			continue
		}
		if a.Name != name {
			t.Errorf("adapter %q has Name=%q", name, a.Name)
		}
		if len(a.EnvMarkers) == 0 {
			t.Errorf("adapter %q has no EnvMarkers", name)
		}
		if len(a.ProcessMatchers) == 0 {
			t.Errorf("adapter %q has no ProcessMatchers", name)
		}
	}
}

func TestAdapters_CaptainLaunchContracts(t *testing.T) {
	for _, name := range []string{Claude, Codex, Opencode, Pi, Grok, Agy} {
		t.Run(name, func(t *testing.T) {
			a, ok := GetAdapter(name)
			if !ok {
				t.Fatal("missing adapter")
			}
			if name == Pi {
				if !a.CaptainLaunch.Supported {
					t.Fatal("pi captain launch contract must be supported")
				}
				if a.CaptainLaunch.ProjectArg || !a.CaptainLaunch.PromptArg || a.CaptainLaunch.Separator != "" {
					t.Fatalf("pi captain launch contract = %+v", a.CaptainLaunch)
				}
				return
			}
			if a.CaptainLaunch.Supported {
				t.Fatalf("%s captain launch must fail closed until verified", name)
			}
		})
	}
}

func TestAdapters_GetAdapterUnknown(t *testing.T) {
	_, ok := GetAdapter("nonexistent")
	if ok {
		t.Error("GetAdapter('nonexistent') should return false")
	}
}

func TestAdapters_FailureAndReadyPatterns(t *testing.T) {
	patterns := GetFailurePatterns(Pi)
	if len(patterns) == 0 || patterns[0] != "OPENAI_API_KEY" {
		t.Fatalf("GetFailurePatterns(pi) = %v, want OPENAI_API_KEY", patterns)
	}
	if got := HasFailurePattern("AuthenticationError: invalid key", Pi); !got {
		t.Fatal("HasFailurePattern did not find the pi authentication failure")
	}
	if got := HasFailurePattern("working normally", Pi); got {
		t.Fatal("HasFailurePattern matched unrelated capture")
	}
	if got := HasReadyPattern("Ready for your prompt", Agy); !got {
		t.Fatal("HasReadyPattern did not find the agy ready marker")
	}

	// The claude captures are the pane text measured on claude 2.1.289 under
	// the soldier posture (bypassPermissions).
	const (
		untrusted = "Quick safety check: Is this a project you created or one you trust?\n\n❯ No, exit\n  Yes, I trust this folder\n\nEnter to confirm · Esc to cancel"
		narrow    = "Quick safety check: Is this a"
		busy      = "Claude Code v2.1.289\n❯ \n⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"
		idle      = "Claude Code v2.1.289\n❯ \n⏵⏵ bypass permissions on (shift+tab to cycle)"
		// Measured on claude 2.1.300 with no login.
		loggedOut = "❯\n⏵⏵ bypass permissions on (shift+tab to cycle) Not logged in · Run /login\n◐ medium · /effort"
		loggedIn  = "❯\n⏵⏵ bypass permissions on (shift+tab to cycle)\n◐ medium · /effort"
		quoted    = "❯ Fix the banner that says Not logged in after login.\n⏵⏵ bypass permissions on (shift+tab to cycle)\n◐ medium · /effort"
	)
	for _, c := range []struct {
		name, capture         string
		trust, failure, ready bool
	}{
		{"untrusted dialog", untrusted, false, true, false},
		{"untrusted dialog narrow", narrow, false, true, false},
		{"busy", busy, false, false, true},
		{"idle", idle, false, false, true},
		{"not logged in", loggedOut, false, true, true},
		{"logged in", loggedIn, false, false, true},
		{"brief quotes the phrase", quoted, false, false, true},
	} {
		if got := IsTrustPrompt(c.capture, Claude); got != c.trust {
			t.Errorf("%s: IsTrustPrompt = %v, want %v (Enter answers \"No, exit\")", c.name, got, c.trust)
		}
		if got := HasFailurePattern(c.capture, Claude); got != c.failure {
			t.Errorf("%s: HasFailurePattern = %v, want %v", c.name, got, c.failure)
		}
		if got := HasReadyPattern(c.capture, Claude); got != c.ready {
			t.Errorf("%s: HasReadyPattern = %v, want %v", c.name, got, c.ready)
		}
	}
}

func TestAdapters_DetectEnvMatchesAdapter(t *testing.T) {
	// Verify that the env markers in the adapter registry are the same ones
	// detectFromEnv() checks. For each verified harness, set its first env
	// marker and verify detection returns that harness.
	for _, name := range []string{Claude, Codex, Opencode, Grok, Agy} {
		t.Run(name, func(t *testing.T) {
			clearEnvMarkers(t)
			a, ok := GetAdapter(name)
			if !ok {
				t.Fatal("missing adapter")
			}
			if len(a.EnvMarkers) == 0 {
				t.Fatal("no env markers")
			}
			env := a.EnvMarkers[0]
			t.Setenv(env, "1")

			got := detectFromEnv()
			if got != name {
				t.Errorf("detectFromEnv() with %q set = %q, want %q", env, got, name)
			}
		})
	}
}

func TestAdapters_ProcessDetectionMatchesAdapter(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"claude", Claude},
		{"claude-code", Claude},
		{"codex", Codex},
		{"opencode", Opencode},
		{"pi", Pi},
		{"pi-coding-agent", Pi},
		{"grok", Grok},
		{"agy", Agy},
		{"antigravity", Agy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchProcessName(tt.name)
			if got != tt.want {
				t.Errorf("matchProcessName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestAdapters_RegistryImmutability(t *testing.T) {
	// Verify that getting an adapter returns a copy that doesn't affect the registry
	a, ok := GetAdapter(Claude)
	if !ok {
		t.Fatal("missing adapter")
	}

	a.Name = "modified"
	a2, _ := GetAdapter(Claude)
	if a2.Name == "modified" {
		t.Error("modifying a returned Adapter should not affect the registry")
	}
}

func TestStateArtifactsForHarness(t *testing.T) {
	tests := []struct {
		name     string
		harness  string
		expected []string
	}{
		{"pi returns pi-ext.ts and the per-task agent dir", Pi, []string{"pi-ext.ts", PiAgentDirSuffix}},
		{"grok returns grok-turnend-token", Grok, []string{"grok-turnend-token"}},
		{"claude returns its per-task config dir", Claude, []string{ClaudeConfigDirSuffix}},
		{"codex has no artifacts", Codex, nil},
		{"opencode has no artifacts", Opencode, nil},
		{"agy has no artifacts", Agy, nil},
		{"unknown harness returns nil", "nonexistent", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StateArtifactsForHarness(tt.harness)
			if len(got) != len(tt.expected) {
				t.Errorf("StateArtifactsForHarness(%q) = %v, want %v", tt.harness, got, tt.expected)
				return
			}
			for i, v := range got {
				if v != tt.expected[i] {
					t.Errorf("StateArtifactsForHarness(%q)[%d] = %q, want %q", tt.harness, i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestStateArtifactsForHarness_ReturnsCopy(t *testing.T) {
	got := StateArtifactsForHarness(Pi)
	if len(got) == 0 {
		t.Fatal("expected artifacts for pi")
	}
	// Modify the returned slice
	got[0] = "modified"
	// Get the artifacts again and verify the original is unchanged
	got2 := StateArtifactsForHarness(Pi)
	if got2[0] != "pi-ext.ts" {
		t.Errorf("modifying returned slice should not affect registry, got %q", got2[0])
	}
}

func TestQuestionDeny(t *testing.T) {
	for name, want := range map[string]string{
		Pi:     "--exclude-tools ask_user_question",
		Claude: "--disallowedTools AskUserQuestion",
	} {
		if got := strings.Join(Adapters[name].QuestionDeny, " "); got != want {
			t.Errorf("%s QuestionDeny = %q, want %q", name, got, want)
		}
	}
}

func TestProcessMatches(t *testing.T) {
	tests := []struct {
		harness, process string
		want             bool
	}{
		{Claude, "claude", true},
		{Claude, "  claude \n", true},
		{Claude, "Claude-Code", true},
		{Pi, "pi", true},
		{Pi, "claude", false},
		{Claude, "pi", false},
		{Grok, "grok-cli-1.2", true}, // substring matcher
		{"", "claude", false},
		{"", "", false},
		{"", "bash", false},
		{"nonexistent", "claude", false},
		{Claude, "", false},
		{Claude, "bash", false},
	}
	for _, tt := range tests {
		if got := ProcessMatches(tt.harness, tt.process); got != tt.want {
			t.Errorf("ProcessMatches(%q, %q) = %v, want %v", tt.harness, tt.process, got, tt.want)
		}
	}
}

func TestAdapters_SoldierLaunchWorktreeFiles(t *testing.T) {
	for name, want := range map[string][]string{
		Pi:     {".pi/settings.json"},
		Claude: nil,
	} {
		a, ok := GetAdapter(name)
		if !ok {
			t.Fatalf("missing adapter for %q", name)
		}
		if got := a.SoldierLaunch.WorktreeFiles; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s SoldierLaunch.WorktreeFiles = %v, want %v", name, got, want)
		}
	}
}
