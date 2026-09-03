package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
)

// runProjectConfig executes one `project config ...` invocation against a fresh
// root command and returns the trimmed output and the execution error.
func runProjectConfig(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"project", "config"}, args...))
	err := root.Execute()
	return strings.TrimSpace(buf.String()), err
}

// registerProject adds a project to the registry with a plain (non-URL) path so
// no clone is attempted.
func registerProject(t *testing.T, home, name string) {
	t.Helper()
	if err := fleet.Add(home, name, t.TempDir(), "", false); err != nil {
		t.Fatalf("register project %q: %v", name, err)
	}
}

// TestProjectConfigSetGetRoundTrip verifies a scalar overlay value written by
// `set` is read back by `get`, and that it lands in the Config-owned overlay
// document keyed by project name.
func TestProjectConfigSetGetRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")

	if _, err := runProjectConfig(t, "set", "sample", "default-mode", "direct-PR"); err != nil {
		t.Fatalf("set default-mode: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "require-no-mistakes", "true"); err != nil {
		t.Fatalf("set require-no-mistakes: %v", err)
	}

	out, err := runProjectConfig(t, "get", "sample", "default-mode")
	if err != nil {
		t.Fatalf("get default-mode: %v", err)
	}
	if got := extractConfigValueFromTOON(out); got != "direct-PR" {
		t.Errorf("get default-mode = %q, want %q", got, "direct-PR")
	}
	out, err = runProjectConfig(t, "get", "sample", "require-no-mistakes")
	if err != nil {
		t.Fatalf("get require-no-mistakes: %v", err)
	}
	if got := extractConfigValueFromTOON(out); got != "true" {
		t.Errorf("get require-no-mistakes = %q, want %q", got, "true")
	}

	// The write path is the Config-owned overlay document keyed by name.
	overlay, err := config.LoadProjectOverlay(home, "sample")
	if err != nil {
		t.Fatalf("LoadProjectOverlay: %v", err)
	}
	if overlay.DefaultMode != "direct-PR" || overlay.RequireNoMistakes == nil || !*overlay.RequireNoMistakes {
		t.Errorf("overlay = %+v, want DefaultMode=direct-PR RequireNoMistakes=true", overlay)
	}
}

// TestProjectConfigClearReturnsToInherit verifies an empty value clears a key so
// the project inherits the base document again (get reports empty success).
func TestProjectConfigClearReturnsToInherit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")

	if _, err := runProjectConfig(t, "set", "sample", "default-mode", "direct-PR"); err != nil {
		t.Fatalf("set default-mode: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "default-mode", ""); err != nil {
		t.Fatalf("clear default-mode: %v", err)
	}
	out, err := runProjectConfig(t, "get", "sample", "default-mode")
	if err != nil {
		t.Fatalf("get cleared default-mode: %v", err)
	}
	if out != "" {
		t.Errorf("get cleared default-mode = %q, want empty (inherit base)", out)
	}

	overlay, err := config.LoadProjectOverlay(home, "sample")
	if err != nil {
		t.Fatalf("LoadProjectOverlay: %v", err)
	}
	if overlay.DefaultMode != "" {
		t.Errorf("cleared overlay DefaultMode = %q, want empty", overlay.DefaultMode)
	}
}

// TestProjectConfigClearBoolReturnsToInherit verifies clearing a *bool key sets
// it back to nil (inherit), distinct from an explicit false.
func TestProjectConfigClearBoolReturnsToInherit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")

	if _, err := runProjectConfig(t, "set", "sample", "require-no-mistakes", "true"); err != nil {
		t.Fatalf("set require-no-mistakes: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "require-no-mistakes", ""); err != nil {
		t.Fatalf("clear require-no-mistakes: %v", err)
	}
	overlay, err := config.LoadProjectOverlay(home, "sample")
	if err != nil {
		t.Fatalf("LoadProjectOverlay: %v", err)
	}
	if overlay.RequireNoMistakes != nil {
		t.Errorf("cleared require-no-mistakes = %v, want nil (inherit base)", *overlay.RequireNoMistakes)
	}
}

func TestProjectConfigGetUnknownKeyRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")
	if _, err := runProjectConfig(t, "get", "sample", "bogus-key"); err == nil {
		t.Fatal("get with unknown key should be refused")
	}
}

func TestProjectConfigSetUnknownKeyRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")
	if _, err := runProjectConfig(t, "set", "sample", "bogus-key", "x"); err == nil {
		t.Fatal("set with unknown key should be refused")
	}
}

func TestProjectConfigGetUnknownProjectRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	if _, err := runProjectConfig(t, "get", "ghost", "default-mode"); err == nil {
		t.Fatal("get for an unregistered project should be refused")
	}
}

func TestProjectConfigSetUnknownProjectRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	if _, err := runProjectConfig(t, "set", "ghost", "default-mode", "direct-PR"); err == nil {
		t.Fatal("set for an unregistered project should be refused")
	}
}

func TestProjectConfigSetInvalidDeliveryModeRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")
	if _, err := runProjectConfig(t, "set", "sample", "default-mode", "bogus-mode"); err == nil {
		t.Fatal("set with an invalid delivery mode should be refused")
	}
}

func TestProjectConfigSetInvalidBoolRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")
	if _, err := runProjectConfig(t, "set", "sample", "require-no-mistakes", "maybe"); err == nil {
		t.Fatal("set with a non-bool require-no-mistakes should be refused")
	}
}

func TestProjectConfigSetInvalidHarnessRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")
	if _, err := runProjectConfig(t, "set", "sample", "soldier-harness", "no-such-harness"); err == nil {
		t.Fatal("set with an unsupported soldier-harness should be refused")
	}
}

// TestProjectConfigMalformedOverlayDocFailsClosed verifies both get and set
// fail closed when the Config-owned overlay document is present but carries an
// unsupported schemaVersion, exercising the overlay-load refusal branch.
func TestProjectConfigMalformedOverlayDocFailsClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")

	docPath := filepath.Join(home, config.ProjectOverlayDocumentPath)
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatalf("mkdir overlay dir: %v", err)
	}
	if err := os.WriteFile(docPath, []byte(`{"schemaVersion":"bogus/v0"}`), 0o644); err != nil {
		t.Fatalf("write malformed overlay doc: %v", err)
	}

	if _, err := runProjectConfig(t, "get", "sample", "default-mode"); err == nil {
		t.Fatal("get against a malformed overlay document should fail closed")
	}
	if _, err := runProjectConfig(t, "set", "sample", "default-mode", "direct-PR"); err == nil {
		t.Fatal("set against a malformed overlay document should fail closed")
	}
}
