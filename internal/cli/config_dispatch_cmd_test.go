package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
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

	root.SetArgs([]string{"config", "dispatch", "set-default", "pi", "--model", "opencode-go/deepseek-v4-flash"})
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

	base, err := config.LoadFleetBase(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if base.Config.SoldierHarness != "pi" || base.Config.Model != "opencode-go/deepseek-v4-flash" {
		t.Fatalf("default = %+v", base.Config)
	}
	if len(base.Config.DispatchProfiles) != 1 {
		t.Fatalf("profiles = %d, want 1", len(base.Config.DispatchProfiles))
	}
	p := base.Config.DispatchProfiles[0]
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
	base, err = config.LoadFleetBase(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if base.Config.DispatchProfiles[0].Harness != "pi" || base.Config.DispatchProfiles[0].Effort != "medium" {
		t.Fatalf("replaced profile = %+v", base.Config.DispatchProfiles[0])
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
	base, err = config.LoadFleetBase(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Config.DispatchProfiles) != 0 {
		t.Fatalf("after rm profiles=%d", len(base.Config.DispatchProfiles))
	}
}

func TestConfigDispatchClear(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_HOME", tmp)
	os.MkdirAll(filepath.Join(tmp, "config"), 0755)
	base := config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.ProjectOverlay{
			SoldierHarness: "pi",
		},
	}
	if err := config.StoreFleetBase(tmp, base); err != nil {
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
	loaded, err := config.LoadFleetBase(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.SoldierHarness != "" || len(loaded.Config.DispatchProfiles) != 0 {
		t.Fatalf("expected cleared config, got %+v", loaded.Config)
	}
}

func TestDispatchConfig_JSONRoundTrip(t *testing.T) {
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
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	var persisted harness.DispatchConfig
	readData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(readData, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.DefaultHarness != "pi" || persisted.DefaultModel != "flash" || persisted.DefaultEffort != "low" {
		t.Fatalf("persisted config = %+v", persisted)
	}
	sel := harness.ResolveDispatchSelection(&persisted, "please do deep architectural redesign")
	if sel.Model != "glm" || sel.Effort != "high" {
		t.Fatalf("selection = %+v", sel)
	}
}
