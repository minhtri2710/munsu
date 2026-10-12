//go:build darwin

package fence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/harness"
)

func (l layout) claudeStateDir() string { return filepath.Join(l.home, "state", "T-1.claude-config") }

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
	launch.HarnessStateDir = l.claudeStateDir()
	// The seat dir is built as a launch builds it, linking the Human's settings.
	humanSettings := filepath.Join(uhome, ".claude", "settings.json")
	if err := os.WriteFile(humanSettings, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	human := harness.ClaudeConfig{Dir: filepath.Join(uhome, ".claude"), GlobalConfig: filepath.Join(uhome, ".claude.json")}
	if err := harness.BuildClaudeConfigDir(human, harness.SeatBuildDir(l.home), launch.HarnessStateDir); err != nil {
		t.Fatal(err)
	}
	launch.Files = []string{verdict}
	launch.GateRepo, launch.GateState = gateRepo, gateState
	f := mustNew(t, launch)

	mustWrite(t, f,
		filepath.Join(l.worktree, "f"),
		filepath.Join(l.gitDir, "index.lock"),
		filepath.Join(l.common, "objects", "f"),
		filepath.Join(l.common, "refs", "remotes", "f"),
		filepath.Join(l.common, "logs", "refs", "remotes", "f"),
		filepath.Join(launch.HarnessStateDir, "f"), filepath.Join(uhome, "Library", "Caches", "claude-cli-nodejs", "f"),
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
		filepath.Join(l.home, "f"), filepath.Join(harness.SeatBuildDir(l.home), "f"),
		filepath.Join(uhome, "other", "f"), filepath.Join(uhome, "Library", "Caches", "f"),
		filepath.Join(uhome, ".claude", "f"), filepath.Join(uhome, ".claude.json"), filepath.Join(uhome, ".claude.json.tmp.123.abc1"),
		filepath.Join(out, "wt2", "f"), filepath.Join(out, "f"),
		verdict+"2", verdict+".tmp.abc", verdict+".tmp.1.xyz",
		gateState+"-x", filepath.Join(out, "gate", "f"),
	)
	// The seat's settings.json links the Human's file. The fence resolves the
	// link, so a write through it is refused and the Human's file is unchanged.
	if o, err := fenced(t, f, "/bin/sh", "-c", `printf x >> "$1"`, "sh", filepath.Join(launch.HarnessStateDir, "settings.json")); err == nil || !strings.Contains(o, "Operation not permitted") {
		t.Errorf("a write through the seat's settings.json link was not refused by the fence: %v %s", err, o)
	}
	if b, err := os.ReadFile(humanSettings); err != nil || string(b) != "{}" {
		t.Errorf("the Human's settings.json = %q, %v; want it unchanged", b, err)
	}
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

// In its dispatcher's home a soldier writes only its own status file, ack log
// and current scout report (Files) and its harness state dir; every other path
// under state/, data/, .journal/ and .lock/ is refused, and so is renaming a
// root away to plant a tree of its own (G1058). Each refused row names its
// nearest allowed neighbour. The table runs for a home outside the temp roots
// and for one inside a temp root (shim set, as a launch sets it), where only the
// profile's home-root deny refuses.
func TestSoldierFenceHomeWrites(t *testing.T) {
	for _, placement := range []string{"outside the temp roots", "inside a temp root"} {
		t.Run(placement, func(t *testing.T) {
			out := outsideDir(t)
			seatEnv(t, out)
			l := newSplitLayout(t, out)
			if placement == "inside a temp root" {
				l = newSplitLayout(t, "")
			}
			state := filepath.Join(l.home, "state")
			data := filepath.Join(l.home, "data", "T-1")
			status, acks, report := filepath.Join(state, "T-1.status"), filepath.Join(state, "T-1.acks"), filepath.Join(data, "report-g1.md")
			metaLock := filepath.Join(state, "T-1.meta.lock")
			shim := filepath.Join(l.home, "git-shim", "bin")
			mkdirs(t, shim, data, filepath.Join(state, "fleet-registry"), filepath.Join(state, "task-authority", "tasks", "T-1"),
				filepath.Join(state, ".inbox", "general"))
			if err := os.Chmod(state, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{status, metaLock} {
				if err := os.WriteFile(f, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			launch := l.soldier()
			launch.HarnessStateDir = l.claudeStateDir()
			launch.Files = []string{status, acks, report}
			launch.PathDirs, launch.ShimDir = []string{shim, "/usr/bin"}, shim
			f := mustNew(t, launch)
			// A later launch of a task whose ack log the seat replaced with a link
			// to the project registry.
			registry := filepath.Join(state, "fleet-registry", "projects.json")
			if err := os.WriteFile(registry, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			linkedAcks := filepath.Join(state, "T-3.acks")
			if err := os.Symlink(registry, linkedAcks); err != nil {
				t.Fatal(err)
			}
			relaunch := launch
			relaunch.Branch = "mu/T-3"
			relaunch.Files = []string{filepath.Join(state, "T-3.status"), linkedAcks}
			linked := mustNew(t, relaunch)
			chmod := func(mode, path string) []string {
				return []string{"/usr/bin/perl", "-e", "chmod(" + mode + ", $ARGV[0]) or die \"$!\\n\"", path}
			}
			flock := []string{"/usr/bin/perl", "-MFcntl=:DEFAULT,:flock", "-e",
				`sysopen(F, $ARGV[0], O_RDONLY|O_CREAT) or die "$!\n"; flock(F, LOCK_EX) or die "$!\n"`, metaLock}
			sh := func(script string) []string { return []string{"/bin/sh", "-c", script} }
			tests := []struct {
				name      string
				argv      []string
				refused   bool
				unchanged string // a path a refused write must leave as it was (absent stays absent)
				fence     *Fence // nil: f
			}{
				{"another task's status (allowed: own status append)", []string{touchBin, filepath.Join(state, "T-2.status")}, true, filepath.Join(state, "T-2.status"), nil},
				{"own status append", sh(`printf 'working: x\n' >> ` + status), false, "", nil},
				{"a new directory in state/ (allowed: own status atomic rename)", []string{"/bin/mkdir", filepath.Join(state, "x")}, true, filepath.Join(state, "x"), nil},
				{"own status atomic rename through its temp sibling", sh(`printf 'y\n' > ` + status + `.tmp.1.ab && /bin/mv ` + status + `.tmp.1.ab ` + status), false, "", nil},
				{"an ack in the dispatcher's inbox (allowed: own ack log)", []string{touchBin, filepath.Join(state, ".inbox", "general", "m1.ack")}, true, filepath.Join(state, ".inbox", "general", "m1.ack"), nil},
				{"own ack log, created", []string{touchBin, acks}, false, "", nil},
				{"own ack log appended", sh(`printf '{}\n' >> ` + acks), false, "", nil},
				{"next generation's report (allowed: own current report)", []string{touchBin, filepath.Join(data, "report-g2.md")}, true, filepath.Join(data, "report-g2.md"), nil},
				{"own brief (allowed: own current report)", []string{touchBin, filepath.Join(data, "brief.md")}, true, filepath.Join(data, "brief.md"), nil},
				{"own current report", []string{touchBin, report}, false, "", nil},
				{"own task document, which holds its review verdict (allowed: own status)", []string{touchBin, filepath.Join(state, "task-authority", "tasks", "T-1", "current.json")}, true, filepath.Join(state, "task-authority", "tasks", "T-1", "current.json"), nil},
				{"a delivery authorization (allowed: own status)", []string{"/bin/mkdir", "-p", filepath.Join(state, "task-authority", "delivery", "T-1")}, true, filepath.Join(state, "task-authority", "delivery"), nil},
				{"a gate record (allowed: own status)", []string{"/bin/mkdir", "-p", filepath.Join(state, "task-authority", "gates", "T-1")}, true, filepath.Join(state, "task-authority", "gates"), nil},
				{"the project registry (allowed: own status)", sh(`printf x >> ` + registry), true, registry, nil},
				{"the project registry through own ack log, a link planted before launch (allowed: own ack log appended)", sh(`printf x >> ` + linkedAcks), true, registry, linked},
				{"the project registry under a launch whose ack log is that link (allowed: own ack log appended)", sh(`printf x >> ` + registry), true, registry, linked},
				{"own status under a launch whose ack log is a link", sh(`printf 'working: x\n' >> ` + filepath.Join(state, "T-3.status")), false, "", linked},
				{"the captain registry (allowed: own status)", []string{touchBin, filepath.Join(state, "fleet-registry", "captains.json")}, true, filepath.Join(state, "fleet-registry", "captains.json"), nil},
				{"the binding registry (allowed: own status)", []string{touchBin, filepath.Join(state, "fleet-registry", "bindings.json")}, true, filepath.Join(state, "fleet-registry", "bindings.json"), nil},
				{"own meta (allowed: own status)", []string{touchBin, filepath.Join(state, "T-1.meta")}, true, filepath.Join(state, "T-1.meta"), nil},
				{"own report cursor (allowed: own status)", []string{touchBin, filepath.Join(state, "T-1.report-cursor")}, true, filepath.Join(state, "T-1.report-cursor"), nil},
				{"the wake queue (allowed: own status)", []string{touchBin, filepath.Join(state, ".wake-queue")}, true, filepath.Join(state, ".wake-queue"), nil},
				{"the event log (allowed: own status)", []string{touchBin, filepath.Join(state, ".event-log")}, true, filepath.Join(state, ".event-log"), nil},
				{"a journal record (allowed: own status)", []string{touchBin, filepath.Join(l.home, ".journal", "fleet-projects.x.json")}, true, filepath.Join(l.home, ".journal", "fleet-projects.x.json"), nil},
				{"a scope lock, created (allowed: flock of own existing meta lock)", []string{touchBin, filepath.Join(l.home, ".lock", "dispatch.lock")}, true, filepath.Join(l.home, ".lock", "dispatch.lock"), nil},
				{"flock of own existing meta lock", flock, false, "", nil},
				{"chmod of state/, even to its own mode (allowed: chmod of own status)", chmod("0700", state), true, "", nil},
				{"chmod of own status", chmod("0600", status), false, "", nil},
				{"renaming state/ away (allowed: own status atomic rename)", []string{"/bin/mv", state, state + ".x"}, true, state + ".x", nil},
				{"renaming data/ away (allowed: own status atomic rename)", []string{"/bin/mv", filepath.Join(l.home, "data"), filepath.Join(l.home, "data.x")}, true, filepath.Join(l.home, "data.x"), nil},
				{"own harness state dir", sh(`/bin/mkdir -p ` + launch.HarnessStateDir + ` && printf x > ` + launch.HarnessStateDir + `/f`), false, "", nil},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					seat := f
					if tt.fence != nil {
						seat = tt.fence
					}
					// What a path is: absent, or its mode and content.
					snap := func(path string) string {
						fi, err := os.Lstat(path)
						if err != nil {
							return "absent"
						}
						b, _ := os.ReadFile(path)
						return fi.Mode().String() + string(b)
					}
					before := snap(tt.unchanged)
					o, err := fenced(t, seat, tt.argv...)
					if !tt.refused {
						if err != nil {
							t.Fatalf("%q was refused: %v %s", tt.argv, err, o)
						}
						return
					}
					if err == nil || !strings.Contains(o, "not permitted") {
						t.Errorf("%q was not refused by the fence: %v %s", tt.argv, err, o)
					}
					if tt.unchanged != "" && snap(tt.unchanged) != before {
						t.Errorf("%s changed after a write the fence must refuse", tt.unchanged)
					}
				})
			}
		})
	}
}

// A pi seat writes its own agent dir, built by munsu, and nothing in the
// Human's ~/.pi/agent, directly or through the links its own dir holds.
func TestSoldierFenceWithPiStateDir(t *testing.T) {
	out := outsideDir(t)
	uhome := seatEnv(t, out)
	l := newSplitLayout(t, out)
	agent := filepath.Join(uhome, ".pi", "agent")
	mkdirs(t, filepath.Join(agent, "install"), filepath.Join(agent, "sessions"))
	for _, name := range []string{"auth.json", "trust.json", "install/current-version"} {
		if err := os.WriteFile(filepath.Join(agent, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	launch := l.soldier()
	launch.Harness = harness.Pi
	launch.HarnessStateDir = l.stateDir()
	if err := harness.BuildPiAgentDir(agent, harness.SeatBuildDir(l.home), launch.HarnessStateDir); err != nil {
		t.Fatal(err)
	}
	f := mustNew(t, launch)
	own := launch.HarnessStateDir
	sh := func(script string) []string { return []string{"/bin/sh", "-c", script} }
	for _, tt := range []struct {
		name      string
		argv      []string
		refused   bool
		unchanged string // the Human's file a refused write must leave as it was
	}{
		{"a file in the Human's agent dir (allowed: own sessions)", []string{touchBin, filepath.Join(agent, "f")}, true, filepath.Join(agent, "f")},
		{"the Human's installed pi version (allowed: own sessions)", sh(`printf x >> ` + filepath.Join(agent, "install", "current-version")), true, filepath.Join(agent, "install", "current-version")},
		{"the Human's auth.json through own dir's link (allowed: own auth lock)", sh(`printf x >> ` + filepath.Join(own, "auth.json")), true, filepath.Join(agent, "auth.json")},
		{"own sessions", sh(`/bin/mkdir -p ` + own + `/sessions/--wt-- && printf x > ` + own + `/sessions/--wt--/s.jsonl`), false, ""},
		{"own auth lock", []string{"/bin/mkdir", filepath.Join(own, "auth.json.lock")}, false, ""},
		{"own trust answer", sh(`printf x >> ` + filepath.Join(own, "trust.json")), false, filepath.Join(agent, "trust.json")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before, _ := os.ReadFile(tt.unchanged)
			o, err := fenced(t, f, tt.argv...)
			if !tt.refused {
				if err != nil {
					t.Fatalf("%q was refused: %v %s", tt.argv, err, o)
				}
			} else if err == nil || !strings.Contains(o, "not permitted") {
				t.Errorf("%q was not refused by the fence: %v %s", tt.argv, err, o)
			}
			if after, _ := os.ReadFile(tt.unchanged); tt.unchanged != "" && string(after) != string(before) {
				t.Errorf("the Human's %s changed", tt.unchanged)
			}
		})
	}
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
		filepath.Join(launch.HarnessStateDir, "f"),
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
		filepath.Join(uhome, "other", "f"), filepath.Join(uhome, ".pi", "agent", "f"), verdict+"2",
	)

	// A claude reviewer writes its own config dir, carved out of the refused
	// home roots, and never the Human's claude config or the seat build dir.
	// Its home sits outside the temp roots, as a real home does.
	ls := newSplitLayout(t, out)
	cl := ls.reviewer()
	cl.HarnessStateDir = ls.claudeStateDir()
	mkdirs(t, cl.HarnessStateDir, harness.SeatBuildDir(ls.home))
	cf := mustNew(t, cl)
	mustWrite(t, cf, filepath.Join(cl.HarnessStateDir, "f"))
	mustRefuse(t, cf, filepath.Join(uhome, ".claude", "f"), filepath.Join(uhome, ".claude.json"), filepath.Join(harness.SeatBuildDir(ls.home), "f"))
}

// mustRefuseMkdir requires the sandbox's own refusal of creating each dir.
func mustRefuseMkdir(t *testing.T, f *Fence, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		out, err := fenced(t, f, "/bin/mkdir", d)
		if err == nil || !strings.Contains(out, "Operation not permitted") {
			t.Errorf("mkdir %s was not refused by the fence: %v %s", d, err, out)
		}
		if _, statErr := os.Lstat(d); statErr == nil {
			t.Errorf("%s exists after a mkdir the fence must refuse", d)
			os.RemoveAll(d)
		}
	}
}

// The dirs munsu builds what a claude or pi seat loads in are refused to every
// seat but their owner. The home sits under a temp root, which every seat
// writes (MUNSU_HOME can put it there), so only the profile's late denies
// refuse these. A blocked row covers planting the name and writing inside an
// existing one; the allow rows are the nearest valid writes of the same seat.
func TestSeatDirRefusals(t *testing.T) {
	seatEnv(t, t.TempDir())
	l := newLayout(t)
	state := filepath.Join(l.home, "state")
	build := harness.SeatBuildDir(l.home)
	sl := l.soldier()
	sl.HarnessStateDir = l.claudeStateDir()
	sl.Files = []string{filepath.Join(state, "T-1.status")}
	rl := l.reviewer()
	rl.HarnessStateDir = filepath.Join(state, "R-1."+harness.ClaudeConfigDirSuffix)
	mkdirs(t, sl.HarnessStateDir, rl.HarnessStateDir)
	soldier, reviewer := mustNew(t, sl), mustNew(t, rl)
	// Another task's live seat dirs, built by munsu.
	live := []string{filepath.Join(state, "T-3."+harness.ClaudeConfigDirSuffix), filepath.Join(state, "T-3."+harness.PiAgentDirSuffix)}
	mkdirs(t, live...)
	otherDirs := func(f *Fence) {
		mustRefuseMkdir(t, f, filepath.Join(state, "T-2."+harness.ClaudeConfigDirSuffix), filepath.Join(state, "T-2."+harness.PiAgentDirSuffix))
		mustRefuse(t, f, filepath.Join(live[0], "settings.json"), filepath.Join(live[1], "settings.json"))
	}
	buildDir := func(f *Fence) {
		if err := os.RemoveAll(build); err != nil {
			t.Fatal(err)
		}
		mustRefuseMkdir(t, f, build)
		mkdirs(t, build)
		mustRefuse(t, f, filepath.Join(build, "f"))
	}
	for _, tc := range []struct {
		name  string
		check func()
	}{
		{"a soldier is refused another task's seat dirs, its reviewer's included", func() {
			otherDirs(soldier)
			mustRefuse(t, soldier, filepath.Join(rl.HarnessStateDir, "settings.json"))
		}},
		{"a reviewer is refused another task's seat dirs, its soldier's included", func() {
			otherDirs(reviewer)
			mustRefuse(t, reviewer, filepath.Join(sl.HarnessStateDir, "settings.json"))
		}},
		{"a soldier writes its own seat dir and its own status", func() {
			mustWrite(t, soldier, filepath.Join(sl.HarnessStateDir, "f"), filepath.Join(state, "T-1.status"))
		}},
		{"a reviewer writes its own seat dir", func() { mustWrite(t, reviewer, filepath.Join(rl.HarnessStateDir, "f")) }},
		{"a soldier is refused the seat build dir", func() { buildDir(soldier) }},
		{"a reviewer is refused the seat build dir", func() { buildDir(reviewer) }},
		{"a reviewer writes a temp path outside the home", func() { mustWrite(t, reviewer, filepath.Join(l.root, "f")) }},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.check() })
	}
}

// connects reports whether the fenced nc reached a listener the test holds at
// sock, which proves the bytes arrived. nc prints no reason for a refused
// connect, so a refusal is nc failing while the held listener accepts nothing.
func connects(t *testing.T, f *Fence, sock string) (bool, error) {
	t.Helper()
	mkdirs(t, filepath.Dir(sock))
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			got <- ""
			return
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, len("typed"))
		n, _ := io.ReadFull(c, buf)
		got <- string(buf[:n])
	}()
	wrapped, err := f.Wrap([]string{"/usr/bin/nc", "-U", sock})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, wrapped[0], wrapped[1:]...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Stdin = strings.NewReader("typed")
	runErr := cmd.Run()
	// A connect that succeeded is queued before nc exits, so a short accept
	// deadline cannot miss it.
	l.(*net.UnixListener).SetDeadline(time.Now().Add(200 * time.Millisecond))
	return <-got == "typed", runErr
}

