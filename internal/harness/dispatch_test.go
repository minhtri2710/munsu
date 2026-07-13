package harness

import (
	"os"
	"reflect"
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
