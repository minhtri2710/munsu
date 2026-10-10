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
	if err := fleet.Add(home, name, t.TempDir(), false); err != nil {
		t.Fatalf("register project %q: %v", name, err)
	}
}

// TestProjectConfigSetGetRoundTrip verifies a tool entry and a scalar overlay
// value written by `set` are read back by `get`, and that they land in the
// Config-owned overlay document keyed by project name.
func TestProjectConfigSetGetRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")

	if _, err := runProjectConfig(t, "set", "sample", "forge", `{"adapter":"github"}`); err != nil {
		t.Fatalf("set forge: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "review", `{"adapter":"no-mistakes"}`); err != nil {
		t.Fatalf("set review: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "tamper-check", "floor --base <base>"); err != nil {
		t.Fatalf("set tamper-check: %v", err)
	}

	out, err := runProjectConfig(t, "get", "sample", "review")
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if got := extractConfigValueFromTOON(out); !strings.Contains(got, "no-mistakes") {
		t.Errorf("get review = %q, want the stored no-mistakes tool entry", got)
	}

	out, err = runProjectConfig(t, "get", "sample", "tamper-check")
	if err != nil {
		t.Fatalf("get tamper-check: %v", err)
	}
	if got := extractConfigValueFromTOON(out); got != "floor --base <base>" {
		t.Errorf("get tamper-check = %q, want %q", got, "floor --base <base>")
	}

	// The write path is the Config-owned overlay document keyed by name.
	overlay, err := config.LoadProjectOverlay(home, "sample")
	if err != nil {
		t.Fatalf("LoadProjectOverlay: %v", err)
	}
	if overlay.Review == nil || overlay.Review.Adapter != "no-mistakes" || overlay.Forge == nil || overlay.Forge.Adapter != "github" || overlay.TamperCheck != "floor --base <base>" {
		t.Errorf("overlay = %+v, want review no-mistakes, forge github, TamperCheck=%q", overlay, "floor --base <base>")
	}
}

// TestProjectConfigClearReturnsToInherit verifies an empty value clears a key so
// the project inherits the base document again (get reports empty success).
func TestProjectConfigClearReturnsToInherit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")

	if _, err := runProjectConfig(t, "set", "sample", "forge", `{"adapter":"github"}`); err != nil {
		t.Fatalf("set forge: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "review", `{"adapter":"no-mistakes"}`); err != nil {
		t.Fatalf("set review: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "tamper-check", "floor --base <base>"); err != nil {
		t.Fatalf("set tamper-check: %v", err)
	}
	// Clearing forge while review still needs it is refused, so review clears first.
	if _, err := runProjectConfig(t, "set", "sample", "review", ""); err != nil {
		t.Fatalf("clear review: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "forge", ""); err != nil {
		t.Fatalf("clear forge: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "tamper-check", ""); err != nil {
		t.Fatalf("clear tamper-check: %v", err)
	}
	out, err := runProjectConfig(t, "get", "sample", "review")
	if err != nil {
		t.Fatalf("get cleared review: %v", err)
	}
	if out != "" {
		t.Errorf("get cleared review = %q, want empty (inherit base)", out)
	}

	overlay, err := config.LoadProjectOverlay(home, "sample")
	if err != nil {
		t.Fatalf("LoadProjectOverlay: %v", err)
	}
	if overlay.Review != nil || overlay.Forge != nil || overlay.TamperCheck != "" {
		t.Errorf("cleared overlay review = %+v forge = %+v TamperCheck = %q, want empty", overlay.Review, overlay.Forge, overlay.TamperCheck)
	}
}

// TestProjectConfigRefusesInvalidToolEntryWithoutWriting verifies a tool entry
// that fails config validation is refused and the overlay document is left as
// it was.
func TestProjectConfigRefusesInvalidToolEntryWithoutWriting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")

	if _, err := runProjectConfig(t, "set", "sample", "forge", `{"adapter":"github","path":"/usr/bin/gh-axi"}`); err == nil {
		t.Fatal("set forge with a path on github should be refused")
	}
	overlay, err := config.LoadProjectOverlay(home, "sample")
	if err != nil {
		t.Fatalf("LoadProjectOverlay: %v", err)
	}
	if overlay.Forge != nil {
		t.Errorf("refused forge entry was written: %+v", overlay.Forge)
	}

	// Clearing the forge under a configured no-mistakes review is refused, and
	// the overlay keeps both entries.
	if _, err := runProjectConfig(t, "set", "sample", "forge", `{"adapter":"github"}`); err != nil {
		t.Fatalf("set forge: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "review", `{"adapter":"no-mistakes"}`); err != nil {
		t.Fatalf("set review: %v", err)
	}
	if _, err := runProjectConfig(t, "set", "sample", "forge", ""); err == nil {
		t.Fatal("clearing forge under a no-mistakes review should be refused")
	}
	overlay, err = config.LoadProjectOverlay(home, "sample")
	if err != nil {
		t.Fatalf("LoadProjectOverlay: %v", err)
	}
	if overlay.Forge == nil || overlay.Review == nil {
		t.Errorf("refused clear changed the overlay: review = %+v forge = %+v", overlay.Review, overlay.Forge)
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
	if _, err := runProjectConfig(t, "get", "ghost", "review"); err == nil {
		t.Fatal("get for an unregistered project should be refused")
	}
}

func TestProjectConfigSetUnknownProjectRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	if _, err := runProjectConfig(t, "set", "ghost", "forge", `{"adapter":"github"}`); err == nil {
		t.Fatal("set for an unregistered project should be refused")
	}
}

func TestProjectConfigSetTamperCheckRefusesMarkdownBreakingValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	registerProject(t, home, "sample")
	for name, value := range map[string]string{"line feed": "floor\n--base <base>", "carriage return": "floor\r--base <base>", "backtick": "`floor` --base <base>"} {
		t.Run(name, func(t *testing.T) {
			_, err := runProjectConfig(t, "set", "sample", "tamper-check", value)
			if err == nil || !strings.Contains(err.Error(), "tamper-check must not contain a carriage return, line feed or backtick") {
				t.Fatalf("set tamper-check %q error = %v, want the refusal naming the key and character class", value, err)
			}
		})
	}
	overlay, err := config.LoadProjectOverlay(home, "sample")
	if err != nil {
		t.Fatalf("LoadProjectOverlay: %v", err)
	}
	if overlay.TamperCheck != "" {
		t.Errorf("a refused value was stored: %q", overlay.TamperCheck)
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

	if _, err := runProjectConfig(t, "get", "sample", "review"); err == nil {
		t.Fatal("get against a malformed overlay document should fail closed")
	}
	if _, err := runProjectConfig(t, "set", "sample", "forge", `{"adapter":"github"}`); err == nil {
		t.Fatal("set against a malformed overlay document should fail closed")
	}
}
