package cli

import (
	"bytes"
	"encoding/json"
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
		Config:        config.FleetBaseConfig{Backend: "tmux"},
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
		Config:        config.FleetBaseConfig{Backend: "tmux"},
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
		Config:        config.FleetBaseConfig{Backend: "tmux"},
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

// TestConfigSetLaunchProfileRejectsEmptyOrUnreadableCaptainPointer verifies
// launch-profile writes fail closed for present but unusable Captain pointers.
func TestConfigSetLaunchProfileRejectsEmptyOrUnreadableCaptainPointer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(string) error
	}{
		{name: "empty", write: func(path string) error {
			return os.WriteFile(path, []byte("\n"), 0600)
		}},
		{name: "dangling symlink", write: func(path string) error {
			return os.Symlink(filepath.Join(filepath.Dir(path), "missing-parent"), path)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("MUNSU_HOME", home)
			configDir := filepath.Join(home, "config")
			if err := os.MkdirAll(configDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := tc.write(filepath.Join(configDir, "parent-home")); err != nil {
				t.Fatal(err)
			}
			if err := runConfigSet(t, "config", "set", "model", "pi"); err == nil {
				t.Fatal("expected Captain-home launch-profile write to fail closed")
			}
			if _, err := os.Stat(filepath.Join(home, config.BaseDocumentPath)); !os.IsNotExist(err) {
				t.Fatalf("base document must not be created: %v", err)
			}
		})
	}
}

// TestConfigShowRejectsMalformedBaseAndShowsSparseBase verifies invalid base
// documents fail closed while valid sparse documents preserve unset rendering.
func TestConfigShowRejectsMalformedBaseAndShowsSparseBase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	basePath := filepath.Join(home, config.BaseDocumentPath)
	if err := os.MkdirAll(filepath.Dir(basePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runMunsuCLI(t, "config", "show", "--output", "json"); err == nil {
		t.Fatal("expected config show to reject malformed base.json")
	}

	if err := config.StoreFleetBase(home, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "stray"), []byte("ignored"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := runMunsuCLI(t, "config", "show", "--output", "json")
	if err != nil {
		t.Fatalf("config show sparse base: %v", err)
	}
	if strings.Contains(output, "Additional config keys:") {
		t.Fatalf("config show contains removed additional-keys header with stray file: %s", output)
	}
	rows := configShowRows(t, output)
	for _, key := range []string{"soldier-harness", "model", "captain-harness"} {
		if got := rows[key]; got != "<not set>" {
			t.Errorf("config show %s = %q, want <not set>", key, got)
		}
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

// TestConfigSetTypedKeysValidateInput verifies typed-key set validates its
// input: an empty backend identity is rejected.
func TestConfigSetTypedKeysValidateInput(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
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

// TestConfigGetTypedKeysKnownUnset verifies config get of soldier-harness,
// model, and captain-harness reports empty success on a fresh home (known-unset),
// matching the flat known-unset contract.
func TestConfigGetTypedKeysKnownUnset(t *testing.T) {
	for _, key := range []string{"soldier-harness", "model", "captain-harness"} {
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
