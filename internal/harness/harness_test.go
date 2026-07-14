package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFromEnv_Claude(t *testing.T) {
	os.Setenv("CLAUDE_CODE", "1")
	defer os.Unsetenv("CLAUDE_CODE")

	h := detectFromEnv()
	if h != Claude {
		t.Errorf("detectFromEnv() = %q, want %q", h, Claude)
	}
}

func TestDetectFromEnv_Codex(t *testing.T) {
	os.Setenv("CODECLIMB", "1")
	defer os.Unsetenv("CODECLIMB")

	h := detectFromEnv()
	if h != Codex {
		t.Errorf("detectFromEnv() = %q, want %q", h, Codex)
	}
}

func TestDetectFromEnv_Opi(t *testing.T) {
	os.Setenv("OPENCODE", "1")
	defer os.Unsetenv("OPENCODE")

	h := detectFromEnv()
	if h != Opencode {
		t.Errorf("detectFromEnv() = %q, want %q", h, Opencode)
	}
}

func TestDetectFromEnv_Pi(t *testing.T) {
	os.Setenv("PI_CODING_AGENT_DIR", "/some/path")
	defer os.Unsetenv("PI_CODING_AGENT_DIR")

	h := detectFromEnv()
	if h != Pi {
		t.Errorf("detectFromEnv() = %q, want %q", h, Pi)
	}
}

func TestDetectFromEnv_Grok(t *testing.T) {
	os.Setenv("GROK_VM_ID", "vm-123")
	defer os.Unsetenv("GROK_VM_ID")

	h := detectFromEnv()
	if h != Grok {
		t.Errorf("detectFromEnv() = %q, want %q", h, Grok)
	}
}

func TestDetectFromEnv_Empty(t *testing.T) {
	// Unset all env markers
	for _, env := range []string{"CLAUDE_CODE", "CODECLIMB", "OPENCODE", "PI_CODING_AGENT_DIR", "GROK_VM_ID"} {
		t.Setenv(env, "")
		os.Unsetenv(env)
	}

	h := detectFromEnv()
	if h != "" {
		t.Errorf("detectFromEnv() = %q, want empty", h)
	}
}

