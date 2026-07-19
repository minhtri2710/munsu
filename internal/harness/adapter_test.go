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
		if a.BusyPattern == "" {
			t.Errorf("adapter %q has empty BusyPattern", name)
		}
		if a.ExitCommand == "" {
			t.Errorf("adapter %q has empty ExitCommand", name)
		}
		if a.InterruptKeys == "" {
			t.Errorf("adapter %q has empty InterruptKeys", name)
		}
		if a.SkillInvocation == "" {
			t.Errorf("adapter %q has empty SkillInvocation", name)
		}
		if a.TurnEndHook == "" {
			t.Errorf("adapter %q has empty TurnEndHook", name)
		}
		if a.TrustDialog == "" {
			t.Errorf("adapter %q has empty TrustDialog", name)
		}
		if a.SupervisionProtocol == "" {
			t.Errorf("adapter %q has empty SupervisionProtocol", name)
		}
	}
}

func TestAdapters_SecondmateLaunchContracts(t *testing.T) {
	for _, name := range []string{Claude, Codex, Opencode, Pi, Grok, Agy} {
		t.Run(name, func(t *testing.T) {
			a, ok := GetAdapter(name)
			if !ok {
				t.Fatal("missing adapter")
			}
			if name == Pi {
				if !a.SecondmateLaunch.Supported {
					t.Fatal("pi secondmate launch contract must be supported")
				}
				if a.SecondmateLaunch.ProjectArg || !a.SecondmateLaunch.PromptArg || a.SecondmateLaunch.Separator != "" {
					t.Fatalf("pi secondmate launch contract = %+v", a.SecondmateLaunch)
				}
				return
			}
			if a.SecondmateLaunch.Supported {
				t.Fatalf("%s secondmate launch must fail closed until verified", name)
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

func TestAdapters_LaunchStringFromRegistry(t *testing.T) {
	for _, name := range []string{Claude, Codex, Opencode, Pi, Grok, Agy} {
		t.Run(name, func(t *testing.T) {
			a, ok := GetAdapter(name)
			if !ok {
				t.Fatal("missing adapter")
			}

			cmd := LaunchString(name, a.LaunchTemplate)

			// All launch strings should start with the harness name
			if !strings.HasPrefix(cmd, strings.ToLower(name)) {
				t.Errorf("LaunchString(%q) = %q, want prefix %q", name, cmd, name)
			}

			// If a ModelFlag is set, the command should contain it
			if a.LaunchTemplate.ModelFlag != "" {
				if !strings.Contains(cmd, a.LaunchTemplate.ModelFlag) {
					// Pi and Grok have ModelFlag but may not have DefaultModel, so they
					// won't include the flag if DefaultModel is empty
					if a.LaunchTemplate.DefaultModel != "" {
						t.Errorf("LaunchString(%q) = %q, should contain %q", name, cmd, a.LaunchTemplate.ModelFlag)
					}
				}
			}

			// If an EffortFlag is set and DefaultEffort is set, should contain both
			if a.LaunchTemplate.EffortFlag != "" && a.LaunchTemplate.DefaultEffort != "" {
				if !strings.Contains(cmd, a.LaunchTemplate.EffortFlag) {
					t.Errorf("LaunchString(%q) = %q, should contain %q", name, cmd, a.LaunchTemplate.EffortFlag)
				}
			}
		})
	}
}

func TestAdapters_SkillInvocationFormat(t *testing.T) {
	tests := []struct {
		harness string
		skill   string
		want    string
	}{
		{Claude, "no-mistakes", "/no-mistakes"},
		{Grok, "no-mistakes", "/no-mistakes"},
		{Opencode, "no-mistakes", "/no-mistakes"},
		{Pi, "no-mistakes", "/no-mistakes"},
		{Agy, "no-mistakes", "/no-mistakes"},
		{Codex, "no-mistakes", "$no-mistakes"},
	}
	for _, tt := range tests {
		t.Run(tt.harness, func(t *testing.T) {
			a, ok := GetAdapter(tt.harness)
			if !ok {
				t.Skip("adapter not in registry")
			}
			got := a.SkillInvocation + tt.skill
			if got != tt.want {
				t.Errorf("SkillInvocation for %q = %q, want %q", tt.harness, got, tt.want)
			}
		})
	}
}

func TestAdapters_LaunchTemplateMatchesRegistry(t *testing.T) {
	for _, name := range []string{Claude, Codex, Opencode, Pi, Grok, Agy} {
		t.Run(name, func(t *testing.T) {
			a, ok := GetAdapter(name)
			if !ok {
				t.Fatal("missing adapter")
			}
			tmpl, ok := Templates[name]
			if !ok {
				t.Fatal("missing template")
			}

			if a.LaunchTemplate.ModelFlag != tmpl.ModelFlag {
				t.Errorf("%s: adapter.ModelFlag=%q, Templates.ModelFlag=%q",
					name, a.LaunchTemplate.ModelFlag, tmpl.ModelFlag)
			}
			if a.LaunchTemplate.EffortFlag != tmpl.EffortFlag {
				t.Errorf("%s: adapter.EffortFlag=%q, Templates.EffortFlag=%q",
					name, a.LaunchTemplate.EffortFlag, tmpl.EffortFlag)
			}
			if a.LaunchTemplate.DefaultModel != tmpl.DefaultModel {
				t.Errorf("%s: adapter.DefaultModel=%q, Templates.DefaultModel=%q",
					name, a.LaunchTemplate.DefaultModel, tmpl.DefaultModel)
			}
			if a.LaunchTemplate.DefaultEffort != tmpl.DefaultEffort {
				t.Errorf("%s: adapter.DefaultEffort=%q, Templates.DefaultEffort=%q",
					name, a.LaunchTemplate.DefaultEffort, tmpl.DefaultEffort)
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

	a.BusyPattern = "modified"
	a2, _ := GetAdapter(Claude)
	if a2.BusyPattern == "modified" {
		t.Error("modifying a returned Adapter should not affect the registry")
	}
}

func TestStateArtifactsForHarness(t *testing.T) {
	tests := []struct {
		name     string
		harness  string
		expected []string
	}{
		{"pi returns pi-ext.ts", Pi, []string{"pi-ext.ts"}},
		{"grok returns grok-turnend-token", Grok, []string{"grok-turnend-token"}},
		{"claude has no artifacts", Claude, nil},
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
