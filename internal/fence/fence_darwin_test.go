//go:build darwin

package fence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

// outsideDir returns a fresh directory under the real user home: neither a temp
// root nor a dev path, so only a profile rule can make it writable.
func outsideDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(home, ".fence-outside-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// seatEnv points the user home and the Go cache roots at dirs under out and
// creates them, so the harness-state and cache allows are observable.
func seatEnv(t *testing.T, out string) (userHome string) {
	t.Helper()
	userHome = filepath.Join(out, "uhome")
	t.Setenv("HOME", userHome)
	t.Setenv("GOCACHE", filepath.Join(out, "gocache"))
	t.Setenv("GOMODCACHE", filepath.Join(out, "gomod"))
	t.Setenv("GOPATH", filepath.Join(out, "gopath"))
	t.Setenv("GOTELEMETRYDIR", filepath.Join(out, "gotel"))
	for _, d := range []string{
		filepath.Join(userHome, ".claude"), filepath.Join(userHome, "Library", "Caches", "claude-cli-nodejs"),
		filepath.Join(userHome, ".pi", "agent"), filepath.Join(userHome, "other"),
		filepath.Join(out, "gocache"), filepath.Join(out, "gomod"), filepath.Join(out, "gopath", "pkg"), filepath.Join(out, "gotel"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return userHome
}

func mustNew(t *testing.T, l Launch) *Fence {
	t.Helper()
	f, err := New(l)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return f
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// fenced runs argv under the fence and returns the combined output and error.
func fenced(t *testing.T, f *Fence, argv ...string) (string, error) {
	t.Helper()
	wrapped, err := f.Wrap(argv)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(wrapped[0], wrapped[1:]...).CombinedOutput()
	return string(out), err
}

func mustWrite(t *testing.T, f *Fence, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if out, err := fenced(t, f, touchBin, p); err != nil {
			t.Errorf("write %s refused: %v %s", p, err, out)
		}
		os.Remove(p)
	}
}

// mustRefuse requires the sandbox's own refusal, so a missing parent directory
// cannot pass for one.
func mustRefuse(t *testing.T, f *Fence, paths ...string) {
	t.Helper()
	for _, p := range paths {
		out, err := fenced(t, f, touchBin, p)
		if err == nil || !strings.Contains(out, "Operation not permitted") {
			t.Errorf("write %s was not refused by the fence: %v %s", p, err, out)
		}
		if _, statErr := os.Lstat(p); statErr == nil {
			t.Errorf("%s exists after a write the fence must refuse", p)
			os.Remove(p)
		}
	}
}

func TestSoldierFenceWritableSet(t *testing.T) {
	out := outsideDir(t)
	uhome := seatEnv(t, out)
	l := newSplitLayout(t, out)
	verdict := filepath.Join(out, "verdict.json")
	gateRepo := filepath.Join(out, "gate", "repo.git")
	gateState := filepath.Join(out, "gate", "state.db")
	mkdirs(t, filepath.Join(gateRepo, "sub"), filepath.Join(l.common, "objects"), filepath.Join(l.common, "hooks"),
		filepath.Join(l.common, "refs", "remotes"), filepath.Join(l.common, "refs", "heads"),
		filepath.Join(l.common, "logs", "refs", "remotes"), filepath.Join(l.common, "logs", "refs", "heads"), filepath.Join(out, "wt2"))
	launch := l.soldier()
	launch.Files = []string{verdict}
	launch.GateRepo, launch.GateState = gateRepo, gateState
	f := mustNew(t, launch)

	mustWrite(t, f,
		filepath.Join(l.worktree, "f"),
		filepath.Join(l.gitDir, "index.lock"),
		filepath.Join(l.common, "objects", "f"),
		filepath.Join(l.common, "refs", "remotes", "f"),
		filepath.Join(l.common, "logs", "refs", "remotes", "f"),
		filepath.Join(l.home, "state", "f"), filepath.Join(l.home, "data", "f"),
		filepath.Join(l.home, ".journal", "f"), filepath.Join(l.home, ".lock", "f"),
		filepath.Join(uhome, ".claude", "f"), filepath.Join(uhome, "Library", "Caches", "claude-cli-nodejs", "f"),
		filepath.Join(uhome, ".claude.json"), filepath.Join(uhome, ".claude.json.tmp.123.abc1"),
		verdict, verdict+".tmp.9.ff",
		filepath.Join(gateRepo, "sub", "f"), filepath.Join(gateRepo, "f"), gateState, gateState+"-wal", gateState+"-shm", gateState+"-journal",
		filepath.Join(out, "gocache", "f"), filepath.Join(out, "gomod", "f"),
		filepath.Join(out, "gopath", "pkg", "f"), filepath.Join(out, "gotel", "f"),
		filepath.Join(l.root, "temp-elsewhere-f"),
	)
	mustRefuse(t, f,
		filepath.Join(l.primary, "f"),
		filepath.Join(l.common, "HEAD"), filepath.Join(l.common, "config"), filepath.Join(l.common, "packed-refs"),
		filepath.Join(l.common, "hooks", "f"), filepath.Join(l.common, "refs", "heads", "main"),
		filepath.Join(l.common, "logs", "HEAD"),
		filepath.Join(l.gitDir, "config.worktree"),
		filepath.Join(l.home, "f"),
		filepath.Join(uhome, "other", "f"), filepath.Join(uhome, "Library", "Caches", "f"), filepath.Join(uhome, ".claude.jsonx"), filepath.Join(uhome, ".claude.json.tmpX"),
		filepath.Join(out, "wt2", "f"), filepath.Join(out, "f"),
		verdict+"2", verdict+".tmp.abc", verdict+".tmp.1.xyz",
		gateState+"-x", filepath.Join(out, "gate", "f"),
	)
	if t.Failed() {
		return
	}

	// Only the task's own branch and the dirs git creates for its slashed name
	// are writable in the common dir.
	if o, err := fenced(t, f, "/bin/mkdir", filepath.Join(l.common, "refs", "heads", "mu")); err != nil {
		t.Fatalf("creating the branch namespace dir: %v %s", err, o)
	}
	if o, err := fenced(t, f, "/bin/mkdir", filepath.Join(l.common, "logs", "refs", "heads", "mu")); err != nil {
		t.Fatalf("creating the branch log namespace dir: %v %s", err, o)
	}
	for _, tree := range []string{"refs", filepath.Join("logs", "refs")} {
		dir := filepath.Join(l.common, tree, "heads", "mu")
		mustWrite(t, f, filepath.Join(dir, "T-1"), filepath.Join(dir, "T-1.lock"))
		mustRefuse(t, f, filepath.Join(dir, "other"), filepath.Join(dir, "T-2"), filepath.Join(dir, "T-1x"))
	}
	if _, err := fenced(t, f, "/bin/mkdir", filepath.Join(l.common, "refs", "heads", "rogue")); err == nil {
		t.Error("a namespace dir other than the task's branch was created")
	}
}

func TestSoldierFenceWithPiStateDir(t *testing.T) {
	out := outsideDir(t)
	uhome := seatEnv(t, out)
	l := newSplitLayout(t, out)
	launch := l.soldier()
	launch.Harness = harness.Pi
	launch.HarnessStateDir = l.stateDir()
	mkdirs(t, launch.HarnessStateDir)
	f := mustNew(t, launch)
	mustWrite(t, f, filepath.Join(launch.HarnessStateDir, "f"), filepath.Join(uhome, ".pi", "agent", "f"))
	mustRefuse(t, f, filepath.Join(uhome, ".claude", "f"), filepath.Join(uhome, ".claude.json"))
}

func TestReviewerFenceWritesOnlySeatStateTempAndItsFiles(t *testing.T) {
	out := outsideDir(t)
	uhome := seatEnv(t, out)
	l := newLayout(t)
	verdict := filepath.Join(out, "verdict.json")
	launch := l.reviewer()
	launch.Harness = harness.Pi
	launch.HarnessStateDir = l.stateDir()
	launch.Files = []string{verdict}
	// A git dir apart from the common dir and the worktree is protected on its own.
	launch.GitDir = filepath.Join(l.root, "gitdir-apart")
	mkdirs(t, launch.HarnessStateDir, launch.GitDir)
	f := mustNew(t, launch)

	mustWrite(t, f,
		filepath.Join(launch.HarnessStateDir, "f"), filepath.Join(uhome, ".pi", "agent", "f"),
		verdict, verdict+".tmp.9.ff", filepath.Join(out, "gocache", "f"),
		filepath.Join(l.root, "temp-elsewhere-f"),
	)
	// These sit under a temp root, which is writable; only the reviewer's
	// explicit denies refuse them.
	mustRefuse(t, f,
		filepath.Join(l.worktree, "f"), filepath.Join(launch.GitDir, "f"), filepath.Join(l.primary, "f"),
		filepath.Join(l.common, "HEAD"),
		filepath.Join(l.home, "state", "f"), filepath.Join(l.home, "data", "f"),
		filepath.Join(l.home, ".journal", "f"), filepath.Join(l.home, ".lock", "f"),
		filepath.Join(uhome, "other", "f"), verdict+"2",
	)
}

func TestProbeRecordsTheControlAndEveryRefusedWrite(t *testing.T) {
	out := outsideDir(t)
	seatEnv(t, out)
	l := newLayout(t)
	tests := []struct {
		name    string
		launch  Launch
		refused []string
	}{
		{"soldier", l.soldier(), []string{l.primary, l.common}},
		{"reviewer", l.reviewer(), []string{l.primary, l.common, l.worktree, l.gitDir, filepath.Join(l.home, "state")}},
		{"home-less reviewer", func() Launch { x := l.reviewer(); x.Home = ""; return x }(), []string{l.primary, l.common, l.worktree, l.gitDir}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := mustNew(t, tt.launch)
			ev, err := f.Probe(context.Background())
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			wrapped, _ := f.Wrap([]string{"/bin/true"})
			sum := sha256.Sum256([]byte(wrapped[2]))
			digest := hex.EncodeToString(sum[:])
			if ev.Role != tt.launch.Role || ev.Harness != tt.launch.Harness || ev.ProfileDigest != digest {
				t.Fatalf("evidence header = %s %s %s; want %s %s %s", ev.Role, ev.Harness, ev.ProfileDigest, tt.launch.Role, tt.launch.Harness, digest)
			}
			if len(ev.Steps) != 1+len(tt.refused) {
				t.Fatalf("steps = %d, want the control plus %d refused writes: %+v", len(ev.Steps), len(tt.refused), ev.Steps)
			}
			for i, s := range ev.Steps {
				want := OutcomeRefused
				if i == 0 {
					want = OutcomeAllowed
				}
				if s.Outcome != want || s.Detail != "" {
					t.Errorf("step %d = %+v, want outcome %s and no detail", i, s, want)
				}
				if len(s.Command) != 5 || s.Command[0] != sandboxExec || s.Command[1] != "-p" ||
					s.Command[2] != "profile-sha256:"+digest || s.Command[3] != touchBin {
					t.Errorf("step %d command = %q; want the profile replaced by its digest", i, s.Command)
				}
				if strings.Contains(strings.Join(s.Command, " "), "(deny file-write*)") {
					t.Errorf("step %d command carries the profile text", i)
				}
				if i > 0 && filepath.Dir(s.Command[4]) != tt.refused[i-1] {
					t.Errorf("step %d wrote under %s, want %s", i, filepath.Dir(s.Command[4]), tt.refused[i-1])
				}
			}
		})
	}
}

func TestProbeRemovesItsControlDir(t *testing.T) {
	out := outsideDir(t)
	seatEnv(t, out)
	f := mustNew(t, newLayout(t).soldier())
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if _, err := f.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("the probe left %d entries in its temp root", len(entries))
	}
}

func TestProbeFailureOutcomes(t *testing.T) {
	dir := t.TempDir()
	open := "(version 1)\n(allow default)\n"
	tests := []struct {
		name    string
		f       *Fence
		outcome Outcome
		steps   int
	}{
		{"a profile that refuses nothing is open", &Fence{profile: open, digest: "d", refuse: []string{dir}}, OutcomeWritten, 2},
		{"a profile that does not compile fails the control", &Fence{profile: "(version 1\n(bogus", digest: "d", refuse: []string{dir}}, OutcomeControlFailed, 1},
		{"a profile that refuses the control write fails the control", &Fence{profile: "(version 1)\n(allow default)\n(deny file-write*)\n", digest: "d", refuse: []string{dir}}, OutcomeControlFailed, 1},
		{"a refusal that is not the sandbox's is an error", &Fence{profile: open, digest: "d", refuse: []string{filepath.Join(dir, "missing")}}, OutcomeError, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := tt.f.Probe(context.Background())
			if err == nil || !strings.Contains(err.Error(), "fence probe: "+string(tt.outcome)) {
				t.Fatalf("Probe err = %v, want a %s failure", err, tt.outcome)
			}
			if len(ev.Steps) != tt.steps || ev.Steps[len(ev.Steps)-1].Outcome != tt.outcome {
				t.Fatalf("steps = %+v, want %d ending in %s", ev.Steps, tt.steps, tt.outcome)
			}
			if ev.ProfileDigest != "d" {
				t.Errorf("evidence lost its digest: %+v", ev)
			}
		})
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a written probe file was left behind: %v", entries)
	}
	t.Run("an unusable temp root is an error with no command", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(dir, "no", "such", "tmp"))
		ev, err := (&Fence{profile: open, digest: "d", refuse: []string{dir}}).Probe(context.Background())
		if err == nil || len(ev.Steps) != 1 || ev.Steps[0].Outcome != OutcomeError || ev.Steps[0].Command != nil {
			t.Fatalf("Probe = %+v, %v; want a command-less error step", ev, err)
		}
	})
}

