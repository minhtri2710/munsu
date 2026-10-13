//go:build darwin

package fleet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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

// probeFence pre-creates the seat's remote reflogs (fence-reflog-remotes-residual),
// and a link planted where one belongs refuses the launch.
func TestProbeFencePreCreatesTheRemoteReflogs(t *testing.T) {
	t.Run("each remote-tracking ref's reflog exists after the probe", func(t *testing.T) {
		isolateHuman(t)
		f := newFenceFixture(t)
		gitInWorktree(t, f.primary, "-c", "core.logAllRefUpdates=false", "update-ref", "refs/remotes/origin/main", "HEAD")
		r := f.runner("direct-PR")
		if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
			t.Fatal(err)
		}
		reflog := filepath.Join(f.primary, ".git", "logs", "refs", "remotes", "origin", "main")
		if fi, err := os.Lstat(reflog); err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("reflog = %v, %v; want a regular file pre-created at spawn", fi, err)
		}
	})
	t.Run("a link planted at a remote's reflog dir refuses the launch", func(t *testing.T) {
		isolateHuman(t)
		f := newFenceFixture(t)
		gitInWorktree(t, f.primary, "-c", "core.logAllRefUpdates=false", "update-ref", "refs/remotes/origin/main", "HEAD")
		victim := t.TempDir()
		remotes := filepath.Join(f.primary, ".git", "logs", "refs", "remotes")
		if err := os.MkdirAll(remotes, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(remotes, "origin")); err != nil {
			t.Fatal(err)
		}
		r := f.runner("direct-PR")
		if err := r.probeFence(BoundWorktree{path: f.worktree}); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("err = %v, want the planted link refused", err)
		}
		if ents, _ := os.ReadDir(victim); len(ents) != 0 {
			t.Errorf("a reflog was written through the planted link: %v", ents)
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

// fencedSoldierHome names the home the child processes below work in.
const fencedSoldierHome = "MUNSU_TEST_FENCED_SOLDIER_HOME"

// TestFencedSoldierWritesChild is the body TestFencedSoldierWritesOnlyItsOwnFiles
// runs inside the fence: the home writes of a soldier's munsu report (its
// status append) and munsu inbox ack (its ack log append).
func TestFencedSoldierWritesChild(t *testing.T) {
	dir := os.Getenv(fencedSoldierHome)
	if dir == "" {
		t.Skip("runs only as the child of TestFencedSoldierWritesOnlyItsOwnFiles")
	}
	if _, err := orchestrator.AppendReportStatus(dir, "task-1", domain.ReportStatusLine("done", "x", "default")); err != nil {
		t.Fatal(err)
	}
	ack := &home.ProcessingAck{MessageID: "m1", SenderRank: home.RankGeneral, SenderIdentity: "general",
		ReceiverRank: home.RankSoldier, ReceiverID: "task-1", TaskID: "task-1", PayloadHash: "h", ProcessedAt: 1, Outcome: home.OutcomeAccepted}
	if err := home.NewStore(dir).WriteAck(ack); err != nil {
		t.Fatal(err)
	}
}

// homeTree records every path under dir with its type, mode and content.
func homeTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		v := fi.Mode().String()
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			v += " " + string(b)
		}
		tree[p] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// Under the real launch fence a soldier's report and ack succeed and change
// nothing in its dispatcher's home but its own status file and ack log: the
// launch creates the meta lock the append locks, and the append leaves the
// private state dir's mode alone (a chmod of it, even to its own mode, is
// refused; TestSoldierFenceHomeWrites).
func TestFencedSoldierWritesOnlyItsOwnFiles(t *testing.T) {
	isolateHuman(t)
	f := newFenceFixture(t)
	if _, err := home.Init(f.home); err != nil {
		t.Fatal(err)
	}
	r := f.runner("direct-PR")
	if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
		t.Fatal(err)
	}
	before := homeTree(t, f.home)
	argv, err := r.fence.Wrap([]string{os.Args[0], "-test.run=^TestFencedSoldierWritesChild$"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), fencedSoldierHome+"="+f.home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the soldier's report and ack failed under the fence: %v\n%s", err, out)
	}
	after := homeTree(t, f.home)
	status := filepath.Join(f.home, "state", "task-1.status")
	acks := filepath.Join(f.home, "state", "task-1.acks")
	for p, v := range after {
		if before[p] != v && p != status && p != acks {
			t.Errorf("%s changed under the fence: %q -> %q", p, before[p], v)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			t.Errorf("%s was removed under the fence", p)
		}
	}
	if lines, err := home.ReadStatus(f.home, "task-1"); err != nil || len(lines) != 1 || !domain.IsReportLine(lines[0]) {
		t.Fatalf("status = %q, %v; want the one report line", lines, err)
	}
	if acked, err := home.NewStore(f.home).ReadAck(&home.Envelope{MessageID: "m1", SenderIdentity: "general", ReceiverRank: home.RankSoldier, TaskID: "task-1"}); err != nil || acked == nil {
		t.Fatalf("ack = %v, %v; want the soldier's ack", acked, err)
	}
}