// A seat, soldier or reviewer, connects to no other session's input socket:
// claude's message sockets, or a tmux server under its default dir or under
// TMUX_TMPDIR (G1085). The allow rows are the nearest valid connects: a socket
// in a sibling temp dir, and the herdr socket, which part 2 has not fenced yet.
// The dirs sit in short scratch paths (a unix socket path is capped near 104
// bytes), never the live ones.
func TestSeatCannotReachAnotherSessionsSocket(t *testing.T) {
	scratch, err := os.MkdirTemp("/tmp", "fsock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(scratch) })
	if scratch, err = filepath.EvalSymlinks(scratch); err != nil {
		t.Fatal(err)
	}
	origClaude, origTmux := claudeSocketDir, tmuxTempDir
	t.Cleanup(func() { claudeSocketDir, tmuxTempDir = origClaude, origTmux })
	claudeSocketDir, tmuxTempDir = filepath.Join(scratch, "cc-socks"), filepath.Join(scratch, "tmp")
	t.Setenv("TMUX_TMPDIR", filepath.Join(scratch, "tmuxtmp"))
	herdrSock := filepath.Join(scratch, "herdr", "herdr.sock")
	t.Setenv("HERDR_SOCKET_PATH", herdrSock)
	tmux := "tmux-" + strconv.Itoa(os.Getuid())

	l := newLayout(t)
	for _, role := range []struct {
		name string
		f    *Fence
	}{{"soldier", mustNew(t, l.soldier())}, {"reviewer", mustNew(t, l.reviewer())}} {
		for _, tt := range []struct {
			name    string
			sock    string
			refused bool
		}{
			{"another claude session's message socket", filepath.Join(claudeSocketDir, "1.sock"), true},
			{"a tmux server under its default dir", filepath.Join(tmuxTempDir, tmux, "default"), true},
			{"a tmux server under TMUX_TMPDIR, the third term of one deny", filepath.Join(scratch, "tmuxtmp", tmux, "default"), true},
			{"a socket in a sibling temp dir", filepath.Join(tmuxTempDir, "other", "s.sock"), false},
			{"the herdr socket until part 2", herdrSock, false},
		} {
			t.Run(role.name+"/"+tt.name, func(t *testing.T) {
				ok, err := connects(t, role.f, tt.sock)
				if tt.refused && (ok || err == nil) {
					t.Errorf("connect to %s was not refused by the fence: connected=%v nc=%v", tt.sock, ok, err)
				}
				if !tt.refused && (!ok || err != nil) {
					t.Errorf("connect to %s was refused: connected=%v nc=%v", tt.sock, ok, err)
				}
			})
		}
	}
}

// A seat's PATH directories are refused wholesale, whatever allowed root they
// sit in, and so is renaming one away. The allow rows are the nearest valid
// writes: the same roots, one name over.
func TestSeatCannotWriteReplaceOrRenameAPathDir(t *testing.T) {
	out := outsideDir(t)
	uhome := seatEnv(t, out)
	l := newSplitLayout(t, out)
	state := filepath.Join(l.home, "state")
	// The shim sits beside state/, outside every writable root.
	shim := filepath.Join(l.home, "git-shim", "bin")
	claudeCache := filepath.Join(uhome, "Library", "Caches", "claude-cli-nodejs")
	claudeBin := filepath.Join(claudeCache, "local")
	tmpBin := filepath.Join(l.root, "pathbin")
	mkdirs(t, shim, claudeBin, tmpBin)
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte("shim"), 0o755); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(l.worktree, "planted")
	if err := os.WriteFile(planted, []byte("planted"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A PATH entry that is a symlink names a directory the sandbox only sees by
	// its real path.
	realBin, linkBin := filepath.Join(l.root, "realbin"), filepath.Join(l.root, "linkbin")
	mkdirs(t, realBin, filepath.Join(l.root, "realbin-sibling"))
	if err := os.Symlink(realBin, linkBin); err != nil {
		t.Fatal(err)
	}
	// A PATH dir deeper under a harness root: its middle directory is strictly
	// inside the root, the root itself is not.
	deepBin := filepath.Join(claudeCache, "deep", "mid", "bin")
	mkdirs(t, deepBin)
	// A PATH dir inside the worktree, a writable root: node_modules is strictly
	// inside it.
	wtBin := filepath.Join(l.worktree, "node_modules", ".bin")
	mkdirs(t, wtBin)
	// The same shim under a home that sits inside a temp root, itself a
	// writable root: nothing above the shim may be chmod'ed either.
	tl := newSplitLayout(t, "")
	tshim := filepath.Join(tl.home, "git-shim", "bin")
	mkdirs(t, tshim)
	// A refused chmod 000 that is not refused must not leave a tree the
	// cleanup cannot remove.
	t.Cleanup(func() {
		for _, d := range []string{filepath.Dir(shim), shim, claudeBin, filepath.Dir(deepBin), deepBin, filepath.Dir(wtBin), wtBin, tl.home, filepath.Dir(tshim), tshim} {
			os.Chmod(d, 0o755)
		}
	})
	piAgentBin := filepath.Join(uhome, ".pi", "agent", "bin")
	mkdirs(t, filepath.Join(l.stateDir(), "bin"), piAgentBin)
	pathDirs := []string{shim, claudeBin, deepBin, wtBin, tmpBin, linkBin, "/usr/bin"}
	sl, rl := l.soldier(), l.reviewer()
	sl.Files = []string{filepath.Join(state, "T-1.status")}
	sl.PathDirs, rl.PathDirs = pathDirs, pathDirs
	sl.ShimDir, rl.ShimDir = shim, shim
	soldier, reviewer := mustNew(t, sl), mustNew(t, rl)
	tsl := tl.soldier()
	tsl.PathDirs, tsl.ShimDir = []string{tshim, "/usr/bin"}, tshim
	tmpHome := mustNew(t, tsl)
	// The user's temp root that holds that home. chmod skips a mode the file
	// already has, so its times are touched instead: a refusal is the sandbox's,
	// and an allowed touch of a temp dir changes nothing that matters.
	tmpRoot, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// pi prepends <agent dir>/bin to the PATH of its commands, so the pi seat's
	// own state dir and the default agent dir hold two more PATH directories.
	psl, prl := sl, rl
	psl.Harness, prl.Harness = harness.Pi, harness.Pi
	psl.HarnessStateDir, prl.HarnessStateDir = l.stateDir(), l.stateDir()
	piSoldier, piReviewer := mustNew(t, psl), mustNew(t, prl)

	tests := []struct {
		name    string
		f       *Fence
		argv    []string
		refused bool
		absent  string // a path a refused write must not have created
		kept    string // a path a refused write must leave as it was
	}{
		{"soldier creates a file in the git shim dir", soldier, []string{touchBin, filepath.Join(shim, "gh")}, true, filepath.Join(shim, "gh"), ""},
		{"soldier replaces the git shim", soldier, []string{"/bin/cp", planted, filepath.Join(shim, "git")}, true, "", filepath.Join(shim, "git")},
		{"soldier creates a file in a PATH dir under a harness root", soldier, []string{touchBin, filepath.Join(claudeBin, "go")}, true, filepath.Join(claudeBin, "go"), ""},
		{"soldier creates a file in a PATH dir under a temp root", soldier, []string{touchBin, filepath.Join(tmpBin, "go")}, true, filepath.Join(tmpBin, "go"), ""},
		{"reviewer creates a file in a PATH dir under a temp root", reviewer, []string{touchBin, filepath.Join(tmpBin, "go")}, true, filepath.Join(tmpBin, "go"), ""},
		{"soldier creates a file in a symlinked PATH dir's real directory", soldier, []string{touchBin, filepath.Join(realBin, "go")}, true, filepath.Join(realBin, "go"), ""},
		{"soldier creates a file through a symlinked PATH dir", soldier, []string{touchBin, filepath.Join(linkBin, "go")}, true, filepath.Join(realBin, "go"), ""},
		{"soldier writes beside a symlinked PATH dir's real directory", soldier, []string{touchBin, filepath.Join(l.root, "realbin-sibling", "f")}, false, "", ""},
		{"soldier writes its worktree", soldier, []string{touchBin, filepath.Join(l.worktree, "f")}, false, "", ""},
		{"soldier writes its task status", soldier, []string{touchBin, filepath.Join(state, "T-1.status")}, false, "", ""},
		{"soldier writes beside a harness PATH dir", soldier, []string{touchBin, filepath.Join(claudeCache, "f")}, false, "", ""},
		{"pi soldier creates a file in its agent dir's bin", piSoldier, []string{touchBin, filepath.Join(l.stateDir(), "bin", "gh")}, true, filepath.Join(l.stateDir(), "bin", "gh"), ""},
		{"pi soldier creates a file in the default pi agent bin", piSoldier, []string{touchBin, filepath.Join(piAgentBin, "pi")}, true, filepath.Join(piAgentBin, "pi"), ""},
		{"pi reviewer creates a file in its agent dir's bin", piReviewer, []string{touchBin, filepath.Join(l.stateDir(), "bin", "gh")}, true, filepath.Join(l.stateDir(), "bin", "gh"), ""},
		{"pi soldier writes its agent dir", piSoldier, []string{touchBin, filepath.Join(l.stateDir(), "f")}, false, "", ""},
		{"pi reviewer writes its agent dir", piReviewer, []string{touchBin, filepath.Join(l.stateDir(), "f")}, false, "", ""},
		{"soldier chmods a harness root that holds a PATH dir", soldier, []string{"/bin/chmod", "700", claudeCache}, false, "", ""},
		{"pi soldier chmods its own state dir", piSoldier, []string{"/bin/chmod", "700", l.stateDir()}, false, "", ""},
		{"soldier chmods a PATH dir under a harness root to 000", soldier, []string{"/bin/chmod", "000", claudeBin}, true, "", claudeBin},
		{"soldier creates a file in a PATH dir inside its worktree", soldier, []string{touchBin, filepath.Join(wtBin, "go")}, true, filepath.Join(wtBin, "go"), ""},
		{"soldier writes beside a PATH dir inside its worktree", soldier, []string{touchBin, filepath.Join(l.worktree, "node_modules", "f")}, false, "", ""},
		{"soldier chmods the directory above a PATH dir inside its worktree to 000", soldier, []string{"/bin/chmod", "000", filepath.Dir(wtBin)}, true, "", wtBin},
		{"soldier chmods its worktree", soldier, []string{"/bin/chmod", "700", l.worktree}, false, "", ""},
		{"soldier chmods a home under a temp root to 000", tmpHome, []string{"/bin/chmod", "000", tl.home}, true, "", tshim},
		{"soldier chmods the dir holding a home under a temp root to 000", tmpHome, []string{"/bin/chmod", "000", filepath.Dir(tl.home)}, true, "", tshim},
		{"soldier changes the temp root holding the home", tmpHome, []string{touchBin, tmpRoot}, true, "", tshim},
		{"soldier chmods the git shim dir to 000", soldier, []string{"/bin/chmod", "000", shim}, true, "", shim},
		{"soldier chmods the git shim dir's parent to 000", soldier, []string{"/bin/chmod", "000", filepath.Dir(shim)}, true, "", shim},
		{"soldier chmods a middle dir above a PATH dir under a harness root to 000", soldier, []string{"/bin/chmod", "000", filepath.Dir(deepBin)}, true, "", deepBin},
		{"soldier renames the git shim dir", soldier, []string{"/bin/mv", shim, shim + ".old"}, true, shim + ".old", shim},
		{"soldier renames the git shim dir's parent", soldier, []string{"/bin/mv", filepath.Dir(shim), filepath.Dir(shim) + ".old"}, true, filepath.Dir(shim) + ".old", shim},
		{"soldier removes the git shim dir's parent", soldier, []string{"/bin/rm", "-r", filepath.Dir(shim)}, true, "", shim},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, err := fenced(t, tt.f, tt.argv...)
			if !tt.refused {
				if err != nil {
					t.Fatalf("%q was refused: %v %s", tt.argv, err, o)
				}
				return
			}
			if err == nil || !strings.Contains(o, "Operation not permitted") {
				t.Errorf("%q was not refused by the fence: %v %s", tt.argv, err, o)
			}
			if tt.absent != "" {
				if _, statErr := os.Lstat(tt.absent); statErr == nil {
					t.Errorf("%s exists after a write the fence must refuse", tt.absent)
				}
			}
			if _, statErr := os.Lstat(tt.kept); tt.kept != "" && statErr != nil {
				t.Errorf("%s is gone after a write the fence must refuse", tt.kept)
			}
			if tt.kept == filepath.Join(shim, "git") {
				if b, _ := os.ReadFile(tt.kept); string(b) != "shim" {
					t.Errorf("the git shim now reads %q", b)
				}
			}
		})
	}
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
		{"soldier", l.soldier(), append([]string{l.primary, l.common}, homeRoots(l.home)...)},
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
	// A home root that does not exist yet is probed by creating it.
	t.Run("soldier whose home has no journal yet", func(t *testing.T) {
		ml := newLayout(t)
		journal := filepath.Join(ml.home, ".journal")
		if err := os.Remove(journal); err != nil {
			t.Fatal(err)
		}
		ev, err := mustNew(t, ml.soldier()).Probe(context.Background())
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		var step *ProbeStep
		for i := range ev.Steps {
			if ev.Steps[i].Command[4] == journal {
				step = &ev.Steps[i]
			}
		}
		if step == nil || step.Outcome != OutcomeRefused {
			t.Fatalf("steps = %+v; want the journal's creation refused", ev.Steps)
		}
		if _, err := os.Lstat(journal); err == nil {
			t.Fatal("the probe created the journal")
		}
	})
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
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
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
		{"a refusal that is not the sandbox's is an error", &Fence{profile: open, digest: "d", refuse: []string{filepath.Join(dir, "file", "sub")}}, OutcomeError, 2},
		{"a missing refused dir the profile lets the seat create is open", &Fence{profile: open, digest: "d", refuse: []string{filepath.Join(dir, "missing")}}, OutcomeWritten, 2},
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
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
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
		{"a relative PATH directory", func(x *Launch) { x.PathDirs = []string{"/usr/bin", "bin"} }, `PATH directory "bin" is not absolute`},
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
	t.Run("absolute PATH directories are accepted", func(t *testing.T) {
		launch := l.soldier()
		launch.PathDirs = []string{filepath.Join(l.home, "git-shim", "bin"), "/usr/bin"}
		mustNew(t, launch)
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

func TestWritableRootsAreWritableAndTheProbeStillRefuses(t *testing.T) {
	out := outsideDir(t)
	seatEnv(t, out)
	l := newSplitLayout(t, out)
	cache := filepath.Join(out, "npm-cache")
	mkdirs(t, cache)
	launch := l.soldier()
	launch.WritableRoots = []string{cache}
	f := mustNew(t, launch)

	mustWrite(t, f, filepath.Join(cache, "f"))
	mustRefuse(t, f, filepath.Join(l.primary, "f"))
	ev, err := f.Probe(context.Background())
	if err != nil || len(ev.WritableRoots) != 1 || ev.WritableRoots[0] != cache {
		t.Fatalf("probe = %+v, %v; want the roots in the evidence and every refusal proven", ev, err)
	}
}
