package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
)

// TestConfigGetBackendReportsPersistedFleetBaseBackend verifies `config get
// backend` reports the persisted typed Backend from the fleet base document —
// not a live env/PATH probe.
func TestConfigGetBackendReportsPersistedFleetBaseBackend(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("HERDR_ENV", "1") // must not shadow the persisted Backend

	if err := config.StoreFleetBase(tmpDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{Backend: "tmux"},
	}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "get", "backend"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config get backend: %v", err)
	}

	if got := extractConfigValueFromTOON(strings.TrimSpace(buf.String())); got != "tmux" {
		t.Errorf("config get backend = %q, want %q (persisted fleet base Backend)", got, "tmux")
	}
}

// TestConfigGetBackendReportsPersistedPublishedSnapshot verifies `config get
// backend` reports the published snapshot Backend (the composed typed truth),
// which takes precedence over the fleet base document Backend.
func TestConfigGetBackendReportsPersistedPublishedSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	if err := config.StoreFleetBase(tmpDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{Backend: "tmux"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StorePublishedSnapshot(tmpDir, config.ResolvedProjectConfig{
		Project:     "sample",
		ProjectPath: "/tmp/sample",
		Digest:      "abc",
		Backend:     "herdr",
	}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "get", "backend"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config get backend: %v", err)
	}

	if got := extractConfigValueFromTOON(strings.TrimSpace(buf.String())); got != "herdr" {
		t.Errorf("config get backend = %q, want %q (published snapshot Backend)", got, "herdr")
	}
}

// TestConfigGetBackendWithoutPersistedIdentityIsTypedMissingInput verifies
// `config get backend` reports a typed missing-input result when no persisted
// snapshot backend identity exists — never a live probe.
func TestConfigGetBackendWithoutPersistedIdentityIsTypedMissingInput(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	// Environment must not select a backend on behalf of diagnostics.
	t.Setenv("TMUX", "/tmp/tmux-xxx/default")
	t.Setenv("HERDR_ENV", "")

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "get", "backend"})
	err := root.Execute()
	if err == nil {
		t.Fatal("config get backend without persisted identity: expected typed missing-input, got nil")
	}
	WriteContractError(buf, err, []string{"config", "get", "backend"})
	out := strings.TrimSpace(buf.String())
	if !strings.Contains(out, "error_code: missing_input") {
		t.Errorf("config get backend must return typed missing_input, got:\n%s", out)
	}
	if !strings.Contains(out, "no persisted backend identity") {
		t.Errorf("config get backend must explain the missing persisted identity, got:\n%s", out)
	}
}

// TestConfigGetBackendLegacyPinAloneIsTypedMissingInput verifies a legacy
// config file pin is not a persisted snapshot identity: without a typed
// document, config get backend reports typed missing-input.
func TestConfigGetBackendLegacyPinAloneIsTypedMissingInput(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	if err := config.Set(tmpDir, "backend", "tmux"); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "get", "backend"})
	err := root.Execute()
	if err == nil {
		t.Fatal("config get backend with only a legacy pin: expected typed missing-input, got nil")
	}
	WriteContractError(buf, err, []string{"config", "get", "backend"})
	out := strings.TrimSpace(buf.String())
	if !strings.Contains(out, "error_code: missing_input") {
		t.Errorf("config get backend with only a legacy pin must return typed missing_input, got:\n%s", out)
	}
}

// TestConfigSetCaptainHarnessWritesBaseDocumentProfile verifies `config set
// captain-harness` authors the CaptainProfile into the fleet base document
// (config/base.json) — the only captain operation source — and writes no flat
// config file. `config get captain-harness` reconstructs the pin line from the
// stored profile.
func TestConfigSetCaptainHarnessWritesBaseDocumentProfile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	if err := runConfigSet(t, "config", "set", "captain-harness", "pi cliproxyapi/grok-4.5 low"); err != nil {
		t.Fatalf("config set captain-harness: %v", err)
	}

	base, err := config.LoadFleetBase(tmpDir)
	if err != nil {
		t.Fatalf("loading fleet base after set: %v", err)
	}
	if base.CaptainProfile.Harness != "pi" || base.CaptainProfile.Model != "cliproxyapi/grok-4.5" || base.CaptainProfile.Effort != "low" {
		t.Fatalf("base captainProfile = %+v, want pi/cliproxyapi/grok-4.5/low", base.CaptainProfile)
	}
	// No flat config/captain-harness file is written.
	if _, err := config.Get(tmpDir, "captain-harness"); err == nil {
		t.Fatal("flat config/captain-harness must not be written")
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"config", "get", "captain-harness"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config get captain-harness: %v", err)
	}
	if got := extractConfigValueFromTOON(strings.TrimSpace(buf.String())); got != "pi cliproxyapi/grok-4.5 low" {
		t.Errorf("config get captain-harness = %q, want %q", got, "pi cliproxyapi/grok-4.5 low")
	}
}

