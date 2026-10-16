//go:build darwin

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fence"
)

func launchFenceForTest(t *testing.T) *fence.Fence {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{
		"home": filepath.Join(root, "home"), "primary": filepath.Join(root, "primary"), "worktree": filepath.Join(root, "wt"),
		"common": filepath.Join(root, "primary", ".git"), "gitdir": filepath.Join(root, "primary", ".git", "worktrees", "wt"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dirs["home"], "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := fence.New(fence.Launch{
		Role: fence.RoleSoldier, Harness: "pi", Home: dirs["home"], Primary: dirs["primary"], Worktree: dirs["worktree"],
		GitDir: dirs["gitdir"], CommonDir: dirs["common"], Branch: "mu/t",
		UserHome: testSeatEnv().UserHome,
	})
	if err != nil {
		t.Fatalf("fence.New: %v", err)
	}
	return f
}

func TestBuildLaunchArtifactExecsTheHarnessUnderTheFence(t *testing.T) {
	in := launchArtifactInputForGuards(t)
	in.Fence = launchFenceForTest(t)
	art, err := buildLaunchArtifact(in)
	if err != nil {
		t.Fatalf("buildLaunchArtifact: %v", err)
	}
	script, err := os.ReadFile(filepath.Join(in.LaunchDir, LaunchScriptName))
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := in.Fence.Wrap(append([]string{in.LaunchBin}, in.LaunchArgs...))
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	want.WriteString("exec")
	for _, arg := range wrapped {
		want.WriteString(" " + shQuote(arg))
	}
	if !strings.Contains(string(script), want.String()+"\n") {
		t.Fatalf("launch script does not exec the harness under the fence; want line %q in:\n%s", want.String(), script)
	}
	if !strings.Contains(art.Command, LaunchScriptName) {
		t.Fatalf("submission command = %q, want it to run the script", art.Command)
	}
	if strings.Index(string(script), "\nexec ") < strings.Index(string(script), "printf '%s' \"$identity\"") {
		t.Fatal("the fenced exec precedes the launch guard, which must run unfenced before it")
	}
}

func TestBuildLaunchArtifactRefusesAnArgvTheFenceCannotWrap(t *testing.T) {
	in := launchArtifactInputForGuards(t)
	in.Fence = launchFenceForTest(t)
	in.LaunchBin = "-x"
	if _, err := buildLaunchArtifact(in); err == nil || !strings.Contains(err.Error(), "soldier launch: fence: cannot wrap argv") {
		t.Fatalf("buildLaunchArtifact error = %v, want the wrap refusal", err)
	}
	if _, err := os.Stat(filepath.Join(in.LaunchDir, LaunchScriptName)); !os.IsNotExist(err) {
		t.Fatalf("a launch script was written for an argv the fence cannot wrap: %v", err)
	}
}

// A fenced claude seat runs from its own config dir and logs in with the
// Human's keychain item; an unfenced one keeps the Human's config.
func TestFencedClaudeLaunchRunsFromItsOwnConfigDir(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fenced bool
	}{
		{"a fenced claude launch exports its own config dir", true},
		{"an unfenced claude launch exports none", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := launchArtifactInputForGuards(t)
			in.LaunchBin = "claude"
			if tc.fenced {
				in.Fence = launchFenceForTest(t)
			}
			dir, err := claudeConfigDirPath(in.HomeDir, in.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			leftover := plantSeatBuildLeftover(t, in.HomeDir, dir)
			art, err := buildLaunchArtifact(in)
			if err != nil {
				t.Fatalf("buildLaunchArtifact: %v", err)
			}
			script := readFile(t, art.ScriptPath)
			want := "export CLAUDE_CONFIG_DIR=" + shQuote(dir) + "\nexport CLAUDE_SECURESTORAGE_CONFIG_DIR=''\n"
			_, statErr := os.Stat(filepath.Join(dir, ".claude.json"))
			if !tc.fenced {
				if strings.Contains(script, "CLAUDE_") || !os.IsNotExist(statErr) {
					t.Fatalf("an unfenced launch got a seat config dir (stat err = %v):\n%s", statErr, script)
				}
				return
			}
			if !strings.Contains(script, want) || statErr != nil {
				t.Fatalf("script lacks %q or the dir was not built (stat err = %v):\n%s", want, statErr, script)
			}
			if strings.Index(script, want) > strings.Index(script, "\nexec ") {
				t.Fatal("the config dir is exported after the harness exec")
			}
			requireBuiltInSeatBuildDir(t, leftover, dir)
		})
	}
}
