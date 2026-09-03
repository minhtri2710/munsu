package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
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
// (config/base.json) — the ONLY captain operation source — while keeping the
// flat file as a diagnostics-only echo.
func TestConfigSetCaptainHarnessWritesBaseDocumentProfile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "set", "captain-harness", "pi cliproxyapi/grok-4.5 low"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config set captain-harness: %v", err)
	}

	base, err := config.LoadFleetBase(tmpDir)
	if err != nil {
		t.Fatalf("loading fleet base after set: %v", err)
	}
	if base.CaptainProfile.Harness != "pi" || base.CaptainProfile.Model != "cliproxyapi/grok-4.5" || base.CaptainProfile.Effort != "low" {
		t.Fatalf("base captainProfile = %+v, want pi/cliproxyapi/grok-4.5/low", base.CaptainProfile)
	}
	// The flat file remains a diagnostics-only echo.
	if got, err := config.Get(tmpDir, "captain-harness"); err != nil || got != "pi cliproxyapi/grok-4.5 low" {
		t.Fatalf("flat captain-harness echo = %q, %v", got, err)
	}
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
	for _, key := range []string{"default-mode", "require-no-mistakes", "allow-direct-pr-fallback"} {
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
