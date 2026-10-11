package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

// TestIntegrateSubcommandFlagsRegistered is a regression guard for a bug where
// `munsu integrate install --harness <X>` (and repair) failed with "unknown
// flag: --harness": the --harness/--scope/--dry-run flags were advertised in the
// parent command's help text but never registered on the install/repair
// subcommands (only safety-check registered --harness locally). Unit tests that
// called runIntegrateInstall directly bypassed cobra parsing, so the gap went
// undetected until an end-to-end `integrate install --harness agy` run.
func TestIntegrateSubcommandFlagsRegistered(t *testing.T) {
	root := NewRootCommand()

	for _, sub := range []string{"install", "repair"} {
		cmd, _, err := root.Find([]string{"integrate", sub})
		if err != nil {
			t.Fatalf("Find(integrate %s): %v", sub, err)
		}
		for _, flag := range []string{"harness", "scope", "dry-run"} {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("integrate %s must register --%s (regression: was missing, breaking all adapter installs)", sub, flag)
			}
		}
	}

	// safety-check must still carry --harness (it predated the fix and must not regress).
	sc, _, err := root.Find([]string{"integrate", "safety-check"})
	if err != nil {
		t.Fatalf("Find(integrate safety-check): %v", err)
	}
	if sc.Flags().Lookup("harness") == nil {
		t.Error("integrate safety-check must register --harness")
	}
	// status must register --scope so project-scope installs can be inspected
	// (without it, `integrate status` after `install --scope project` reports absent).
	st, _, err := root.Find([]string{"integrate", "status"})
	if err != nil {
		t.Fatalf("Find(integrate status): %v", err)
	}
	if st.Flags().Lookup("scope") == nil {
		t.Error("integrate status must register --scope")
	}
}

// TestIntegrateWithoutHarnessUsesOnlyTheConfiguredOne: without --harness every
// integrate subcommand uses the configured soldier-harness and refuses when
// none is configured, even where the environment names a harness. The install
// and repair allows rows use codex with a stub munsu on PATH: a dry run then needs
// no live tool, unlike pi, whose capability probe needs the pi binary, node and
// the installed Pi package.
func TestIntegrateWithoutHarnessUsesOnlyTheConfiguredOne(t *testing.T) {
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			t.Setenv("MUNSU_HOME", homeDir)
			t.Setenv("CLAUDECODE", "1")
			t.Setenv("HOME", t.TempDir())
			// The hook installers resolve munsu from PATH; a stub makes that independent of the box.
			stubDir := t.TempDir()
			stubName := "munsu"
			if runtime.GOOS == "windows" {
				stubName += ".exe"
			}
			if err := os.WriteFile(filepath.Join(stubDir, stubName), nil, 0o755); err != nil {
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
