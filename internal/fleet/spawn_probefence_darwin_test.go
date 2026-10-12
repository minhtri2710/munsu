//go:build darwin

package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestProbeFenceRecordsTheProjectsWritableRoots(t *testing.T) {
	isolateHuman(t)
	f := newFenceFixture(t)
	cache, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	overlay := fleetconfig.ProjectOverlay{FenceWritableRoots: []string{cache}}

	t.Run("a soldier's fence carries the roots", func(t *testing.T) {
		r := f.runner("direct-PR")
		withOverlay(t, r, overlay)
		if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
			t.Fatal(err)
		}
		if got := r.fenceRecord.WritableRoots; len(got) != 1 || got[0] != cache {
			t.Fatalf("record = %+v, want the root %s", r.fenceRecord, cache)
		}
	})
	t.Run("a root inside the primary checkout refuses the launch", func(t *testing.T) {
		r := f.runner("direct-PR")
		withOverlay(t, r, fleetconfig.ProjectOverlay{FenceWritableRoots: []string{filepath.Join(f.primary, "node_modules")}})
		if err := r.probeFence(BoundWorktree{path: f.worktree}); err == nil || !strings.Contains(err.Error(), "overlaps protected path "+f.primary) {
			t.Fatalf("err = %v, want the root inside the primary refused", err)
		}
	})
	t.Run("a reviewer's fence carries none", func(t *testing.T) {
		r := f.runner("direct-PR")
		withOverlay(t, r, overlay)
		launchDir := filepath.Join(f.home, "review", "task-1", "1")
		if err := os.MkdirAll(filepath.Join(f.home, "state"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(launchDir, 0o755); err != nil {
			t.Fatal(err)
		}
		r.review = &ReviewLaunch{LaunchDir: launchDir, VerdictFile: reviewVerdictFile(launchDir)}
		if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
			t.Fatal(err)
		}
		if len(r.fenceRecord.WritableRoots) != 0 {
			t.Fatalf("record = %+v, a reviewer gets no extra roots", r.fenceRecord)
		}
	})
}

// The launch hands the fence the PATH its script builds: the shim dir and every
// inherited directory are refused, and the seat's own worktree still is not.
func TestProbeFenceRefusesTheSeatsPathDirs(t *testing.T) {
	isolateHuman(t)
	f := newFenceFixture(t)
	inherited, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", inherited+string(os.PathListSeparator)+os.Getenv("PATH"))
	shim := gitShimDir(f.home)
	if err := os.MkdirAll(shim, 0o755); err != nil {
		t.Fatal(err)
	}
	r := f.runner("direct-PR")
	if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
		t.Fatal(err)
	}
	touch := func(dir string) (string, error) {
		argv, err := r.fence.Wrap([]string{"/usr/bin/touch", filepath.Join(dir, "gh")})
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		return string(out), err
	}
	for _, dir := range []string{shim, inherited} {
		if out, err := touch(dir); err == nil || !strings.Contains(out, "Operation not permitted") {
			t.Errorf("write in PATH dir %s was not refused by the fence: %v %s", dir, err, out)
		}
	}
	if out, err := touch(f.worktree); err != nil {
		t.Errorf("write in the worktree was refused: %v %s", err, out)
	}
}

// fencedAppendStatusHome names the home the child process below appends to.
const fencedAppendStatusHome = "MUNSU_TEST_FENCED_APPEND_STATUS_HOME"

// TestFencedAppendStatusChild is the body TestFencedSoldierAppendsItsStatus
// runs inside the fence: the real status append every soldier report makes.
func TestFencedAppendStatusChild(t *testing.T) {
	dir := os.Getenv(fencedAppendStatusHome)
	if dir == "" {
		t.Skip("runs only as the child of TestFencedSoldierAppendsItsStatus")
	}
	if err := home.AppendStatus(dir, "task-1", "running: x"); err != nil {
		t.Fatal(err)
	}
}

