package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

func TestConfigDispatchSetDefaultAndAdd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_HOME", tmp)
	os.MkdirAll(filepath.Join(tmp, "config"), 0755)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"config", "dispatch", "set-default", "pi", "--model", "opencode-go/deepseek-v4-flash", "--effort", "low"})
	if err := root.Execute(); err != nil {
		t.Fatalf("set-default: %v\n%s", err, buf.String())
	}

	buf.Reset()
	root = NewRootCommand()
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{
		"config", "dispatch", "add",
		"--name", "review",
		"--match", "review",
		"--match", "audit",
		"--harness", "codex",
		"--model", "gpt-5.2-codex",
		"--effort", "high",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("add: %v\n%s", err, buf.String())
	}

	cfg, err := harness.LoadDispatch(harness.DispatchPath(tmp))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultHarness != "pi" || cfg.DefaultModel != "opencode-go/deepseek-v4-flash" || cfg.DefaultEffort != "low" {
		t.Fatalf("default = %+v", cfg)
	}
	if len(cfg.Profiles) != 1 {
		t.Fatalf("profiles = %d, want 1", len(cfg.Profiles))
	}
	p := cfg.Profiles[0]
	if p.Name != "review" || p.Harness != "codex" || p.Model != "gpt-5.2-codex" || p.Effort != "high" {
		t.Fatalf("profile = %+v", p)
	}
	if len(p.Match) != 2 {
		t.Fatalf("match = %v", p.Match)
	}

	// replace
	buf.Reset()
	root = NewRootCommand()
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{
		"config", "dispatch", "add",
		"--name", "review",
		"--match", "review",
		"--harness", "pi",
		"--model", "flash",
		"--effort", "medium",
		"--replace",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("replace: %v\n%s", err, buf.String())
	}
	cfg, err = harness.LoadDispatch(harness.DispatchPath(tmp))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profiles[0].Harness != "pi" || cfg.Profiles[0].Effort != "medium" {
		t.Fatalf("replaced profile = %+v", cfg.Profiles[0])
	}

	// show
	buf.Reset()
	root = NewRootCommand()
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"config", "dispatch", "show"})
	if err := root.Execute(); err != nil {
		t.Fatalf("show: %v\n%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "review") || !strings.Contains(out, "default:") {
		t.Fatalf("show output unexpected: %s", out)
	}

	// rm
	buf.Reset()
	root = NewRootCommand()
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"config", "dispatch", "rm", "review"})
	if err := root.Execute(); err != nil {
		t.Fatalf("rm: %v\n%s", err, buf.String())
	}
	cfg, err = harness.LoadDispatch(harness.DispatchPath(tmp))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != 0 {
		t.Fatalf("after rm profiles=%d", len(cfg.Profiles))
	}
}

func TestConfigDispatchClear(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_HOME", tmp)
	path := harness.DispatchPath(tmp)
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := harness.SaveDispatch(path, &harness.DispatchConfig{DefaultHarness: "pi"}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"config", "dispatch", "clear"})
	if err := root.Execute(); err != nil {
		t.Fatalf("clear: %v\n%s", err, buf.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, stat err=%v", err)
	}
}

func TestSaveDispatchRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "soldier-dispatch.json")
	cfg := &harness.DispatchConfig{
		DefaultHarness: "pi",
		DefaultModel:   "flash",
		DefaultEffort:  "low",
		Profiles: []harness.DispatchProfile{
			{Name: "hard", When: "deep architectural redesign", Harness: "pi", Model: "glm", Effort: "high"},
		},
	}
	if err := harness.SaveDispatch(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := harness.LoadDispatch(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultHarness != "pi" || got.Profiles[0].Name != "hard" {
		t.Fatalf("got %+v", got)
	}
	// when-only profile should still match via phrase
	sel := harness.ResolveDispatchSelection(got, "please do deep architectural redesign")
	if sel.Model != "glm" || sel.Effort != "high" {
		t.Fatalf("selection = %+v", sel)
	}
}
