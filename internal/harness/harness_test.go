package harness

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
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

func TestSoldierFailsClosedOnMalformedPublishedSnapshot(t *testing.T) {
	home := t.TempDir()
	if err := config.StoreFleetBase(home, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion, Config: config.ProjectOverlay{SoldierHarness: "pi"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, config.PublishedSnapshotPath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Soldier(home); err == nil {
		t.Fatal("expected malformed published snapshot error")
	}
}

func TestSoldierFailsClosedOnMalformedFleetBase(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, config.BaseDocumentPath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Soldier(home); err == nil {
		t.Fatal("expected malformed fleet base error")
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

	// Write a fleet base document with a soldier harness
	os.MkdirAll(filepath.Join(tmp, "config"), 0755)
	base := config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.ProjectOverlay{
			SoldierHarness: "codex",
		},
	}
	if err := config.StoreFleetBase(tmp, base); err != nil {
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

func TestSoldier_HasSoldierHarnessInBase(t *testing.T) {
	tmp := t.TempDir()

	writeBase(t, tmp, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "opencode"},
	})
	// A stale legacy flat pin is ignored when the typed base document exists.
	if err := os.MkdirAll(filepath.Join(tmp, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "config", "soldier-harness"), []byte("pi\n"), 0o644); err != nil {
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

// writeBase persists a fleet base document (config/base.json) for a test home.
func writeBase(t *testing.T, home string, doc config.FleetBaseDocument) {
	t.Helper()
	if err := config.StoreFleetBase(home, doc); err != nil {
		t.Fatal(err)
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

	writeBase(t, tmp, config.FleetBaseDocument{
		SchemaVersion:  config.FleetBaseSchemaVersion,
		CaptainProfile: config.CaptainProfile{Harness: "grok"},
	})

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

	// Only soldier-harness, no captain profile.
	writeBase(t, tmp, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "pi"},
	})

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
	cfg := &DispatchConfig{
		DefaultHarness: "pi",
		Profiles: []DispatchProfile{
			{Name: "code-review", Match: []string{"review"}, Harness: "codex"},
		},
	}
	if cfg.DefaultHarness != "pi" {
		t.Errorf("DefaultHarness = %q, want %q", cfg.DefaultHarness, "pi")
	}
	if len(cfg.Profiles) == 0 {
		t.Fatal("expected at least one profile")
	}
}

func TestCaptain_UnsetProfileFallsToDetect(t *testing.T) {
	tmp := t.TempDir()

	// Empty captain profile and empty soldier harness — both unset, so the
	// resolver falls through to Detect.
	writeBase(t, tmp, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion})

	for _, env := range []string{"CODECLIMB", "OPENCODE", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT", "GROK_VM_ID", "GROK_AGENT", "AGY_CONVERSATION_ID", "ANTIGRAVITY_AGENT", "ANTIGRAVITY_CLI", "ANTIGRAVITY_LS_ADDRESS"} {
		t.Setenv(env, "")
	}
	t.Setenv("CLAUDE_CODE", "1")

	h, err := Captain(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != Claude {
		t.Errorf("Captain() = %q, want %q (unset profile should fall through to Detect)", h, Claude)
	}
}

func TestCaptain_EmptyCaptainHarnessFallsToSoldierHarness(t *testing.T) {
	tmp := t.TempDir()

	// No captain-harness token; soldier-harness supplies the bare fallback name.
	writeBase(t, tmp, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "pi"},
	})

	h, err := Captain(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != Pi {
		t.Errorf("Captain() = %q, want %q (empty captain profile should fall through to soldier-harness)", h, Pi)
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
	// Captain profile carries its own model; base Config.Model must NOT override it.
	writeBase(t, tmp, config.FleetBaseDocument{
		SchemaVersion:  config.FleetBaseSchemaVersion,
		Config:         config.ProjectOverlay{Model: "ignored-model"},
		CaptainProfile: config.CaptainProfile{Harness: "pi", Model: "cliproxyapi/grok-4.5", Effort: "low"},
	})
	// Conflicting legacy pins are disposable data and must not shadow the base.
	if err := os.MkdirAll(filepath.Join(tmp, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"captain-harness": "grok ignored-model high\n",
		"model":           "legacy-model\n",
	} {
		if err := os.WriteFile(filepath.Join(tmp, "config", key), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
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
	// No captain profile harness; soldier-harness supplies the bare name and
	// base Config.Model supplies the model.
	writeBase(t, tmp, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{SoldierHarness: "pi", Model: "opencode-go/deepseek-v4-flash"},
	})
	prof, err := CaptainProfileFromHome(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Harness != "pi" || prof.Model != "opencode-go/deepseek-v4-flash" {
		t.Errorf("profile = %+v", prof)
	}
}

func TestCaptainProfileFromHome_SparseProfilePreservesModelEffort(t *testing.T) {
	tmp := t.TempDir()
	// A sparse captain profile carries Model and Effort but no Harness; the
	// soldier-harness fallback supplies the bare harness name without
	// discarding the independently stored Model/Effort.
	writeBase(t, tmp, config.FleetBaseDocument{
		SchemaVersion:  config.FleetBaseSchemaVersion,
		Config:         config.ProjectOverlay{SoldierHarness: "pi"},
		CaptainProfile: config.CaptainProfile{Model: "opencode-go/deepseek-v4-flash", Effort: "high"},
	})
	prof, err := CaptainProfileFromHome(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Harness != "pi" || prof.Model != "opencode-go/deepseek-v4-flash" || prof.Effort != "high" {
		t.Errorf("profile = %+v, want harness pi, model preserved, effort high", prof)
	}
}

func TestCaptainProfileFromHome_BaseModelFallback(t *testing.T) {
	tmp := t.TempDir()
	// Captain profile has a harness but no model; base Config.Model fills it in.
	writeBase(t, tmp, config.FleetBaseDocument{
		SchemaVersion:  config.FleetBaseSchemaVersion,
		Config:         config.ProjectOverlay{Model: "opencode-go/deepseek-v4-flash"},
		CaptainProfile: config.CaptainProfile{Harness: "pi"},
	})
	prof, err := CaptainProfileFromHome(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Harness != "pi" || prof.Model != "opencode-go/deepseek-v4-flash" || prof.Effort != "" {
		t.Errorf("profile = %+v", prof)
	}
}

func TestCaptainProfileFromHome_MalformedBaseFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	// A base.json that exists but does not parse must surface an error, never
	// degrade to an empty profile (that path is reserved for a missing file).
	basePath := filepath.Join(tmp, config.BaseDocumentPath)
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptainProfileFromHome(tmp); err == nil {
		t.Fatal("malformed base.json must fail closed, got nil error")
	}
}

func TestCaptainProfileFromHome_UnknownHarnessRejected(t *testing.T) {
	tmp := t.TempDir()
	// A stored captain harness that no adapter recognizes must be rejected by
	// ValidateHarness rather than returned as a launchable profile.
	writeBase(t, tmp, config.FleetBaseDocument{
		SchemaVersion:  config.FleetBaseSchemaVersion,
		CaptainProfile: config.CaptainProfile{Harness: "bogus-harness"},
	})
	if _, err := CaptainProfileFromHome(tmp); err == nil {
		t.Fatal("unknown captain harness must be rejected by ValidateHarness")
	}
}

func TestResolveSoldierFromSnapshot(t *testing.T) {
	t.Run("non-empty identity resolves", func(t *testing.T) {
		cfg := config.ResolvedProjectConfig{Project: "acme", SoldierHarness: "codex"}
		got, err := ResolveSoldierFromSnapshot(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if got != "codex" {
			t.Errorf("ResolveSoldierFromSnapshot = %q, want codex", got)
		}
	})
	t.Run("empty identity fails closed", func(t *testing.T) {
		cfg := config.ResolvedProjectConfig{Project: "acme"}
		_, err := ResolveSoldierFromSnapshot(cfg)
		if !errors.Is(err, ErrNoSoldierHarnessInSnapshot) {
			t.Fatalf("empty identity error = %v, want %v", err, ErrNoSoldierHarnessInSnapshot)
		}
	})
	t.Run("unknown harness rejected via ValidateHarness", func(t *testing.T) {
		cfg := config.ResolvedProjectConfig{Project: "acme", SoldierHarness: "copilot"}
		if _, err := ResolveSoldierFromSnapshot(cfg); err == nil {
			t.Fatal("unknown harness must be rejected by ValidateHarness")
		}
	})
}

func TestResolveCaptainFromSnapshot(t *testing.T) {
	t.Run("non-empty identity resolves", func(t *testing.T) {
		cfg := config.ResolvedProjectConfig{Project: "acme", CaptainProfile: config.CaptainProfile{Harness: "pi", Model: "sonnet"}}
		got, err := ResolveCaptainFromSnapshot(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if got != "pi" {
			t.Errorf("ResolveCaptainFromSnapshot = %q, want pi", got)
		}
	})
	t.Run("empty identity fails closed", func(t *testing.T) {
		cfg := config.ResolvedProjectConfig{Project: "acme"}
		_, err := ResolveCaptainFromSnapshot(cfg)
		if !errors.Is(err, ErrNoCaptainHarnessInSnapshot) {
			t.Fatalf("empty identity error = %v, want %v", err, ErrNoCaptainHarnessInSnapshot)
		}
	})
	t.Run("unknown harness rejected via ValidateHarness", func(t *testing.T) {
		cfg := config.ResolvedProjectConfig{Project: "acme", CaptainProfile: config.CaptainProfile{Harness: "vim"}}
		if _, err := ResolveCaptainFromSnapshot(cfg); err == nil {
			t.Fatal("unknown harness must be rejected by ValidateHarness")
		}
	})
}

func PreflightBinaryName(harnessName string) (string, bool) {
	binary, ok := preflightBinaryNames[harnessName]
	return binary, ok
}

func TestPreflightBinaryName(t *testing.T) {
	for _, h := range KnownHarnesses {
		binary, ok := PreflightBinaryName(h)
		if !ok || binary == "" {
			t.Errorf("PreflightBinaryName(%q) = (%q, %v), want known binary", h, binary, ok)
		}
	}
	if _, ok := PreflightBinaryName("unknown"); ok {
		t.Error("PreflightBinaryName('unknown') should report not found")
	}
}
