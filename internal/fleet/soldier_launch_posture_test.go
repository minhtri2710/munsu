package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
)

// isolateHuman points the Human's home at an empty temp dir, so a pi agent dir
// built for a launch never reads the developer's real ~/.pi/agent.
func isolateHuman(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("PI_CODING_AGENT_DIR", "")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestBuildLaunchArgsRefusesAHarnessThatCannotDenyItsQuestionTool(t *testing.T) {
	adapter, ok := harness.GetAdapter(harness.Pi)
	if !ok || len(adapter.QuestionDeny) == 0 {
		t.Fatalf("pi adapter = %+v, want a question deny", adapter)
	}
	_, args, err := BuildLaunchArgs(t.TempDir(), harness.Pi, "", "", "prompt")
	if err != nil {
		t.Fatalf("BuildLaunchArgs with the deny: %v", err)
	}
	if !harness.PostureOf(harness.Pi, args).QuestionDeny {
		t.Fatalf("argv %q carries no question deny", args)
	}

	stripped := adapter
	stripped.QuestionDeny = nil
	harness.Adapters[harness.Pi] = stripped
	t.Cleanup(func() { harness.Adapters[harness.Pi] = adapter })
	_, _, err = BuildLaunchArgs(t.TempDir(), harness.Pi, "", "", "prompt")
	if err == nil || !strings.Contains(err.Error(), "cannot deny its ask-the-user tool") {
		t.Fatalf("BuildLaunchArgs error = %v, want the question-deny refusal", err)
	}
}

func TestLaunchArtifactPostureAndPiAgentDir(t *testing.T) {
	isolateHuman(t)
	for _, tc := range []struct {
		name string
		bin  string
		args []string
		want harness.LaunchPosture
	}{
		{"pi with the deny", harness.Pi, []string{"--exclude-tools", "ask_user_question", "prompt"}, harness.LaunchPosture{QuestionDeny: true, SkillBlock: true}},
		{"pi without the deny", harness.Pi, []string{"prompt"}, harness.LaunchPosture{SkillBlock: true}},
		{"claude with both denies", harness.Claude, []string{"--disallowedTools", "AskUserQuestion", "--disallowedTools", "Skill(munsu-ops)", "--", "prompt"}, harness.LaunchPosture{QuestionDeny: true, SkillBlock: true}},
		{"another binary", "/bin/true", []string{"prompt"}, harness.LaunchPosture{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := launchArtifactInputForGuards(t)
			in.LaunchBin, in.LaunchArgs = tc.bin, tc.args
			art, err := buildLaunchArtifact(in)
			if err != nil {
				t.Fatalf("buildLaunchArtifact: %v", err)
			}
			if art.Posture != tc.want {
				t.Fatalf("Posture = %+v, want %+v", art.Posture, tc.want)
			}
			script := readFile(t, art.ScriptPath)
			agentDir, err := piAgentDirPath(in.HomeDir, in.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			export := "export PI_CODING_AGENT_DIR=" + shQuote(agentDir)
			settings := filepath.Join(in.WorktreePath, filepath.FromSlash(PiSettingsName))
			if tc.bin == harness.Pi {
				if !strings.Contains(script, export) {
					t.Fatalf("script does not export the per-task pi agent dir %q:\n%s", agentDir, script)
				}
				if _, err := os.Stat(filepath.Join(agentDir, "settings.json")); err != nil {
					t.Fatalf("per-task agent dir has no settings copy: %v", err)
				}
				if got := readFile(t, settings); got != string(harness.PiProjectSettings()) {
					t.Fatalf("worktree %s = %q, want the skill block", PiSettingsName, got)
				}
				return
			}
			if strings.Contains(script, "PI_CODING_AGENT_DIR") {
				t.Fatalf("a non-pi launch exports a pi agent dir:\n%s", script)
			}
			if _, err := os.Stat(settings); !os.IsNotExist(err) {
				t.Fatalf("a non-pi launch wrote the worktree pi settings (stat err = %v)", err)
			}
		})
	}
}

func TestLaunchScriptTurnsOffGitBackgroundMaintenance(t *testing.T) {
	isolateHuman(t)
	art, err := buildLaunchArtifact(launchArtifactInputForGuards(t))
	if err != nil {
		t.Fatal(err)
	}
	var export string
	for _, line := range strings.Split(readFile(t, art.ScriptPath), "\n") {
		if strings.HasPrefix(line, "export GIT_CONFIG_COUNT=") {
			export = line
		}
	}
	if export == "" {
		t.Fatal("launch script does not export GIT_CONFIG_COUNT")
	}
	pairs := map[string]string{}
	for _, m := range regexp.MustCompile(`GIT_CONFIG_KEY_(\d+)=(\S+) GIT_CONFIG_VALUE_(\d+)=(\S+)`).FindAllStringSubmatch(export, -1) {
		if m[1] != m[3] {
			t.Fatalf("key %s is paired with value %s in %q", m[1], m[3], export)
		}
		pairs[m[2]] = m[4]
	}
	if !strings.HasPrefix(export, "export GIT_CONFIG_COUNT=2 ") || len(pairs) != 2 || pairs["maintenance.auto"] != "false" || pairs["gc.auto"] != "0" {
		t.Fatalf("git config export = %q (pairs %v), want count 2 with maintenance.auto=false and gc.auto=0", export, pairs)
	}
}

func TestReviewLaunchExportsTheVerdictFileAndNeverWritesTheCheckout(t *testing.T) {
	isolateHuman(t)
	newInput := func(t *testing.T) LaunchArtifactInput {
		in := launchArtifactInputForGuards(t)
		in.LaunchDir = t.TempDir()
		in.Review = &ReviewLaunch{
			VerdictFile: filepath.Join(in.LaunchDir, "verdict.json"),
			Before:      domain.TreeState{Head: strings.Repeat("a", 40), Porcelain: strings.Repeat("b", 64)},
		}
		return in
	}

	t.Run("checkout without the settings file is refused and left untouched", func(t *testing.T) {
		in := newInput(t)
		_, err := buildLaunchArtifact(in)
		if err == nil || !strings.Contains(err.Error(), "a reviewer never writes the checkout it reads") {
			t.Fatalf("buildLaunchArtifact error = %v, want the reviewer no-write refusal", err)
		}
		entries, readErr := os.ReadDir(in.WorktreePath)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("reviewed checkout = %v, %v; want it untouched", entries, readErr)
		}
	})

	t.Run("checkout with the identical settings file is launched", func(t *testing.T) {
		in := newInput(t)
		writePiSettingsInWorktree(t, in.WorktreePath, harness.PiProjectSettings())
		art, err := buildLaunchArtifact(in)
		if err != nil {
			t.Fatalf("buildLaunchArtifact: %v", err)
		}
		script := readFile(t, art.ScriptPath)
		if !strings.Contains(script, "export MUNSU_VERDICT_FILE="+shQuote(in.Review.VerdictFile)) {
			t.Fatalf("script does not export the verdict file:\n%s", script)
		}
		if filepath.Dir(art.ScriptPath) != in.LaunchDir {
			t.Fatalf("script %s is not in the launch dir %s", art.ScriptPath, in.LaunchDir)
		}
	})

	t.Run("a soldier launch exports no verdict file", func(t *testing.T) {
		art, err := buildLaunchArtifact(launchArtifactInputForGuards(t))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(readFile(t, art.ScriptPath), "MUNSU_VERDICT_FILE") {
			t.Fatal("a soldier launch script exports a verdict file")
		}
	})
}

func writePiSettingsInWorktree(t *testing.T, worktree string, content []byte) {
	t.Helper()
	path := filepath.Join(worktree, filepath.FromSlash(PiSettingsName))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitInWorktree(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestWritePiProjectSettings(t *testing.T) {
	want := string(harness.PiProjectSettings())
	path := func(wt string) string { return filepath.Join(wt, filepath.FromSlash(PiSettingsName)) }

	t.Run("absent file is created and re-entry keeps it", func(t *testing.T) {
		wt := t.TempDir()
		if err := writePiProjectSettings(wt, true); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, path(wt)); got != want {
			t.Fatalf("settings = %q, want %q", got, want)
		}
		before, err := os.Stat(path(wt))
		if err != nil {
			t.Fatal(err)
		}
		if err := writePiProjectSettings(wt, true); err != nil {
			t.Fatalf("re-entry: %v", err)
		}
		after, err := os.Stat(path(wt))
		if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
			t.Fatalf("re-entry rewrote the file: %v %v %v", before, after, err)
		}
	})

	refusals := []struct {
		name    string
		prepare func(t *testing.T, wt string)
		want    string
	}{
		{"different content", func(t *testing.T, wt string) { writePiSettingsInWorktree(t, wt, []byte("{}\n")) }, "cannot be expressed without changing the project's file"},
		{"directory in place", func(t *testing.T, wt string) {
			if err := os.MkdirAll(path(wt), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "cannot be expressed without changing the project's file"},
		{"tracked with identical content", func(t *testing.T, wt string) {
			writePiSettingsInWorktree(t, wt, harness.PiProjectSettings())
			gitInWorktree(t, wt, "init", "-q", ".")
			gitInWorktree(t, wt, "add", PiSettingsName)
		}, "cannot be expressed without changing the project's file"},
		{"parent is a file", func(t *testing.T, wt string) {
			if err := os.WriteFile(filepath.Join(wt, ".pi"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "checking"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			wt := t.TempDir()
			tc.prepare(t, wt)
			err := writePiProjectSettings(wt, true)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("writePiProjectSettings error = %v, want %q", err, tc.want)
			}
		})
	}

	t.Run("symlink in place", func(t *testing.T) {
		wt := t.TempDir()
		target := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(target, harness.PiProjectSettings(), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path(wt)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path(wt)); err != nil {
			t.Fatal(err)
		}
		if err := writePiProjectSettings(wt, true); err == nil || !strings.Contains(err.Error(), "cannot be expressed") {
			t.Fatalf("writePiProjectSettings error = %v, want the refusal for a symlinked file", err)
		}
	})

	t.Run("create false never writes", func(t *testing.T) {
		wt := t.TempDir()
		if err := writePiProjectSettings(wt, false); err == nil || !strings.Contains(err.Error(), "a reviewer never writes the checkout it reads") {
			t.Fatalf("writePiProjectSettings error = %v, want the no-write refusal", err)
		}
		if _, err := os.Stat(path(wt)); !os.IsNotExist(err) {
			t.Fatalf("create=false wrote the file (stat err = %v)", err)
		}
	})
}

func TestProvisionPiSkillBlockRefusals(t *testing.T) {
	isolateHuman(t)
	wt := t.TempDir()
	if _, err := provisionPiSkillBlock(t.TempDir(), "a/b", wt, true); err == nil || !strings.Contains(err.Error(), `soldier launch: invalid task ID "a/b"`) {
		t.Fatalf("invalid task id error = %v, want the launch refusal", err)
	}
	fileHome := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileHome, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := provisionPiSkillBlock(fileHome, "t1", wt, true); err == nil || !strings.Contains(err.Error(), "soldier launch: mkdir") {
		t.Fatalf("file home error = %v, want the launch refusal", err)
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := provisionPiSkillBlock(t.TempDir(), "t1", wt, true); err == nil || !strings.Contains(err.Error(), "the Human's pi agent dir") {
		t.Fatalf("no human home error = %v, want the agent dir refusal", err)
	}
}