// TestConfigSetLaunchProfileKeysAuthorFleetBase verifies `config set
// soldier-harness` and `config set model` author the typed launch-profile
// fields into the fleet base document (the single operational authority),
// write no flat config file, and round-trip through get/show. The "default"
// sentinel normalizes to unset at the write boundary.
func TestConfigSetLaunchProfileKeysAuthorFleetBase(t *testing.T) {
	t.Run("soldier-harness", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("MUNSU_HOME", tmpDir)

		if err := runConfigSet(t, "config", "set", "soldier-harness", "pi"); err != nil {
			t.Fatalf("config set soldier-harness: %v", err)
		}
		base, err := config.LoadFleetBase(tmpDir)
		if err != nil {
			t.Fatalf("loading fleet base: %v", err)
		}
		if base.Config.SoldierHarness != "pi" {
			t.Fatalf("base soldierHarness = %q, want pi", base.Config.SoldierHarness)
		}
		if _, err := config.Get(tmpDir, "soldier-harness"); err == nil {
			t.Fatal("flat config/soldier-harness must not be written")
		}
		if got := configGetValue(t, "soldier-harness"); got != "pi" {
			t.Errorf("config get soldier-harness = %q, want pi", got)
		}

		// "default" normalizes to unset (canonical empty).
		if err := runConfigSet(t, "config", "set", "soldier-harness", "default"); err != nil {
			t.Fatalf("config set soldier-harness default: %v", err)
		}
		base, _ = config.LoadFleetBase(tmpDir)
		if base.Config.SoldierHarness != "" {
			t.Errorf("soldierHarness = %q, want empty after default", base.Config.SoldierHarness)
		}
	})

	t.Run("model", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("MUNSU_HOME", tmpDir)

		if err := runConfigSet(t, "config", "set", "model", "cliproxyapi/grok-4.5"); err != nil {
			t.Fatalf("config set model: %v", err)
		}
		base, err := config.LoadFleetBase(tmpDir)
		if err != nil {
			t.Fatalf("loading fleet base: %v", err)
		}
		if base.Config.Model != "cliproxyapi/grok-4.5" {
			t.Fatalf("base model = %q, want cliproxyapi/grok-4.5", base.Config.Model)
		}
		if _, err := config.Get(tmpDir, "model"); err == nil {
			t.Fatal("flat config/model must not be written")
		}
		if got := configGetValue(t, "model"); got != "cliproxyapi/grok-4.5" {
			t.Errorf("config get model = %q, want cliproxyapi/grok-4.5", got)
		}

		if err := runConfigSet(t, "config", "set", "model", "default"); err != nil {
			t.Fatalf("config set model default: %v", err)
		}
		base, _ = config.LoadFleetBase(tmpDir)
		if base.Config.Model != "" {
			t.Errorf("model = %q, want empty after default", base.Config.Model)
		}
	})

	t.Run("soldier-harness rejects unknown", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("MUNSU_HOME", tmpDir)
		if err := runConfigSet(t, "config", "set", "soldier-harness", "not-a-harness"); err == nil {
			t.Fatal("expected validation error for unknown soldier harness")
		}
	})
}

// configGetValue runs `config get <key>` and returns the rendered value.
func configGetValue(t *testing.T, key string) string {
	t.Helper()
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"config", "get", key})
	if err := root.Execute(); err != nil {
		t.Fatalf("config get %s: %v", key, err)
	}
	return extractConfigValueFromTOON(strings.TrimSpace(buf.String()))
}