func TestMatchProcessName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"claude", Claude},
		{"Claude", Claude},
		{"claude-code", Claude},
		{"Claude Code", Claude},
		{"codex", Codex},
		{"Codex", Codex},
		{"codeclimb", Codex},
		{"opencode", Opencode},
		{"pi", Pi},
		{"pi-coding-agent", Pi},
		{"grok", Grok},
		{"bash", ""},
		{"zsh", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchProcessName(tt.name); got != tt.want {
				t.Errorf("matchProcessName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestIsKnownHarness(t *testing.T) {
	for _, h := range KnownHarnesses {
		if !IsKnownHarness(h) {
			t.Errorf("IsKnownHarness(%q) = false, want true", h)
		}
	}
	if IsKnownHarness("unknown") {
		t.Error("IsKnownHarness('unknown') = true, want false")
	}
}

func TestTemplates(t *testing.T) {
	if tmpl, ok := Templates[Claude]; !ok {
		t.Errorf("missing template for %s", Claude)
	} else if tmpl.ModelFlag != "--model" {
		t.Errorf("Claude ModelFlag = %q, want --model", tmpl.ModelFlag)
	}

	if tmpl, ok := Templates[Codex]; !ok {
		t.Errorf("missing template for %s", Codex)
	} else if tmpl.DefaultModel == "" {
		t.Errorf("Codex DefaultModel is empty")
	}

	if tmpl, ok := Templates[Pi]; !ok {
		t.Errorf("missing template for %s", Pi)
	} else if tmpl.ModelFlag != "--model" {
		t.Errorf("Pi ModelFlag = %q, want --model", tmpl.ModelFlag)
	}

	if tmpl, ok := Templates[Grok]; !ok {
		t.Errorf("missing template for %s", Grok)
	} else if tmpl.ModelFlag != "" {
		t.Errorf("Grok ModelFlag = %q, want empty", tmpl.ModelFlag)
	}
}

func TestCrew_HasDispatchDefault(t *testing.T) {
	tmp := t.TempDir()

	// Write a dispatch config with a default harness
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	dispatchFile := filepath.Join(configDir, "crew-dispatch.json")
	if err := os.WriteFile(dispatchFile, []byte(`{"defaultHarness":"codex"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h, err := Crew(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != "codex" {
		t.Errorf("Crew() = %q, want %q", h, "codex")
	}
}

func TestCrew_HasCrewHarnessFile(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	// Write crew-harness file (no dispatch config)
	harnessFile := filepath.Join(configDir, "crew-harness")
	if err := os.WriteFile(harnessFile, []byte("opencode\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h, err := Crew(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != "opencode" {
		t.Errorf("Crew() = %q, want %q", h, "opencode")
	}
}

func TestCrew_NoConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CODE", "1")

	// No config files at all — falls back to detected harness
	h, err := Crew(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h == "" {
		t.Error("Crew() returned empty, expected a harness from detect")
	}
}

func TestSecondmate_HasSecondmateHarness(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	harnessFile := filepath.Join(configDir, "secondmate-harness")
	if err := os.WriteFile(harnessFile, []byte("grok\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h, err := Secondmate(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != "grok" {
		t.Errorf("Secondmate() = %q, want %q", h, "grok")
	}
}

func TestSecondmate_FallsBackToCrewHarness(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	// Only crew-harness, no secondmate-harness
	harnessFile := filepath.Join(configDir, "crew-harness")
	if err := os.WriteFile(harnessFile, []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h, err := Secondmate(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != "pi" {
		t.Errorf("Secondmate() = %q, want %q", h, "pi")
	}
}

func TestSecondmate_NoConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CODE", "1")

	h, err := Secondmate(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h == "" {
		t.Error("Secondmate() returned empty, expected a harness from detect")
	}
}

func TestDispatchDefaultHarness(t *testing.T) {
	cfg, err := LoadDispatch("testdata/dispatch.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultHarness != "pi" {
		t.Errorf("DefaultHarness = %q, want %q", cfg.DefaultHarness, "pi")
	}
	if len(cfg.Profiles) == 0 {
		t.Fatal("expected at least one profile")
	}
}

func TestCrew_CrewHarnessDefaultIgnored(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	if err := os.WriteFile(filepath.Join(configDir, "crew-harness"), []byte("default\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUNSU_CREW-HARNESS_OVERRIDE", "")
	for _, env := range []string{"CODECLIMB", "OPENCODE", "PI_CODING_AGENT_DIR", "GROK_VM_ID"} {
		t.Setenv(env, "")
	}
	t.Setenv("CLAUDE_CODE", "1")

	h, err := Crew(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != Claude {
		t.Errorf("Crew() = %q, want %q (default sentinel should fall through to Detect)", h, Claude)
	}
}

func TestSecondmate_DefaultSentinelsIgnored(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	if err := os.WriteFile(filepath.Join(configDir, "secondmate-harness"), []byte("default\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "crew-harness"), []byte("default\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUNSU_SECONDMATE-HARNESS_OVERRIDE", "")
	t.Setenv("MUNSU_CREW-HARNESS_OVERRIDE", "")
	for _, env := range []string{"CODECLIMB", "OPENCODE", "PI_CODING_AGENT_DIR", "GROK_VM_ID"} {
		t.Setenv(env, "")
	}
	t.Setenv("CLAUDE_CODE", "1")

	h, err := Secondmate(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != Claude {
		t.Errorf("Secondmate() = %q, want %q (default sentinels should fall through to Detect)", h, Claude)
	}
}

func TestSecondmate_DefaultSecondmateHarnessFallsToCrewHarness(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	if err := os.WriteFile(filepath.Join(configDir, "secondmate-harness"), []byte("default\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "crew-harness"), []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUNSU_SECONDMATE-HARNESS_OVERRIDE", "")
	t.Setenv("MUNSU_CREW-HARNESS_OVERRIDE", "")

	h, err := Secondmate(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != Pi {
		t.Errorf("Secondmate() = %q, want %q (default secondmate sentinel should fall through to crew-harness)", h, Pi)
	}
}