// A soldier's own status append chmods the state dir, a fence writable root:
// the PATH-dir protection must leave that mode change alone.
func TestFencedSoldierAppendsItsStatus(t *testing.T) {
	isolateHuman(t)
	f := newFenceFixture(t)
	state := filepath.Join(f.home, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o755); err != nil {
		t.Fatal(err)
	}
	r := f.runner("direct-PR")
	if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
		t.Fatal(err)
	}
	argv, err := r.fence.Wrap([]string{os.Args[0], "-test.run=^TestFencedAppendStatusChild$"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), fencedAppendStatusHome+"="+f.home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the status append failed under the fence: %v\n%s", err, out)
	}
	if fi, err := os.Stat(state); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("state dir = %v, %v; want the append to have restricted it to 0700", fi, err)
	}
	lines, err := home.ReadStatus(f.home, "task-1")
	if err != nil || len(lines) != 1 || lines[0] != "running: x" {
		t.Fatalf("status = %q, %v; want the appended line", lines, err)
	}
}

// fencedOpenHome names the home the child process below opens.
const fencedOpenHome = "MUNSU_TEST_FENCED_OPEN_HOME"

// TestFencedOpenHomeChild is the body TestFencedSoldierOpensItsHome runs inside
// the fence: the home open every soldier-side munsu command makes.
func TestFencedOpenHomeChild(t *testing.T) {
	dir := os.Getenv(fencedOpenHome)
	if dir == "" {
		t.Skip("runs only as the child of TestFencedSoldierOpensItsHome")
	}
	if _, err := home.Open(dir); err != nil {
		t.Fatal(err)
	}
}

// The fence locks every ancestor of the git shim, the home included, so that
// lock holds only while munsu never chmods the home on a seat's path: only
// createHome does, before any seat exists. A seat-side chmod of the home (even
// to its own mode) fails here with "operation not permitted". The home sits in
// a temp root, so the roots-included case is the one checked.
func TestFencedSoldierOpensItsHome(t *testing.T) {
	isolateHuman(t)
	f := newFenceFixture(t)
	if _, err := home.Init(f.home); err != nil {
		t.Fatal(err)
	}
	if _, err := provisionGitShim(f.home); err != nil {
		t.Fatal(err)
	}
	r := f.runner("direct-PR")
	if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
		t.Fatal(err)
	}
	argv, err := r.fence.Wrap([]string{os.Args[0], "-test.run=^TestFencedOpenHomeChild$"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), fencedOpenHome+"="+f.home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the home open failed under the fence: %v\n%s", err, out)
	}
}