// TestConfigSetCaptainHarnessPreservesExistingBaseDocument verifies authored
// base fields (e.g. Backend) survive a captain-harness set.
func TestConfigSetCaptainHarnessPreservesExistingBaseDocument(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	if err := config.StoreFleetBase(tmpDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{Backend: "tmux"},
	}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "set", "captain-harness", "claude"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config set captain-harness: %v", err)
	}

	base, err := config.LoadFleetBase(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if base.Config.Backend != "tmux" {
		t.Fatalf("backend lost after set: %+v", base)
	}
	if base.CaptainProfile.Harness != "claude" {
		t.Fatalf("captainProfile = %+v, want claude", base.CaptainProfile)
	}
}

// TestConfigSetCaptainHarnessMalformedBaseFailsClosed verifies a malformed
// existing base.json is never self-repaired by config set.
func TestConfigSetCaptainHarnessMalformedBaseFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	if err := os.MkdirAll(filepath.Join(tmpDir, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, config.BaseDocumentPath), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "set", "captain-harness", "pi"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected failure for malformed base.json")
	}
	if !strings.Contains(err.Error(), "fleet base document") {
		t.Fatalf("error = %v, want fleet base document failure", err)
	}
}

// TestConfigSetLaunchProfileRejectsCaptainHome verifies launch-profile keys
// cannot be authored from a Captain home. A Captain home is identified by its
// durable config/parent-home pointer (present before the first published
// snapshot); the write is refused and no base.json is created.
func TestConfigSetLaunchProfileRejectsCaptainHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	if err := config.Set(home, "parent-home", "/var/munsu/general"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"soldier-harness", "model", "captain-harness"} {
		if err := runConfigSet(t, "config", "set", key, "pi"); err == nil {
			t.Fatalf("expected Captain-home rejection for %s", key)
		}
	}
	if _, err := os.Stat(filepath.Join(home, config.BaseDocumentPath)); !os.IsNotExist(err) {
		t.Fatalf("base document must not be created from a Captain home: %v", err)
	}
}

// TestConfigSetModelRejectsMultiToken verifies `config set model` fails fast on
// multi-token input rather than silently truncating to the first token.
func TestConfigSetModelRejectsMultiToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	if err := runConfigSet(t, "config", "set", "model", "cliproxyapi/grok-4.5 extra"); err == nil {
		t.Fatal("expected multi-token model to be rejected")
	}
	if _, err := os.Stat(filepath.Join(home, config.BaseDocumentPath)); !os.IsNotExist(err) {
		t.Fatalf("base document must not be written on rejected model: %v", err)
	}
}

func runConfigSet(t *testing.T, args ...string) error {
	t.Helper()
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	return root.Execute()
}

// TestConfigSetDefaultModeAuthorsFleetBase verifies `config set default-mode`
// authors the typed DefaultMode into the fleet base document (the single
// operational authority) and writes no flat config file.
func TestConfigSetDefaultModeAuthorsFleetBase(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	if err := runConfigSet(t, "config", "set", "default-mode", "direct-PR"); err != nil {
		t.Fatalf("config set default-mode: %v", err)
	}

	base, err := config.LoadFleetBase(tmpDir)
	if err != nil {
		t.Fatalf("loading fleet base after set: %v", err)
	}
	if base.Config.DefaultMode != "direct-PR" {
		t.Fatalf("base defaultMode = %q, want direct-PR", base.Config.DefaultMode)
	}
	if _, err := config.Get(tmpDir, "default-mode"); err == nil {
		t.Fatal("flat config/default-mode must not be written")
	}
}

