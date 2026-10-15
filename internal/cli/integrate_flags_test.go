package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

// TestIntegrateHarnessScopeAndDryRunFlags drives every integrate install,
// repair and status flag through cobra parsing, so a flag that is registered but
// wired to nothing fails a row. Without --harness every subcommand uses the
// configured soldier-harness and refuses when none is configured, even where the
// environment names a harness; --harness overrides that refusal. A --scope other
// than user or project is refused. The install and repair allows rows use codex
// with a stub munsu on PATH: a dry run then needs no live tool, unlike pi, whose
// capability probe needs the pi binary, node and the installed Pi package. Rows
// run from a scratch directory so a broken --dry-run cannot write project-scope
// artifacts into the checkout.
func TestIntegrateHarnessScopeAndDryRunFlags(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		configured string
		wantErr    string
		wantOut    string
	}{
		{name: "install with none configured blocks", args: []string{"integrate", "install", "--dry-run"}, wantErr: "munsu config set soldier-harness"},
		{name: "repair with none configured blocks", args: []string{"integrate", "repair", "--dry-run"}, wantErr: "munsu config set soldier-harness"},
		{name: "status with none configured blocks", args: []string{"integrate", "status"}, wantErr: "munsu config set soldier-harness"},
		{name: "install with soldier-harness configured allows", args: []string{"integrate", "install", "--dry-run"}, configured: "codex", wantOut: "harness: codex"},
		{name: "repair with soldier-harness configured allows", args: []string{"integrate", "repair", "--dry-run"}, configured: "codex", wantOut: "harness: codex"},
		{name: "status with soldier-harness configured allows", args: []string{"integrate", "status"}, configured: "pi", wantOut: "harness: pi"},
		{name: "install --harness with none configured allows", args: []string{"integrate", "install", "--dry-run", "--harness", "codex"}, wantOut: "harness: codex"},
		{name: "repair --harness with none configured allows", args: []string{"integrate", "repair", "--dry-run", "--harness", "codex"}, wantOut: "harness: codex"},
		{name: "install --dry-run reports without writing", args: []string{"integrate", "install", "--dry-run"}, configured: "codex", wantOut: "[dry-run] would write"},
		{name: "repair --dry-run reports without writing", args: []string{"integrate", "repair", "--dry-run"}, configured: "codex", wantOut: "[dry-run] drift would be corrected"},
		{name: "install --scope outside user and project blocks", args: []string{"integrate", "install", "--dry-run", "--scope", "bogus"}, configured: "codex", wantErr: `unsupported scope "bogus"`},
		{name: "install --scope project allows", args: []string{"integrate", "install", "--dry-run", "--scope", "project"}, configured: "codex", wantOut: "harness: codex"},
		{name: "repair --scope outside user and project blocks", args: []string{"integrate", "repair", "--dry-run", "--scope", "bogus"}, configured: "codex", wantErr: `unsupported scope "bogus"`},
		{name: "repair --scope project allows", args: []string{"integrate", "repair", "--dry-run", "--scope", "project"}, configured: "codex", wantOut: "harness: codex"},
		{name: "status --scope outside user and project blocks", args: []string{"integrate", "status", "--scope", "bogus"}, configured: "pi", wantErr: `unsupported scope "bogus"`},
		{name: "status --scope project allows", args: []string{"integrate", "status", "--scope", "project"}, configured: "pi", wantOut: "harness: pi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			t.Setenv("MUNSU_HOME", homeDir)
			t.Setenv("CLAUDECODE", "1")
			t.Setenv("HOME", t.TempDir())
			t.Chdir(t.TempDir())
			// The hook installers resolve munsu from PATH; a stub makes that independent of the box.
			stubDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(stubDir, "munsu"), nil, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{
				SchemaVersion: config.FleetBaseSchemaVersion,
				Config:        config.FleetBaseConfig{SoldierHarness: tc.configured},
			}); err != nil {
				t.Fatal(err)
			}

			root := NewRootCommand()
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs(tc.args)
			err := root.Execute()
			out := buf.String()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("%v = (%q, %v), want error containing %q", tc.args, out, err, tc.wantErr)
				}
				return
			}
			if err != nil || !strings.Contains(out, tc.wantOut) {
				t.Fatalf("%v = (%q, %v), want output containing %q", tc.args, out, err, tc.wantOut)
			}
		})
	}
}