// Taking the shim dir off the seat's PATH would let `git` resolve past the git
// guard: a directory the shell cannot enter is skipped. None of the shim dir,
// its parent or the home (here inside a temp root, a writable root) may be
// chmod'ed to hide it; the state dir the seat writes is not above the shim. The home sits directly in /private/tmp, a
// writable root, so that root is itself an ancestor of the shim.
func TestFencedSoldierCannotTakeTheGitShimOffItsPath(t *testing.T) {
	isolateHuman(t)
	f := newFenceFixture(t)
	tmpHome, err := os.MkdirTemp("/private/tmp", "munsu-shim-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpHome) })
	if f.home, err = canonicalPath(tmpHome); err != nil {
		t.Fatal(err)
	}
	shimDir, err := provisionGitShim(f.home)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(f.home, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	hidden := []string{filepath.Dir(shimDir), shimDir, f.home, state}
	t.Cleanup(func() {
		for _, d := range hidden {
			os.Chmod(d, 0o755)
		}
	})
	r := f.runner("direct-PR")
	if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
		t.Fatal(err)
	}
	script := "PATH=" + shQuote(shimDir) + ":/usr/bin\n"
	for _, d := range hidden {
		script += "/bin/chmod 000 " + shQuote(d) + " 2>/dev/null; command -v git; /bin/chmod 755 " + shQuote(d) + " 2>/dev/null\n"
	}
	argv, err := r.fence.Wrap([]string{"/bin/sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// /private/tmp is a writable root above the home: the fence must refuse a
	// change to its own times, which an unfenced touch of it succeeds at.
	touch, err := r.fence.Wrap([]string{"/usr/bin/touch", "/private/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if o, err := exec.Command(touch[0], touch[1:]...).CombinedOutput(); err == nil || !strings.Contains(string(o), "Operation not permitted") {
		t.Errorf("touch /private/tmp under the fence = %v %s; want the sandbox's refusal", err, o)
	}
	want := filepath.Join(shimDir, "git")
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i, d := range hidden {
		if i >= len(got) || got[i] != want {
			t.Errorf("after a chmod 000 of %s, `command -v git` = %q; want the shim %s", d, got, want)
		}
	}
}

// A claude reviewer's fence carries its config dir: the reviewer writes its
// own sessions there, while every other path in the home stays refused. A
// soldier writes all of state/, so only the reviewer shows the carve-out.
func TestFencedClaudeReviewerWritesItsOwnConfigDir(t *testing.T) {
	isolateHuman(t)
	f := newFenceFixture(t)
	launchDir := filepath.Join(f.home, "review", "task-1", "1")
	for _, d := range []string{filepath.Join(f.home, "state"), launchDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := f.runner("direct-PR")
	r.launchBin = "claude"
	r.review = &ReviewLaunch{LaunchDir: launchDir, VerdictFile: reviewVerdictFile(launchDir)}
	if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
		t.Fatal(err)
	}
	own, err := claudeConfigDirPath(f.home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	other, err := claudeConfigDirPath(f.home, "task-2")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, dir string
		allowed   bool
	}{
		{"writes a session in its own config dir", own, true},
		{"is refused another task's config dir", other, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(tc.dir, 0o755); err != nil {
				t.Fatal(err)
			}
			argv, err := r.fence.Wrap([]string{"/usr/bin/touch", filepath.Join(tc.dir, "session.jsonl")})
			if err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
			if tc.allowed && err != nil {
				t.Fatalf("write refused: %v %s", err, out)
			}
			if !tc.allowed && (err == nil || !strings.Contains(string(out), "Operation not permitted")) {
				t.Fatalf("write = %v %s; want the sandbox's refusal", err, out)
			}
		})
	}
}

// On a fenced host the claude preflight asks for the login the seat will use:
// the Human's keychain item, from an empty config dir that is not the seat's.
func TestFencedClaudePreflightAsksForTheSeatsLogin(t *testing.T) {
	isolateHuman(t)
	homeDir := t.TempDir()
	bin := t.TempDir()
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	for _, tc := range []struct {
		name, check string
		wantErr     string
	}{
		{"a seat login is found", `[ "${CLAUDE_SECURESTORAGE_CONFIG_DIR-unset}" = "" ]`, ""},
		{"no seat login refuses the launch", `false`, "auth-absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := "#!/bin/sh\n" + tc.check + ` && [ -d "$CLAUDE_CONFIG_DIR" ] && case "$CLAUDE_CONFIG_DIR" in ` + shQuote(homeDir) + "/*) exit 1;; esac\n"
			testutil.WriteFakeExecutable(t, filepath.Join(bin, "claude"), script)
			r := NewRunner(Args{ID: "task-1", HarnessFlag: "claude", HomeDir: homeDir})
			r.projPath, r.effectiveMode = t.TempDir(), "direct-PR"
			err := r.preflightHarness()
			var pf *harness.PreflightError
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("preflightHarness: %v", err)
				}
				return
			}
			if !asPreflightError(err, &pf) || pf.Reason != tc.wantErr {
				t.Fatalf("err = %v, want %s", err, tc.wantErr)
			}
		})
	}
}
