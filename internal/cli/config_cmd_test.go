package cli

import (
	"bytes"
	"os"
	"path/filepath"
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
