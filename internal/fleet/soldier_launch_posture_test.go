package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A soldier launches only through a verified harness with a prompt-arg
// contract and an expressible question deny.
func TestBuildLaunchArgsRefusals(t *testing.T) {
	cases := []struct {
		name    string
		harness string
		mutate  func(*harness.Adapter)
		want    string // refusal text; empty means allowed
	}{
		{name: "unknown harness refused", harness: "unknown_harness", want: "is not a verified harness"},
		{name: "harness without a prompt-arg contract refused", harness: harness.Codex, want: "does not have a verified prompt-arg contract"},
		{
			name:    "harness that cannot deny its question tool refused",
			harness: harness.Pi,
			mutate:  func(a *harness.Adapter) { a.QuestionDeny = nil },
			want:    "cannot deny its ask-the-user tool",
		},
		{name: "verified harness with the deny allowed", harness: harness.Pi},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mutate != nil {
				original := harness.Adapters[tc.harness]
				adapter := original
				tc.mutate(&adapter)
				harness.Adapters[tc.harness] = adapter
				t.Cleanup(func() { harness.Adapters[tc.harness] = original })
			}

			_, args, err := BuildLaunchArgs(t.TempDir(), tc.harness, "", "", "prompt")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("BuildLaunchArgs: %v", err)
				}
				if !harness.PostureOf(tc.harness, args).QuestionDeny {
					t.Fatalf("argv %q carries no question deny", args)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("BuildLaunchArgs error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLaunchArtifactPostureAndPiAgentDir(t *testing.T) {
	for _, tc := range []struct {
		name string
		bin  string
		args []string
		want harness.LaunchPosture
	}{
		{"pi with the deny", harness.Pi, []string{"--exclude-tools", "ask_user_question", "prompt"}, harness.LaunchPosture{QuestionDeny: true, SkillBlock: true}},
		{"pi without the deny", harness.Pi, []string{"prompt"}, harness.LaunchPosture{SkillBlock: true}},
		{"another binary", "/bin/true", []string{"prompt"}, harness.LaunchPosture{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := launchArtifactInputForGuards(t)
			in.LaunchBin, in.LaunchArgs = tc.bin, tc.args
			agentDir, err := piAgentDirPath(in.HomeDir, in.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			leftover := plantSeatBuildLeftover(t, in.HomeDir, agentDir)
			art, err := buildLaunchArtifact(in)
			if err != nil {
				t.Fatalf("buildLaunchArtifact: %v", err)
			}
			if art.Posture != tc.want {
				t.Fatalf("Posture = %+v, want %+v", art.Posture, tc.want)
			}
			script := readFile(t, art.ScriptPath)
			export := "export PI_CODING_AGENT_DIR=" + shQuote(agentDir)
			settings := filepath.Join(in.WorktreePath, filepath.FromSlash(PiSettingsName))
			if tc.want.SkillBlock {
				if !strings.Contains(script, export) {
					t.Fatalf("script does not export the per-task pi agent dir %q:\n%s", agentDir, script)
				}
				if _, err := os.Stat(filepath.Join(agentDir, "settings.json")); err != nil {
					t.Fatalf("per-task agent dir has no settings copy: %v", err)
				}
				if got := readFile(t, settings); got != string(harness.PiProjectSettings()) {
					t.Fatalf("worktree %s = %q, want the skill block", PiSettingsName, got)
				}
				requireBuiltInSeatBuildDir(t, leftover, agentDir)
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

// plantSeatBuildLeftover leaves a stale crashed build of dst in the home's seat
// build dir, where a launch must build dst, and returns its path.
func plantSeatBuildLeftover(t *testing.T, homeDir, dst string) string {
	t.Helper()
	leftover := filepath.Join(harness.SeatBuildDir(homeDir), filepath.Base(dst)+"-crashed")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(leftover, old, old); err != nil {
		t.Fatal(err)
	}
	return leftover
}

// requireBuiltInSeatBuildDir requires that dst was built in the seat build
// dir: the build there removed the planted stale leftover.
func requireBuiltInSeatBuildDir(t *testing.T, leftover, dst string) {
	t.Helper()
	if _, err := os.Lstat(leftover); !os.IsNotExist(err) {
		t.Fatalf("%s survived the launch of %s, so the build ran elsewhere: %v", leftover, dst, err)
	}
}

func TestLaunchScriptTurnsOffGitBackgroundMaintenance(t *testing.T) {
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

func TestPrepareLaunchFilesRefusesUnsupportedHarnessWorktreeFile(t *testing.T) {
	original := harness.Adapters[harness.Pi]
	modified := original
	modified.SoldierLaunch.WorktreeFiles = []string{"unsupported.json"}
	harness.Adapters[harness.Pi] = modified
	t.Cleanup(func() { harness.Adapters[harness.Pi] = original })

	_, err := prepareLaunchFiles("charter", nil, &LaunchEnvelope{}, "prompt", []byte("script"), harness.Pi, ".soldier-launch-guard-test-1", "launch|1|fence")
	if err == nil || !strings.Contains(err.Error(), `unsupported prepared launch file "unsupported.json"`) {
		t.Fatalf("prepareLaunchFiles error = %v, want unsupported harness file refusal", err)
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
	if _, err := provisionPiSkillBlock(t.TempDir(), "t1", wt, true); err == nil || !strings.Contains(err.Error(), "the Human's pi agent dir") {
		t.Fatalf("no human home error = %v, want the agent dir refusal", err)
	}
}

// A seat's tool caches resolve under its own temp dir and never under the
// Human's home: the script, run for real, exports them and the child sees them.
func TestLaunchScriptRedirectsToolCachesIntoTheSeatTemp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	human := t.TempDir()
	t.Setenv("HOME", human)
	wt := t.TempDir()
	out := filepath.Join(t.TempDir(), "env")
	artifact, err := buildLaunchArtifact(LaunchArtifactInput{
		WorktreePath: wt, LaunchDir: wt, HomeDir: human, PathDirs: seatPathDirs(human), TaskID: "ship-cache", SnapshotDigest: "digest",
		HomeID: "test-home", SeatEnv: testSeatEnv(),
		LaunchBin: "/bin/sh", LaunchArgs: []string{"-c", "env > " + out}, LaunchID: "lid", Generation: "1", EndpointFence: "fence",
	})
	if err != nil {
		t.Fatalf("buildLaunchArtifact: %v", err)
	}
	if msg, err := exec.Command("/bin/bash", artifact.ScriptPath).CombinedOutput(); err != nil {
		t.Fatalf("running the launch script: %v\n%s", err, msg)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	seat := seatTempDir(tmp, "test-home", "ship-cache", "1")
	want := map[string]string{
		"npm_config_cache": "npm-cache", "npm_config_store_dir": "npm-store",
		"PIP_CACHE_DIR": "pip-cache", "UV_CACHE_DIR": "uv-cache", "XDG_CACHE_HOME": "xdg-cache",
		"GOCACHE": "go-build", "GOMODCACHE": "go-mod", "GOPATH": "go-path", "GOTELEMETRYDIR": "go-telemetry",
	}
	for k, sub := range want {
		if got := env[k]; got != filepath.Join(seat, sub) {
			t.Errorf("%s = %q, want %q", k, got, filepath.Join(seat, sub))
		}
	}
	for k, v := range env {
		if strings.HasPrefix(v, human) && strings.Contains(k, "CACHE") {
			t.Errorf("%s = %q points into the Human's home", k, v)
		}
	}
}