// TestFencedCommittedReadChild is the body
// TestFencedSoldierReadsCommittedTaskDocuments runs inside the fence: the home
// open the git guard makes before it reads the task's worktree binding.
func TestFencedCommittedReadChild(t *testing.T) {
	dir := os.Getenv(fencedSoldierHome)
	if dir == "" {
		t.Skip("runs only as the child of TestFencedSoldierReadsCommittedTaskDocuments")
	}
	h, err := home.OpenCommitted(dir)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.NewTaskID("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Get(id); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("Get = %v; want the task not found", err)
	}
	fmt.Println(fencedHomeOpened)
}

const fencedHomeOpened = "munsu-test: fenced home opened"

// A soldier's git guard reads its task's binding while a dispatcher commit is
// in flight: journal recovery would write .journal and .lock, which its fence
// refuses, so the read opens the home as last committed. The home sits in a
// temp root, so the roots-included case is the one checked; the git shim
// locks the home itself against a chmod.
func TestFencedSoldierReadsCommittedTaskDocuments(t *testing.T) {
	isolateHuman(t)
	f := newFenceFixture(t)
	if _, err := home.Init(f.home); err != nil {
		t.Fatal(err)
	}
	if _, err := provisionGitShim(f.home); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(f.home, ".journal")
	if err := os.MkdirAll(journal, 0o700); err != nil {
		t.Fatal(err)
	}
	record := `{"txn_id":"t1","scope":"task-x","fence_token":1,"expected_revision":0,"new_revision":1,"items":[]}`
	if err := os.WriteFile(filepath.Join(journal, "task-x.t1.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	r := f.runner("direct-PR")
	if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
		t.Fatal(err)
	}
	argv, err := r.fence.Wrap([]string{os.Args[0], "-test.run=^TestFencedCommittedReadChild$"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), fencedSoldierHome+"="+f.home)
	// The marker, not the exit status alone: a child that skipped or matched no
	// test exits 0 without opening the home.
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), fencedHomeOpened) {
		t.Fatalf("the committed read did not succeed under the fence: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(journal, "task-x.t1.json")); err != nil {
		t.Fatalf("the in-flight record = %v; want it left for its writer", err)
	}
}

// Taking the shim dir off the seat's PATH would let `git` resolve past the git
// guard: a directory the shell cannot enter is skipped. None of the shim dir,
// its parent or the home (here inside a temp root, a writable root) may be
// chmod'ed to hide it; the state dir holding the seat's own files is not above the shim. The home sits directly in /private/tmp, a
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
	// Every chmod above is refused, so the script's status is not theirs.
	script += "exit 0\n"
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
// soldier's home writes are pinned by TestSoldierFenceHomeWrites (fence).
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
// the Human's keychain item, from an empty config dir that is not the seat's,
// made in the home's seat build dir, which no fence root covers.
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
			script := "#!/bin/sh\n" + tc.check + ` && [ -d "$CLAUDE_CONFIG_DIR" ] && [ "${CLAUDE_CONFIG_DIR%/*}" = ` + shQuote(harness.SeatBuildDir(homeDir)) + " ]\n"
			testutil.WriteFakeExecutable(t, filepath.Join(bin, "claude"), script)
			r := NewRunner(Args{ID: "task-1", HarnessFlag: "claude", HomeDir: homeDir})
			r.projPath, r.effectiveMode = t.TempDir(), "direct-PR"
			if err := r.resolveHome(); err != nil {
				t.Fatal(err)
			}
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
