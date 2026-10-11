package harness

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

// clearEnvMarkers unsets all known harness env markers to avoid
// system-level env var interference in detection tests.
func clearEnvMarkers(t *testing.T) {
	t.Helper()
	for _, env := range []string{
		"CLAUDECODE",
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
	if err := config.StoreFleetBase(home, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion, Config: config.FleetBaseConfig{SoldierHarness: "pi"}}); err != nil {
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
	clearEnvMarkers(t)
	t.Setenv("CLAUDECODE", "1")

	h := detectFromEnv()
	if h != Claude {
		t.Errorf("detectFromEnv() = %q, want %q", h, Claude)
	}
}

func TestDetectFromEnv_ClaudeUsesClaudeCodeMarker(t *testing.T) {
	clearEnvMarkers(t)
	t.Setenv("CLAUDECODE", "1")
	if got := detectFromEnv(); got != Claude {
		t.Fatalf("detectFromEnv() with CLAUDECODE set = %q, want %q", got, Claude)
	}

	clearEnvMarkers(t)
	t.Setenv("CLAUDE_CODE", "1")
	if got := detectFromEnv(); got != "" {
		t.Fatalf("detectFromEnv() with obsolete CLAUDE_CODE set = %q, want empty", got)
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

func TestMatchProcessNameDeterministicWithOverlappingAdapters(t *testing.T) {
	originalClaude := Adapters[Claude]
	originalAgy := Adapters[Agy]
	claude := originalClaude
	agy := originalAgy
	claude.ProcessMatchers = append(claude.ProcessMatchers, ProcessNameMatcher{Name: "shared-agent"})
	agy.ProcessMatchers = append(agy.ProcessMatchers, ProcessNameMatcher{Name: "shared", Substr: true})
	Adapters[Claude] = claude
	Adapters[Agy] = agy
	t.Cleanup(func() {
		Adapters[Claude] = originalClaude
		Adapters[Agy] = originalAgy
	})

	for i := 0; i < 1000; i++ {
		if got := matchProcessName("shared-agent"); got != Claude {
			t.Fatalf("matchProcessName iteration %d = %q, want %q", i, got, Claude)
		}
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

func TestSoldier_HasDispatchDefault(t *testing.T) {
	tmp := t.TempDir()

	// Write a fleet base document with a soldier harness
	os.MkdirAll(filepath.Join(tmp, "config"), 0755)
	base := config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.FleetBaseConfig{
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
		Config:        config.FleetBaseConfig{SoldierHarness: "opencode"},
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

// TestResolveRefusesWithoutAConfiguredHarness: Soldier and Captain use only the
// configured harness; an environment that Detect would recognise never stands in.
func TestResolveRefusesWithoutAConfiguredHarness(t *testing.T) {
	for _, tc := range []struct {
		name    string
		resolve func(string) (string, error)
		base    config.FleetBaseDocument
		want    string
		wantErr string
	}{
		{
			name:    "soldier with none configured blocks",
			resolve: Soldier,
			wantErr: "munsu config set soldier-harness",
		},
		{
			name:    "soldier with soldier-harness configured allows",
			resolve: Soldier,
			base:    config.FleetBaseDocument{Config: config.FleetBaseConfig{SoldierHarness: Codex}},
			want:    Codex,
		},
		{
			name:    "captain with none configured blocks",
			resolve: Captain,
			wantErr: "munsu config set captain-harness",
		},
		{
			name:    "captain with captain-harness configured allows",
			resolve: Captain,
			base:    config.FleetBaseDocument{CaptainProfile: config.CaptainProfile{Harness: Grok}},
			want:    Grok,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnvMarkers(t)
			t.Setenv("CLAUDECODE", "1")
			home := t.TempDir()
			tc.base.SchemaVersion = config.FleetBaseSchemaVersion
			writeBase(t, home, tc.base)

			got, err := tc.resolve(home)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolve = (%q, %v), want error containing %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("resolve = (%q, %v), want %q", got, err, tc.want)
			}
		})
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
		Config:        config.FleetBaseConfig{SoldierHarness: "pi"},
	})

	h, err := Captain(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if h != "pi" {
		t.Errorf("Captain() = %q, want %q", h, "pi")
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

func TestCaptain_EmptyCaptainHarnessFallsToSoldierHarness(t *testing.T) {
	tmp := t.TempDir()

	// No captain-harness token; soldier-harness supplies the bare fallback name.
	writeBase(t, tmp, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.FleetBaseConfig{SoldierHarness: "pi"},
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
		Config:         config.FleetBaseConfig{Model: "ignored-model"},
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
		Config:        config.FleetBaseConfig{SoldierHarness: "pi", Model: "opencode-go/deepseek-v4-flash"},
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
		Config:         config.FleetBaseConfig{SoldierHarness: "pi"},
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
		Config:         config.FleetBaseConfig{Model: "opencode-go/deepseek-v4-flash"},
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

func TestResolveFromSnapshotRefusals(t *testing.T) {
	ranks := []struct {
		name       string
		resolve    func(config.ResolvedProjectConfig) (string, error)
		cfgFor     func(harness string) config.ResolvedProjectConfig
		sentinel   error
		unknownMsg string
	}{
		{
			name:    "soldier",
			resolve: ResolveSoldierFromSnapshot,
			cfgFor: func(h string) config.ResolvedProjectConfig {
				return config.ResolvedProjectConfig{Project: "acme", SoldierHarness: h}
			},
			sentinel:   ErrNoSoldierHarnessInSnapshot,
			unknownMsg: "snapshot soldier harness",
		},
		{
			name:    "captain",
			resolve: ResolveCaptainFromSnapshot,
			cfgFor: func(h string) config.ResolvedProjectConfig {
				return config.ResolvedProjectConfig{Project: "acme", CaptainProfile: config.CaptainProfile{Harness: h, Model: "sonnet"}}
			},
			sentinel:   ErrNoCaptainHarnessInSnapshot,
			unknownMsg: "snapshot captain harness",
		},
	}
	for _, rank := range ranks {
		t.Run(rank.name+"/identity set resolves", func(t *testing.T) {
			got, err := rank.resolve(rank.cfgFor("pi"))
			if err != nil || got != "pi" {
				t.Fatalf("resolve = (%q, %v), want (pi, nil)", got, err)
			}
		})
		t.Run(rank.name+"/empty identity fails closed", func(t *testing.T) {
			_, err := rank.resolve(rank.cfgFor(""))
			if !errors.Is(err, rank.sentinel) || !strings.Contains(err.Error(), `project "acme"`) {
				t.Fatalf("empty identity error = %v, want %v naming the project", err, rank.sentinel)
			}
		})
		t.Run(rank.name+"/unknown harness fails closed", func(t *testing.T) {
			_, err := rank.resolve(rank.cfgFor("vim"))
			if err == nil || errors.Is(err, rank.sentinel) || !strings.Contains(err.Error(), rank.unknownMsg) {
				t.Fatalf("unknown harness error = %v, want the %q refusal", err, rank.unknownMsg)
			}
		})
	}
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