// TestConfigSetRequireNoMistakesAuthorsFleetBase verifies `config set
// require-no-mistakes true` actually turns on the typed gate in the fleet base
// document (fail-closed behavior is preserved) with no flat file echo.
func TestConfigSetRequireNoMistakesAuthorsFleetBase(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	if err := runConfigSet(t, "config", "set", "require-no-mistakes", "true"); err != nil {
		t.Fatalf("config set require-no-mistakes: %v", err)
	}

	base, err := config.LoadFleetBase(tmpDir)
	if err != nil {
		t.Fatalf("loading fleet base after set: %v", err)
	}
	if base.Config.RequireNoMistakes == nil || !*base.Config.RequireNoMistakes {
		t.Fatalf("base requireNoMistakes = %v, want true", base.Config.RequireNoMistakes)
	}
	if _, err := config.Get(tmpDir, "require-no-mistakes"); err == nil {
		t.Fatal("flat config/require-no-mistakes must not be written")
	}
}

// TestConfigSetBackendAuthorsFleetBase verifies `config set backend` authors
// the typed Backend into the fleet base document and `config get backend`
// reports it (the persisted snapshot identity).
func TestConfigSetBackendAuthorsFleetBase(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	if err := runConfigSet(t, "config", "set", "backend", "tmux"); err != nil {
		t.Fatalf("config set backend: %v", err)
	}

	base, err := config.LoadFleetBase(tmpDir)
	if err != nil {
		t.Fatalf("loading fleet base after set: %v", err)
	}
	if base.Config.Backend != "tmux" {
		t.Fatalf("base backend = %q, want tmux", base.Config.Backend)
	}
	if _, err := config.Get(tmpDir, "backend"); err == nil {
		t.Fatal("flat config/backend must not be written")
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"config", "get", "backend"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config get backend: %v", err)
	}
	if got := extractConfigValueFromTOON(strings.TrimSpace(buf.String())); got != "tmux" {
		t.Errorf("config get backend = %q, want tmux", got)
	}
}

// TestConfigSetTypedKeysPreserveExistingBaseFields verifies authored base
// fields survive a typed-key set (no clobbering).
func TestConfigSetTypedKeysPreserveExistingBaseFields(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	if err := config.StoreFleetBase(tmpDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.ProjectOverlay{
			Backend:           "herdr",
			RequireNoMistakes: &[]bool{true}[0],
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := runConfigSet(t, "config", "set", "default-mode", "direct-PR"); err != nil {
		t.Fatalf("config set default-mode: %v", err)
	}

	base, err := config.LoadFleetBase(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if base.Config.Backend != "herdr" {
		t.Fatalf("backend lost after set: %+v", base.Config)
	}
	if base.Config.RequireNoMistakes == nil || !*base.Config.RequireNoMistakes {
		t.Fatalf("requireNoMistakes lost after set: %+v", base.Config)
	}
	if base.Config.DefaultMode != "direct-PR" {
		t.Fatalf("defaultMode = %q, want direct-PR", base.Config.DefaultMode)
	}
}

// TestConfigSetTypedKeysValidateInput verifies typed-key set validates its
// input: invalid delivery mode, non-boolean require-no-mistakes, and empty
// backend identity are rejected.
func TestConfigSetTypedKeysValidateInput(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{"default-mode", "aggressive"},
		{"require-no-mistakes", "maybe"},
		{"allow-direct-pr-fallback", "maybe"},
		{"backend", ""},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("MUNSU_HOME", tmpDir)
			if err := runConfigSet(t, "config", "set", tc.key, tc.value); err == nil {
				t.Fatalf("config set %s %q: expected validation error", tc.key, tc.value)
			}
		})
	}
}

// TestConfigSetTypedKeyMalformedBaseFailsClosed verifies a malformed existing
// base.json is never self-repaired by a typed-key set.
func TestConfigSetTypedKeyMalformedBaseFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	if err := os.MkdirAll(filepath.Join(tmpDir, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, config.BaseDocumentPath), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := runConfigSet(t, "config", "set", "backend", "tmux"); err == nil {
		t.Fatal("expected failure for malformed base.json")
	}
}

// TestConfigGetTypedKeysKnownUnset verifies config get default-mode and
// require-no-mistakes report empty success on a fresh home (known-unset),
// matching the flat known-unset contract.
func TestConfigGetTypedKeysKnownUnset(t *testing.T) {
	for _, key := range []string{"default-mode", "require-no-mistakes", "allow-direct-pr-fallback", "soldier-harness", "model", "captain-harness"} {
		t.Run(key, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("MUNSU_HOME", tmpDir)

			root := NewRootCommand()
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs([]string{"config", "get", key})
			if err := root.Execute(); err != nil {
				t.Fatalf("config get %s known-unset: expected success, got error: %v", key, err)
			}
			if got := strings.TrimSpace(buf.String()); got != "" {
				t.Errorf("config get %s known-unset: expected empty output, got %q", key, got)
			}
		})
	}
}

