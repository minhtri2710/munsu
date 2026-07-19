package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// clearEnvMarkers unsets all known harness env markers to avoid
// system-level env var interference in detection tests.
func clearEnvMarkers(t *testing.T) {
	t.Helper()
	for _, env := range []string{
		"CLAUDE_CODE",
		"CODECLIMB",
		"OPENCODE",
		"PI_CODING_AGENT_DIR",
		"PI_CODING_AGENT",
		"GROK_VM_ID",
		"GROK_AGENT",
		"ANTIGRAVITY_LS_ADDRESS",
		"ANTIGRAVITY_AGENT",
	} {
		t.Setenv(env, "")
		os.Unsetenv(env)
	}
}

func TestDetectFromEnv_Claude(t *testing.T) {
	// Unset all markers first to avoid interference from system-level vars
	clearEnvMarkers(t)
	os.Setenv("CLAUDE_CODE", "1")
	defer os.Unsetenv("CLAUDE_CODE")

	h := detectFromEnv()
	if h != Claude {
		t.Errorf("detectFromEnv() = %q, want %q", h, Claude)
	}
}

func TestDetectFromEnv_Codex(t *testing.T) {
	clearEnvMarkers(t)
	os.Setenv("CODECLIMB", "1")
	defer os.Unsetenv("CODECLIMB")

	h := detectFromEnv()
	if h != Codex {
		t.Errorf("detectFromEnv() = %q, want %q", h, Codex)
	}
}

func TestDetectFromEnv_Opi(t *testing.T) {
	clearEnvMarkers(t)
	os.Setenv("OPENCODE", "1")
	defer os.Unsetenv("OPENCODE")

	h := detectFromEnv()
	if h != Opencode {
		t.Errorf("detectFromEnv() = %q, want %q", h, Opencode)
	}
}

func TestDetectFromEnv_Pi(t *testing.T) {
	clearEnvMarkers(t)
	os.Setenv("PI_CODING_AGENT_DIR", "/some/path")
	defer os.Unsetenv("PI_CODING_AGENT_DIR")

	h := detectFromEnv()
	if h != Pi {
		t.Errorf("detectFromEnv() = %q, want %q", h, Pi)
	}
}

func TestDetectFromEnv_Grok(t *testing.T) {
	clearEnvMarkers(t)
	os.Setenv("GROK_VM_ID", "vm-123")
	defer os.Unsetenv("GROK_VM_ID")

	h := detectFromEnv()
	if h != Grok {
		t.Errorf("detectFromEnv() = %q, want %q", h, Grok)
	}
}

func TestDetectFromEnv_Agy(t *testing.T) {
	clearEnvMarkers(t)
	os.Setenv("ANTIGRAVITY_AGENT", "1")
	defer os.Unsetenv("ANTIGRAVITY_AGENT")

	h := detectFromEnv()
	if h != Agy {
		t.Errorf("detectFromEnv() = %q, want %q", h, Agy)
	}
}

func TestDetectFromEnv_Empty(t *testing.T) {
	// Unset all env markers including system-level vars
	clearEnvMarkers(t)

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
		{"agy", Agy},
		{"antigravity", Agy},
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
	if IsKnownHarness("unknown") {
		t.Error("IsKnownHarness('unknown') = true, want false")
	}
}

func TestValidateHarness(t *testing.T) {
	if err := ValidateHarness(""); err != nil {
		t.Errorf("ValidateHarness('') = %v, want nil", err)
	}
	if err := ValidateHarness("default"); err != nil {
		t.Errorf("ValidateHarness('default') = %v, want nil", err)
	}
	for _, h := range KnownHarnesses {
		if err := ValidateHarness(h); err != nil {
			t.Errorf("ValidateHarness(%q) = %v, want nil", h, err)
		}
	}
	if err := ValidateHarness("not-a-harness"); err == nil {
		t.Error("ValidateHarness('not-a-harness') = nil, want error")
	}
	if err := ValidateHarness("invalid!"); err == nil {
		t.Error("ValidateHarness('invalid!') = nil, want error")
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
	} else if tmpl.ModelFlag != "--model" {
		t.Errorf("Grok ModelFlag = %q, want --model", tmpl.ModelFlag)
	}

	if tmpl, ok := Templates[Agy]; !ok {
		t.Errorf("missing template for %s", Agy)
	} else if tmpl.ModelFlag != "--model" {
		t.Errorf("Agy ModelFlag = %q, want --model", tmpl.ModelFlag)
	} else if len(tmpl.ExtraArgs) == 0 {
	} else if len(tmpl.ExtraArgs) == 0 {
		t.Errorf("Agy ExtraArgs should not be empty")
	} else if tmpl.ExtraArgs[0] != "--dangerously-skip-permissions" {
		t.Errorf("Agy ExtraArgs[0] = %q, want --dangerously-skip-permissions", tmpl.ExtraArgs[0])
	}
}