func TestNewRefusals(t *testing.T) {
	out := outsideDir(t)
	seatEnv(t, out)
	l := newLayout(t)
	// h2 holds a primary checkout inside its state root.
	h2 := filepath.Join(l.root, "h2")
	pri := filepath.Join(h2, "state", "pri")
	mkdirs(t, filepath.Join(pri, ".git", "worktrees", "wt"))
	tests := []struct {
		name string
		mut  func(*Launch)
		want string
	}{
		{"an invalid resolved path is refused", func(x *Launch) { x.Primary = "primary" }, "fence: primary checkout: path"},
		{"an unmodeled harness", func(x *Launch) { x.Harness = harness.Codex }, `no state profile for harness "codex"`},
		{"a task branch with a dot-dot", func(x *Launch) { x.Branch = "mu/a..b" }, `invalid task branch "mu/a..b"`},
		{"a file inside the primary checkout", func(x *Launch) { x.Files = []string{filepath.Join(l.primary, "v.json")} }, "overlaps protected path " + l.primary},
		{"a file inside the git common dir", func(x *Launch) { x.Files = []string{filepath.Join(l.common, "v.json")} }, "overlaps protected path"},
		{"a file inside the git dir", func(x *Launch) { x.Files = []string{filepath.Join(l.gitDir, "v.json")} }, "overlaps protected path"},
		{"a home whose state root holds the primary", func(x *Launch) {
			x.Home, x.Primary, x.CommonDir, x.GitDir = h2, pri, filepath.Join(pri, ".git"), filepath.Join(pri, ".git", "worktrees", "wt")
		}, "overlaps protected path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launch := l.soldier()
			tt.mut(&launch)
			if f, err := New(launch); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New = %v, %v; want an error containing %q", f, err, tt.want)
			}
		})
	}
	t.Run("a reviewer may not write a file inside the checkout under review", func(t *testing.T) {
		launch := l.reviewer()
		launch.Files = []string{filepath.Join(l.worktree, "verdict.json")}
		if _, err := New(launch); err == nil || !strings.Contains(err.Error(), "overlaps protected path "+l.worktree) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a reviewer may not write a file inside its git dir", func(t *testing.T) {
		launch := l.reviewer()
		launch.GitDir = filepath.Join(l.root, "gitdir-apart")
		mkdirs(t, launch.GitDir)
		launch.Files = []string{filepath.Join(launch.GitDir, "verdict.json")}
		if _, err := New(launch); err == nil || !strings.Contains(err.Error(), "overlaps protected path "+launch.GitDir) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a soldier may write a file inside its own worktree", func(t *testing.T) {
		launch := l.soldier()
		launch.Files = []string{filepath.Join(l.worktree, "v.json")}
		mustNew(t, launch)
	})
	t.Run("no user home", func(t *testing.T) {
		t.Setenv("HOME", "")
		if _, err := New(l.soldier()); err == nil || !strings.Contains(err.Error(), "fence: resolving user home") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a user home that does not exist", func(t *testing.T) {
		t.Setenv("HOME", filepath.Join(l.root, "no-such-home"))
		if _, err := New(l.soldier()); err == nil || !strings.Contains(err.Error(), "fence: user home:") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestNewBuildsOneProfilePerLaunch(t *testing.T) {
	out := outsideDir(t)
	seatEnv(t, out)
	l := newLayout(t)
	a, b := mustNew(t, l.soldier()), mustNew(t, l.soldier())
	if a.digest != b.digest || a.profile != b.profile {
		t.Fatal("the same launch built two different profiles")
	}
	if r := mustNew(t, l.reviewer()); r.digest == a.digest {
		t.Fatal("a reviewer and a soldier share a profile digest")
	}
}