// TestConfigGetRequireNoMistakesReportsTypedValue verifies config get
// require-no-mistakes reports the persisted typed value, including an
// explicitly authored false.
func TestConfigGetRequireNoMistakesReportsTypedValue(t *testing.T) {
	for _, tc := range []struct {
		set  bool
		want string
	}{
		{true, "true"},
		{false, "false"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("MUNSU_HOME", tmpDir)
			if err := config.StoreFleetBase(tmpDir, config.FleetBaseDocument{
				SchemaVersion: config.FleetBaseSchemaVersion,
				Config:        config.ProjectOverlay{RequireNoMistakes: &tc.set},
			}); err != nil {
				t.Fatal(err)
			}

			root := NewRootCommand()
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs([]string{"config", "get", "require-no-mistakes"})
			if err := root.Execute(); err != nil {
				t.Fatalf("config get require-no-mistakes: %v", err)
			}
			if got := extractConfigValueFromTOON(strings.TrimSpace(buf.String())); got != tc.want {
				t.Errorf("config get require-no-mistakes = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConfigSetAllowDirectPRFallbackRoundTrips is the regression test for the
// round-trip bug: `config set allow-direct-pr-fallback` previously fell through
// to the flat store while get/show read the fleet base document, so a set was
// never observable. The setter now authors the typed base field, and get reports
// it. Both an authored true and false round-trip with no flat file echo.
func TestConfigSetAllowDirectPRFallbackRoundTrips(t *testing.T) {
	for _, want := range []string{"true", "false"} {
		t.Run(want, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("MUNSU_HOME", tmpDir)

			if err := runConfigSet(t, "config", "set", "allow-direct-pr-fallback", want); err != nil {
				t.Fatalf("config set allow-direct-pr-fallback: %v", err)
			}

			base, err := config.LoadFleetBase(tmpDir)
			if err != nil {
				t.Fatalf("loading fleet base after set: %v", err)
			}
			if base.Config.AllowDirectPRFallback == nil || strconv.FormatBool(*base.Config.AllowDirectPRFallback) != want {
				t.Fatalf("base allowDirectPRFallback = %v, want %s", base.Config.AllowDirectPRFallback, want)
			}
			if _, err := config.Get(tmpDir, "allow-direct-pr-fallback"); err == nil {
				t.Fatal("flat config/allow-direct-pr-fallback must not be written")
			}

			root := NewRootCommand()
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs([]string{"config", "get", "allow-direct-pr-fallback"})
			if err := root.Execute(); err != nil {
				t.Fatalf("config get allow-direct-pr-fallback: %v", err)
			}
			if got := extractConfigValueFromTOON(strings.TrimSpace(buf.String())); got != want {
				t.Errorf("config get allow-direct-pr-fallback = %q, want %q", got, want)
			}
		})
	}
}

// runMunsuCLI executes one munsu command through the real root command and
// returns its contract output.
func runMunsuCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	err := root.Execute()
	return strings.TrimSpace(buf.String()), err
}

// configShowRows parses `config show --output json` into a key -> rendered
// value map. The show table is the command's user-facing rendered output, so
// the assertion is about what an operator reads, parsed semantically rather
// than substring-matched.
func configShowRows(t *testing.T, output string) map[string]string {
	t.Helper()
	var resp struct {
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("parsing config show JSON: %v", err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(resp.Data.Message, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Additional config keys:") {
			break
		}
		if len(line) < 31 || strings.HasPrefix(line, " ") {
			continue
		}
		key := strings.TrimSpace(line[:30])
		if key == "" || key == "KEY" || strings.HasPrefix(key, "-") {
			continue
		}
		rows[key] = strings.TrimSpace(line[30:])
	}
	return rows
}

// TestConfigSetAllowDirectPRFallbackFeedsConfigSurfacesAndSpawnSnapshot walks
// the whole operator path for the typed key: `config set
// allow-direct-pr-fallback` authors the fleet base document, `config get` and
// `config show` report the authored value, the flat file-per-key store is never
// written, and the resolved spawn snapshot that the Runner consumes observes
// the same value. An explicitly authored false is covered too, so the assertion
// is about the authored value and not about a zero value that happens to match.
func TestConfigSetAllowDirectPRFallbackFeedsConfigSurfacesAndSpawnSnapshot(t *testing.T) {
	for _, want := range []bool{true, false} {
		t.Run(strconv.FormatBool(want), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("MUNSU_HOME", home)
			projectPath := filepath.Join(t.TempDir(), "alpha")
			if err := os.MkdirAll(projectPath, 0o755); err != nil {
				t.Fatal(err)
			}

			// Deterministic operator setup through the CLI only: a registered
			// project plus the typed identities the spawn resolver requires. No
			// host probing, so the resolved snapshot depends solely on what these
			// commands authored.
			for _, args := range [][]string{
				{"project", "add", "alpha", projectPath},
				{"project", "config", "set", "alpha", "soldier-harness", "pi"},
				{"config", "set", "backend", "tmux"},
				{"config", "set", "allow-direct-pr-fallback", strconv.FormatBool(want)},
			} {
				if _, err := runMunsuCLI(t, args...); err != nil {
					t.Fatalf("munsu %s: %v", strings.Join(args, " "), err)
				}
			}

			// The fleet base document is the authored persisted state.
			base, err := config.LoadFleetBase(home)
			if err != nil {
				t.Fatalf("loading fleet base after set: %v", err)
			}
			if base.Config.AllowDirectPRFallback == nil || *base.Config.AllowDirectPRFallback != want {
				t.Fatalf("base allowDirectPRFallback = %v, want %v", base.Config.AllowDirectPRFallback, want)
			}

			// The flat file-per-key store is never written for this typed key.
			flatPath := filepath.Join(home, "config", "allow-direct-pr-fallback")
			if _, err := os.Stat(flatPath); !os.IsNotExist(err) {
				t.Errorf("flat %s must not exist, stat err = %v", flatPath, err)
			}
			if _, err := config.Get(home, "allow-direct-pr-fallback"); err == nil {
				t.Error("flat config/allow-direct-pr-fallback must not be readable")
			}

			getOut, err := runMunsuCLI(t, "config", "get", "allow-direct-pr-fallback")
			if err != nil {
				t.Fatalf("config get allow-direct-pr-fallback: %v", err)
			}
			gotGet := extractConfigValueFromTOON(getOut)
			if gotGet != strconv.FormatBool(want) {
				t.Errorf("config get allow-direct-pr-fallback = %q, want %q", gotGet, strconv.FormatBool(want))
			}

			showOut, err := runMunsuCLI(t, "config", "show", "--output", "json")
			if err != nil {
				t.Fatalf("config show: %v", err)
			}
			gotShow := configShowRows(t, showOut)["allow-direct-pr-fallback"]
			wantShow := strconv.FormatBool(want) + " (typed config)"
			if gotShow != wantShow {
				t.Errorf("config show allow-direct-pr-fallback = %q, want %q", gotShow, wantShow)
			}

			// The real spawn consumer resolves the same authored value.
			resolved, err := fleet.ResolveSpawnProjectConfig(home, fleet.Args{ProjectName: "alpha"}, fleet.DispatchPolicyGeneralDirect, nil)
			if err != nil {
				t.Fatalf("resolving spawn project config: %v", err)
			}
			if resolved.AllowDirectPRFallback != want {
				t.Errorf("resolved spawn snapshot AllowDirectPRFallback = %v, want %v", resolved.AllowDirectPRFallback, want)
			}

			t.Logf("munsu config set allow-direct-pr-fallback %v -> base.json allowDirectPRFallback=%v, config get=%q, config show=%q, resolved spawn snapshot AllowDirectPRFallback=%v, flat config/allow-direct-pr-fallback written=false",
				want, *base.Config.AllowDirectPRFallback, gotGet, gotShow, resolved.AllowDirectPRFallback)
		})
	}
}