func TestSoldier_HasDispatchDefault(t *testing.T) {
	tmp := t.TempDir()

	// Write a dispatch config with a default harness
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	dispatchFile := filepath.Join(configDir, "soldier-dispatch.json")
	if err := os.WriteFile(dispatchFile, []byte(`{"defaultHarness":"codex"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h, err := Soldier(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != "codex" {
		t.Errorf("Soldier() = %q, want %q", h, "codex")
	}
}

func TestSoldier_HasSoldierHarnessFile(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	// Write soldier-harness file (no dispatch config)
	harnessFile := filepath.Join(configDir, "soldier-harness")
	if err := os.WriteFile(harnessFile, []byte("opencode\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h, err := Soldier(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != "opencode" {
		t.Errorf("Soldier() = %q, want %q", h, "opencode")
	}
}

func TestSoldier_NoConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CODE", "1")

	// No config files at all — falls back to detected harness
	h, err := Soldier(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h == "" {
		t.Error("Soldier() returned empty, expected a harness from detect")
	}
}

func TestCaptain_HasCaptainHarness(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	harnessFile := filepath.Join(configDir, "captain-harness")
	if err := os.WriteFile(harnessFile, []byte("grok\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h, err := Captain(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != "grok" {
		t.Errorf("Captain() = %q, want %q", h, "grok")
	}
}

func TestCaptain_FallsBackToSoldierHarness(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	// Only soldier-harness, no captain-harness
	harnessFile := filepath.Join(configDir, "soldier-harness")
	if err := os.WriteFile(harnessFile, []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h, err := Captain(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != "pi" {
		t.Errorf("Captain() = %q, want %q", h, "pi")
	}
}

func TestCaptain_NoConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CODE", "1")

	h, err := Captain(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h == "" {
		t.Error("Captain() returned empty, expected a harness from detect")
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

func TestSoldier_SoldierHarnessDefaultIgnored(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	if err := os.WriteFile(filepath.Join(configDir, "soldier-harness"), []byte("default\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUNSU_CREW-HARNESS_OVERRIDE", "")
	for _, env := range []string{"CODECLIMB", "OPENCODE", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT", "GROK_VM_ID", "GROK_AGENT"} {
		t.Setenv(env, "")
	}
	t.Setenv("CLAUDE_CODE", "1")

	h, err := Soldier(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != Claude {
		t.Errorf("Soldier() = %q, want %q (default sentinel should fall through to Detect)", h, Claude)
	}
}

func TestCaptain_DefaultSentinelsIgnored(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	if err := os.WriteFile(filepath.Join(configDir, "captain-harness"), []byte("default\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "soldier-harness"), []byte("default\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUNSU_CAPTAIN-HARNESS_OVERRIDE", "")
	t.Setenv("MUNSU_CREW-HARNESS_OVERRIDE", "")
	for _, env := range []string{"CODECLIMB", "OPENCODE", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT", "GROK_VM_ID", "GROK_AGENT"} {
		t.Setenv(env, "")
	}
	t.Setenv("CLAUDE_CODE", "1")

	h, err := Captain(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != Claude {
		t.Errorf("Captain() = %q, want %q (default sentinels should fall through to Detect)", h, Claude)
	}
}

func TestCaptain_DefaultCaptainHarnessFallsToSoldierHarness(t *testing.T) {
	tmp := t.TempDir()

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)

	if err := os.WriteFile(filepath.Join(configDir, "captain-harness"), []byte("default\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "soldier-harness"), []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("MUNSU_CAPTAIN-HARNESS_OVERRIDE")
	os.Unsetenv("MUNSU_CREW-HARNESS_OVERRIDE")

	h, err := Captain(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != Pi {
		t.Errorf("Captain() = %q, want %q (default captain sentinel should fall through to soldier-harness)", h, Pi)
	}
}

func TestParseHarnessLine(t *testing.T) {
	tests := []struct {
		in      string
		harness string
		model   string
		effort  string
	}{
		{"pi", "pi", "", ""},
		{"pi cliproxyapi/grok-4.5 low", "pi", "cliproxyapi/grok-4.5", "low"},
		{"  codex gpt-5.2-codex high  ", "codex", "gpt-5.2-codex", "high"},
		{"default", "", "", ""},
		{"# comment", "", "", ""},
		{"", "", "", ""},
	}
	for _, tt := range tests {
		got := ParseHarnessLine(tt.in)
		if got.Harness != tt.harness || got.Model != tt.model || got.Effort != tt.effort {
			t.Errorf("ParseHarnessLine(%q) = %+v, want harness=%q model=%q effort=%q",
				tt.in, got, tt.harness, tt.model, tt.effort)
		}
	}
}

func TestCaptainProfileFromHome_MultiToken(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	if err := os.WriteFile(filepath.Join(configDir, "captain-harness"),
		[]byte("pi cliproxyapi/grok-4.5 low\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// legacy model file should NOT override multi-token model
	if err := os.WriteFile(filepath.Join(configDir, "model"), []byte("ignored-model\n"), 0644); err != nil {
		t.Fatal(err)
	}

	prof, err := CaptainProfileFromHome(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Harness != "pi" || prof.Model != "cliproxyapi/grok-4.5" || prof.Effort != "low" {
		t.Errorf("profile = %+v", prof)
	}
	h, err := Captain(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != "pi" {
		t.Errorf("Captain() = %q, want pi", h)
	}
}

func TestCaptainProfileFromHome_SoldierFallback(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	// no captain-harness; soldier-harness bare name only
	if err := os.WriteFile(filepath.Join(configDir, "soldier-harness"),
		[]byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "model"),
		[]byte("opencode-go/deepseek-v4-flash\n"), 0644); err != nil {
		t.Fatal(err)
	}
	prof, err := CaptainProfileFromHome(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Harness != "pi" || prof.Model != "opencode-go/deepseek-v4-flash" {
		t.Errorf("profile = %+v", prof)
	}
}


func TestCaptainProfileFromHome_ModelFileFallback(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	if err := os.WriteFile(filepath.Join(configDir, "captain-harness"), []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "model"), []byte("opencode-go/deepseek-v4-flash\n"), 0644); err != nil {
		t.Fatal(err)
	}
	prof, err := CaptainProfileFromHome(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Harness != "pi" || prof.Model != "opencode-go/deepseek-v4-flash" || prof.Effort != "" {
		t.Errorf("profile = %+v", prof)
	}
}
