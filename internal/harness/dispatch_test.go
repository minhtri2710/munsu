package harness

import "testing"

// ResolveDispatch is a test helper wrapping ResolveDispatchSelection.
func ResolveDispatch(cfg *DispatchConfig, taskDesc string) string {
	sel, _ := ResolveDispatchSelection(cfg, taskDesc)
	return sel.Harness
}

func TestResolveDispatch(t *testing.T) {
	cfg := &DispatchConfig{
		DefaultHarness: "pi",
		Profiles: []DispatchProfile{
			{
				Name:    "code-review",
				Match:   []string{"review", "audit", "check"},
				Harness: "codex",
			},
			{
				Name:    "research",
				Match:   []string{"research", "investigate", "search"},
				Harness: "claude",
			},
			{
				Name:    "data-pipeline",
				Match:   []string{"data pipeline"},
				Harness: "codex",
			},
			{
				Name:    "default-catchall",
				Match:   []string{"*"},
				Harness: "pi",
			},
		},
	}

	tests := []struct {
		desc string
		want string
	}{
		{"review this code", "codex"},
		{"audit the security", "codex"},
		{"check for issues", "codex"},
		{"research the topic", "claude"},
		{"investigate the bug", "claude"},
		{"search for occurrences", "claude"},
		{"build data pipeline", "codex"},
		{"implement the feature", "pi"},
		{"", "pi"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := ResolveDispatch(cfg, tt.desc)
			if got != tt.want {
				t.Errorf("ResolveDispatch(_, %q) = %q, want %q", tt.desc, got, tt.want)
			}
		})
	}
}

func TestResolveDispatch_NoMatchNoDefault(t *testing.T) {
	cfg := &DispatchConfig{
		Profiles: []DispatchProfile{
			{
				Name:    "code-review",
				Match:   []string{"review"},
				Harness: "codex",
			},
		},
	}
	got := ResolveDispatch(cfg, "implement feature")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestResolveDispatch_EmptyConfig(t *testing.T) {
	cfg := &DispatchConfig{}
	got := ResolveDispatch(cfg, "anything")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestResolveDispatchSelection_ModelEffort(t *testing.T) {
	cfg := &DispatchConfig{
		DefaultHarness: "pi",
		DefaultModel:   "default-model",
		DefaultEffort:  "low",
		Profiles: []DispatchProfile{
			{
				Name:     "review",
				Match:    []string{"review"},
				Harness:  "codex",
				Model:    "gpt-5.2-codex",
				Effort:   "high",
				Scaffold: "guided",
			},
			{
				Name:    "all",
				Match:   []string{"*"},
				Harness: "pi",
				Model:   "opencode-go/deepseek-v4-flash",
				Effort:  "medium",
			},
		},
	}

	got, _ := ResolveDispatchSelection(cfg, "review this PR")
	if got.Harness != "codex" || got.Model != "gpt-5.2-codex" || got.Effort != "high" || got.Scaffold != "guided" {
		t.Errorf("review selection = %+v", got)
	}

	got, _ = ResolveDispatchSelection(cfg, "implement feature")
	if got.Harness != "pi" || got.Model != "opencode-go/deepseek-v4-flash" || got.Effort != "medium" || got.Scaffold != "" {
		t.Errorf("catchall selection = %+v", got)
	}

	got, _ = ResolveDispatchSelection(cfg, "")
	if got.Harness != "pi" || got.Model != "default-model" || got.Effort != "low" || got.Scaffold != "" {
		t.Errorf("empty desc selection = %+v", got)
	}
}

func TestResolveDispatchSelection_ProseMatch(t *testing.T) {
	cfg := &DispatchConfig{
		DefaultHarness: "pi", DefaultModel: "flash", DefaultEffort: "low",
		Profiles: []DispatchProfile{
			{
				Match: []string{"Extremely hard work only: deep architectural redesign"},
				Use: []DispatchCandidate{
					{Harness: "pi", Model: "glm", Effort: "high"},
				},
			},
		},
	}
	got, err := ResolveDispatchSelection(cfg, "please do deep architectural redesign of the module")
	if err != nil || got.Harness != "pi" || got.Model != "glm" || got.Effort != "high" {
		t.Errorf("got %+v, %v", got, err)
	}
}
