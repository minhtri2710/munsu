package harness

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

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

func TestMatchesProfile(t *testing.T) {
	tests := []struct {
		rules []string
		desc  string
		want  bool
	}{
		{[]string{"*"}, "anything", true},
		{[]string{"review"}, "review this code", true},
		{[]string{"review"}, "code review", true},
		{[]string{"review"}, "deploy the feature", false},
		{[]string{"audit", "review"}, "security audit", true},
		{[]string{"audit", "review"}, "deploy", false},
		{[]string{"data pipeline"}, "build data pipeline", true},
		{[]string{"data pipeline"}, "deploy", false},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			taskLower := tt.desc
			taskWords := splitWords(taskLower)
			got := matchesProfile(tt.rules, taskLower, taskWords)
			if got != tt.want {
				t.Errorf("matchesProfile(%v, %q) = %v, want %v", tt.rules, tt.desc, got, tt.want)
			}
		})
	}
}

func TestLoadDispatch_FileNotFound(t *testing.T) {
	_, err := LoadDispatch("/nonexistent/path/dispatch.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestLoadDispatch_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := tmpDir + "/bad-dispatch.json"
	if err := writeFile(path, `{invalid json}`); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDispatch(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLoadDispatch_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	path := tmpDir + "/empty-dispatch.json"
	if err := writeFile(path, `{}`); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDispatch(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultHarness != "" {
		t.Errorf("DefaultHarness = %q, want empty", cfg.DefaultHarness)
	}
	if len(cfg.Profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(cfg.Profiles))
	}
}

func TestLoadDispatch_Full(t *testing.T) {
	cfg, err := LoadDispatch("testdata/dispatch.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultHarness != "pi" {
		t.Errorf("DefaultHarness = %q, want pi", cfg.DefaultHarness)
	}
	expectedProfiles := []DispatchProfile{
		{Name: "code-review", Match: []string{"review", "audit", "check"}, Harness: "codex", MaxConcurrent: 2},
		{Name: "research", Match: []string{"research", "investigate"}, Harness: "claude", MaxConcurrent: 1},
		{Name: "default", Match: []string{"*"}, Harness: "pi"},
	}
	if !reflect.DeepEqual(cfg.Profiles, expectedProfiles) {
		t.Errorf("Profiles = %+v, want %+v", cfg.Profiles, expectedProfiles)
	}
}

func TestLoadDispatch_WithSelect(t *testing.T) {
	tmpDir := t.TempDir()
	path := tmpDir + "/dispatch-select.json"
	jsonContent := `{
		"defaultHarness": "codex",
		"profiles": [
			{"name": "quota", "match": ["*"], "harness": "codex", "select": "quota-balanced"}
		]
	}`
	if err := writeFile(path, jsonContent); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDispatch(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profiles[0].SelectStrategy != "quota-balanced" {
		t.Errorf("SelectStrategy = %q, want quota-balanced", cfg.Profiles[0].SelectStrategy)
	}
}

func TestSelectProfile_UnknownStrategy(t *testing.T) {
	profiles := []DispatchProfile{
		{Name: "first", Match: []string{"*"}, Harness: "codex"},
		{Name: "general", Match: []string{"*"}, Harness: "claude"},
	}
	got := SelectProfile(profiles, "unknown-strategy")
	if got != "codex" {
		t.Errorf("SelectProfile with unknown strategy = %q, want codex", got)
	}
}

func TestSelectProfile_EmptyProfiles(t *testing.T) {
	got := SelectProfile(nil, "quota-balanced")
	if got != "" {
		t.Errorf("SelectProfile(nil) = %q, want empty", got)
	}
}

func TestSelectProfile_QuotaBalancedNoQuotaAxi(t *testing.T) {
	profiles := []DispatchProfile{
		{Name: "first", Match: []string{"*"}, Harness: "codex"},
		{Name: "general", Match: []string{"*"}, Harness: "claude"},
	}
	got := SelectProfile(profiles, "quota-balanced")
	if got != "codex" {
		t.Errorf("SelectProfile(quota-balanced, no quota-axi) = %q, want codex", got)
	}
}

func TestResolveDispatch_WithQuotaBalanced(t *testing.T) {
	cfg := &DispatchConfig{
		DefaultHarness: "pi",
		Profiles: []DispatchProfile{
			{
				Name:           "quota-catchall",
				Match:          []string{"*"},
				Harness:        "codex",
				SelectStrategy: "quota-balanced",
			},
		},
	}

	got := ResolveDispatch(cfg, "any task")
	if got != "codex" {
		t.Errorf("ResolveDispatch with quota-balanced (no quota-axi) = %q, want codex", got)
	}
}

func TestSelectQuotaBalanced_MixedFreshStale(t *testing.T) {
	tests := []struct {
		name        string
		profiles    []DispatchProfile
		quotaJSON   string
		wantHarness string
	}{
		{
			name: "fresh_wins_over_slight_stale",
			profiles: []DispatchProfile{
				{Name: "codex", Harness: "codex"},
				{Name: "claude", Harness: "claude"},
			},
			quotaJSON: `{
				"providers": [
					{"provider": "codex", "state": {"status": "fresh"}, "windows": [
						{"id": "five_hour", "kind": "general", "percentRemaining": 60},
						{"id": "weekly", "kind": "general", "percentRemaining": 70}
					]},
					{"provider": "claude", "state": {"status": "stale"}, "windows": [
						{"id": "five_hour", "kind": "general", "percentRemaining": 75},
						{"id": "seven_day", "kind": "general", "percentRemaining": 80}
					]}
				]
			}`,
			wantHarness: "codex",
		},
		{
			name: "stale_wins_with_big_margin",
			profiles: []DispatchProfile{
				{Name: "codex", Harness: "codex"},
				{Name: "claude", Harness: "claude"},
			},
			quotaJSON: `{
				"providers": [
					{"provider": "codex", "state": {"status": "fresh"}, "windows": [
						{"id": "five_hour", "kind": "general", "percentRemaining": 30},
						{"id": "weekly", "kind": "general", "percentRemaining": 40}
					]},
					{"provider": "claude", "state": {"status": "stale"}, "windows": [
						{"id": "five_hour", "kind": "general", "percentRemaining": 75},
						{"id": "seven_day", "kind": "general", "percentRemaining": 80}
					]}
				]
			}`,
			wantHarness: "claude",
		},
		{
			name: "exact_tie_first_wins",
			profiles: []DispatchProfile{
				{Name: "codex", Harness: "codex"},
				{Name: "claude", Harness: "claude"},
			},
			quotaJSON: `{
				"providers": [
					{"provider": "codex", "state": {"status": "fresh"}, "windows": [
						{"id": "five_hour", "kind": "general", "percentRemaining": 50},
						{"id": "weekly", "kind": "general", "percentRemaining": 50}
					]},
					{"provider": "claude", "state": {"status": "fresh"}, "windows": [
						{"id": "five_hour", "kind": "general", "percentRemaining": 50},
						{"id": "seven_day", "kind": "general", "percentRemaining": 50}
					]}
				]
			}`,
			wantHarness: "codex",
		},
		{
			name: "model_windows_ignored",
			profiles: []DispatchProfile{
				{Name: "codex", Harness: "codex"},
			},
			quotaJSON: `{
				"providers": [
					{"provider": "codex", "state": {"status": "fresh"}, "windows": [
						{"id": "five_hour", "kind": "general", "percentRemaining": 30},
						{"id": "model:codex_bengalfox:gpt-5.2-codex", "kind": "model", "percentRemaining": 100},
						{"id": "model:fable", "kind": "model", "percentRemaining": 100}
					]}
				]
			}`,
			wantHarness: "codex",
		},
		{
			name: "unknown_harness_skipped",
			profiles: []DispatchProfile{
				{Name: "unknown", Harness: "unknown-harness"},
				{Name: "codex", Harness: "codex"},
			},
			quotaJSON: `{
				"providers": [
					{"provider": "codex", "state": {"status": "fresh"}, "windows": [
						{"id": "five_hour", "kind": "general", "percentRemaining": 80}
					]}
				]
			}`,
			wantHarness: "codex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectQuotaBalancedWithFixture(tt.profiles, tt.quotaJSON)
			if got != tt.wantHarness {
				t.Errorf("selectQuotaBalanced = %q, want %q", got, tt.wantHarness)
			}
		})
	}
}

func TestSelectQuotaBalanced_AllUnavailable(t *testing.T) {
	profiles := []DispatchProfile{
		{Name: "codex", Harness: "codex"},
	}
	quotaJSON := `{"providers": []}`
	got := selectQuotaBalancedWithFixture(profiles, quotaJSON)
	if got != "codex" {
		t.Errorf("all unavailable = %q, want codex (first profile)", got)
	}
}

func TestSelectQuotaBalanced_NoUsableWindows(t *testing.T) {
	profiles := []DispatchProfile{
		{Name: "codex", Harness: "codex"},
	}
	quotaJSON := `{
		"providers": [
			{"provider": "codex", "state": {"status": "fresh"}, "windows": [
				{"id": "model:codex_bengalfox:gpt-5.2-codex", "kind": "model", "percentRemaining": 100}
			]}
		]
	}`
	got := selectQuotaBalancedWithFixture(profiles, quotaJSON)
	if got != "codex" {
		t.Errorf("no usable windows = %q, want codex (first profile)", got)
	}
}

// selectQuotaBalancedWithFixture runs the quota-balanced selection with a
// pre-supplied quota-axi JSON fixture instead of calling the real binary.
func selectQuotaBalancedWithFixture(profiles []DispatchProfile, fixtureJSON string) string {
	if len(profiles) == 0 {
		return ""
	}

	var qd quotaData
	if err := json.Unmarshal([]byte(fixtureJSON), &qd); err != nil {
		return profiles[0].Harness
	}

	if len(qd.Providers) == 0 {
		return profiles[0].Harness
	}

	type candidate struct {
		index int
		min   float64
		fresh bool
	}

	var candidates []candidate
	for i, p := range profiles {
		providerName, ok := quotaProvider[p.Harness]
		if !ok {
			continue
		}

		var provider *quotaProviderData
		for j := range qd.Providers {
			if qd.Providers[j].Provider == providerName {
				provider = &qd.Providers[j]
				break
			}
		}
		if provider == nil {
			continue
		}

		genIDs := generalWindowIDs[p.Harness]
		if len(genIDs) == 0 {
			continue
		}

		var remaining []float64
		for _, w := range provider.Windows {
			if w.Kind == "model" {
				continue
			}
			for _, gid := range genIDs {
				if w.ID == gid {
					remaining = append(remaining, w.PercentRemaining)
					break
				}
			}
		}

		if len(remaining) == 0 {
			continue
		}

		min := remaining[0]
		for _, r := range remaining[1:] {
			if r < min {
				min = r
			}
		}

		candidates = append(candidates, candidate{
			index: i,
			min:   min,
			fresh: provider.State.Status == "fresh",
		})
	}

	if len(candidates) == 0 {
		return profiles[0].Harness
	}

	var freshCands, staleCands []candidate
	for _, c := range candidates {
		if c.fresh {
			freshCands = append(freshCands, c)
		} else {
			staleCands = append(staleCands, c)
		}
	}

	sort.Slice(freshCands, func(i, j int) bool {
		if freshCands[i].min != freshCands[j].min {
			return freshCands[i].min > freshCands[j].min
		}
		return freshCands[i].index < freshCands[j].index
	})

	sort.Slice(staleCands, func(i, j int) bool {
		if staleCands[i].min != staleCands[j].min {
			return staleCands[i].min > staleCands[j].min
		}
		return staleCands[i].index < staleCands[j].index
	})

	var best candidate
	if len(freshCands) > 0 && len(staleCands) > 0 {
		margin := 20.0
		if staleCands[0].min >= freshCands[0].min+margin {
			best = staleCands[0]
		} else {
			best = freshCands[0]
		}
	} else if len(freshCands) > 0 {
		best = freshCands[0]
	} else if len(staleCands) > 0 {
		best = staleCands[0]
	} else {
		return profiles[0].Harness
	}

	return profiles[best.index].Harness
}

func splitWords(s string) []string {
	if s == "" {
		return nil
	}
	w := make([]string, 0)
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if i > start {
				w = append(w, s[start:i])
			}
			start = i + 1
		}
	}
	return w
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

func TestSplitWords(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"hello", []string{"hello"}},
		{"hello world", []string{"hello", "world"}},
		{"  spaced  ", []string{"spaced"}},
	}
	for _, tt := range tests {
		got := splitWords(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitWords(%q) length = %d, want %d", tt.input, len(got), len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitWords(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestLoadDispatch_LegacyShape(t *testing.T) {
	tmpDir := t.TempDir()
	path := tmpDir + "/crew-dispatch.json"
	jsonContent := `{
  "rules": [
    {
      "when": "Trivial mechanical edit such as a rote rename",
      "use": [
        { "harness": "pi", "model": "opencode-go/deepseek-v4-flash", "effort": "low" }
      ],
      "why": "cheap"
    },
    {
      "when": "Ordinary mid-size work",
      "use": [
        { "harness": "pi", "model": "cline-pass/deepseek-v4-flash", "effort": "medium" }
      ]
    }
  ],
  "default": { "harness": "pi", "model": "opencode-go/deepseek-v4-flash", "effort": "low" }
}`
	if err := writeFile(path, jsonContent); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDispatch(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultHarness != "pi" {
		t.Errorf("DefaultHarness = %q, want pi", cfg.DefaultHarness)
	}
	if cfg.DefaultModel != "opencode-go/deepseek-v4-flash" {
		t.Errorf("DefaultModel = %q", cfg.DefaultModel)
	}
	if cfg.DefaultEffort != "low" {
		t.Errorf("DefaultEffort = %q", cfg.DefaultEffort)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("Profiles len = %d, want 2", len(cfg.Profiles))
	}
	if cfg.Profiles[0].Harness != "pi" {
		t.Errorf("Profiles[0].Harness = %q, want pi", cfg.Profiles[0].Harness)
	}
	if cfg.Profiles[0].Model != "opencode-go/deepseek-v4-flash" {
		t.Errorf("Profiles[0].Model = %q", cfg.Profiles[0].Model)
	}
	if cfg.Profiles[0].Effort != "low" {
		t.Errorf("Profiles[0].Effort = %q", cfg.Profiles[0].Effort)
	}
}

func TestResolveDispatchSelection_ModelEffort(t *testing.T) {
	cfg := &DispatchConfig{
		DefaultHarness: "pi",
		DefaultModel:   "default-model",
		DefaultEffort:  "low",
		Profiles: []DispatchProfile{
			{
				Name:    "review",
				Match:   []string{"review"},
				Harness: "codex",
				Model:   "gpt-5.2-codex",
				Effort:  "high",
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
	cfg.normalize()

	got := ResolveDispatchSelection(cfg, "review this PR")
	if got.Harness != "codex" || got.Model != "gpt-5.2-codex" || got.Effort != "high" {
		t.Errorf("review selection = %+v", got)
	}

	got = ResolveDispatchSelection(cfg, "implement feature")
	if got.Harness != "pi" || got.Model != "opencode-go/deepseek-v4-flash" || got.Effort != "medium" {
		t.Errorf("catchall selection = %+v", got)
	}

	got = ResolveDispatchSelection(cfg, "")
	if got.Harness != "pi" || got.Model != "default-model" || got.Effort != "low" {
		t.Errorf("empty desc selection = %+v", got)
	}
}

func TestResolveDispatchSelection_LegacyWhenProse(t *testing.T) {
	cfg := &DispatchConfig{
		Default: &DispatchCandidate{Harness: "pi", Model: "flash", Effort: "low"},
		Rules: []DispatchProfile{
			{
				When: "Extremely hard work only: deep architectural redesign",
				Use: []DispatchCandidate{
					{Harness: "pi", Model: "glm", Effort: "high"},
				},
			},
		},
	}
	cfg.normalize()
	got := ResolveDispatchSelection(cfg, "please do deep architectural redesign of the module")
	if got.Harness != "pi" || got.Model != "glm" || got.Effort != "high" {
		t.Errorf("got %+v", got)
	}
}

func TestLaunchStringWith_Overrides(t *testing.T) {
	tmpl := Templates[Pi]
	cmd := LaunchStringWith(Pi, tmpl, "opencode-go/deepseek-v4-flash", "medium")
	if !strings.Contains(cmd, "--model") || !strings.Contains(cmd, "opencode-go/deepseek-v4-flash") {
		t.Errorf("missing model in %q", cmd)
	}
	if !strings.Contains(cmd, "--thinking") || !strings.Contains(cmd, "medium") {
		t.Errorf("missing effort in %q", cmd)
	}
	// empty keeps no flags
	cmd = LaunchStringWith(Pi, tmpl, "", "")
	if strings.Contains(cmd, "--model") || strings.Contains(cmd, "--thinking") {
		t.Errorf("empty overrides should omit flags: %q", cmd)
	}
	// default sentinel omitted
	cmd = LaunchStringWith(Pi, tmpl, "default", "default")
	if strings.Contains(cmd, "--model") || strings.Contains(cmd, "--thinking") {
		t.Errorf("default sentinel should omit flags: %q", cmd)
	}
}
