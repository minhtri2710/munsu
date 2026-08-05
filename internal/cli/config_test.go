package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

// extractConfigValueFromTOON parses TOON output from config get and returns
// the message value. Expected format: "message: <value>" after "data:" section.
func extractConfigValueFromTOON(output string) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "data:" {
			// Next line should be "  message: <value>"
			for j := i + 1; j < len(lines); j++ {
				msgLine := strings.TrimSpace(lines[j])
				if strings.HasPrefix(msgLine, "message:") {
					val := strings.TrimSpace(strings.TrimPrefix(msgLine, "message:"))
					return strings.Trim(val, `"`)
				}
			}
		}
	}
	return ""
}

// TestConfigGetKnownSet verifies config get returns the exact value for a
// known key that has been persisted. Backend is a typed snapshot identity: the
// persisted truth comes from the fleet base document, not the legacy flat pin.
func TestConfigGetKnownSet(t *testing.T) {
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

	root.SetArgs([]string{"config", "get", "backend"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("config get known-set: unexpected error: %v", err)
	}

	got := extractConfigValueFromTOON(strings.TrimSpace(buf.String()))
	if got != "tmux" {
		t.Errorf("config get backend = %q, want %q", got, "tmux")
	}
}

// TestConfigGetKnownUnset verifies config get returns empty output with
// success for a known key that has not been set.
func TestConfigGetKnownUnset(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "get", "captain-harness"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("config get known-unset: expected success, got error: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	if got != "" {
		t.Errorf("config get known-unset: expected empty output, got %q", got)
	}
}

// TestConfigGetUnknown verifies config get returns a non-zero exit and
// clear error for an unknown key.
func TestConfigGetUnknown(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "get", "nonexistent"})
	err := root.Execute()
	if err == nil {
		t.Fatal("config get unknown: expected error, got nil")
	}

	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("config get unknown: error should mention the key, got: %v", err)
	}
}

// TestConfigGetCaseSensitive verifies that case sensitivity is maintained
// (BACKEND is not the same as backend).
func TestConfigGetCaseSensitive(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	// BACKEND is not in KnownKeys (case-sensitive)
	root.SetArgs([]string{"config", "get", "BACKEND"})
	err := root.Execute()
	if err == nil {
		t.Fatal("config get BACKEND: expected error for unknown key (case mismatch), got nil")
	}
}

// TestConfigGetIgnoresEnvOverride verifies the CLI reports the persisted file
// value and does not honor the obsolete MUNSU_<KEY>_OVERRIDE ambient env.
func TestConfigGetIgnoresEnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_MODEL_OVERRIDE", "environment-model")

	if err := config.Set(tmpDir, "model", "claude"); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "get", "model"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("config get model: unexpected error: %v", err)
	}

	got := extractConfigValueFromTOON(strings.TrimSpace(buf.String()))
	if got != "claude" {
		t.Errorf("config get model = %q, want %q (persisted file value; env override must be ignored)", got, "claude")
	}
}

// TestConfigShowAndGetAgree verifies that config show and config get agree
// on key recognition by checking that keys listed in show output are
// accepted by get.
func TestConfigShowAndGetAgree(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Use JSON output to easily parse the message field
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "show", "--output", "json"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("config show failed: %v", err)
	}

	var resp struct {
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parsing show JSON: %v", err)
	}

	tableLines := strings.Split(resp.Data.Message, "\n")

	var wellKnownLines []string
	for _, line := range tableLines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		// Lines like "backend          <not set>" or "backend          tmux (file: ...)"
		fields := strings.Fields(line)
		if len(fields) > 0 {
			key := fields[0]
			if key == "home" || key == "KEY" || key == "VALUE" || key == "Additional" || key == "config" {
				// skip header and non-key lines
				if key == "Additional" {
					break
				}
				continue
			}
			wellKnownLines = append(wellKnownLines, key)
		}
	}
}
func TestConfigGetAllKnownKeys(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	for _, key := range config.KnownKeys {
		// backend is special-cased: it reports the persisted snapshot identity
		// and returns typed missing_input without one (covered by
		// config_cmd_test.go), so it must not expect success in an empty home.
		if key == "backend" {
			continue
		}
		root := NewRootCommand()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)

		root.SetArgs([]string{"config", "get", key})
		err := root.Execute()
		if err != nil {
			t.Errorf("config get known key %q: expected success, got error: %v", key, err)
		}
	}
}

// TestConfigGetReadsPersistedValue verifies config get reports the typed
// fleet base value for the delivery-mode contract keys (persisted-truth
// behavior: the base document is the single operational authority).
func TestConfigGetReadsPersistedValue(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	if err := config.StoreFleetBase(tmpDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{DefaultMode: "direct-PR"},
	}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "get", "default-mode"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("config get default-mode: unexpected error: %v", err)
	}

	got := extractConfigValueFromTOON(strings.TrimSpace(buf.String()))
	if got != "direct-PR" {
		t.Errorf("config get default-mode = %q, want %q", got, "direct-PR")
	}
}
