package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// watcherChildCaseEnv selects which failure the re-exec'd harness body builds,
// and marks the process as that harness.
const watcherChildCaseEnv = "MUNSU_TEST_WATCHER_CHILD_CASE"

const (
	// caseFailAfterSpawn fails between the spawn and the reap: the child is
	// running and the body never gets to consume it.
	caseFailAfterSpawn = "fail-after-spawn"
	// caseDelayedPID lets the body reap and fail, because the child announces
	// itself after the body's PID budget has run out but still inside the
	// cleanup's. This is the path a premature "already reaped" assignment
	// disarms.
	caseDelayedPID = "delayed-pid"
)

// harnessWatcherChildWaits are defaultWatcherChildWaits scaled so a lane can
// afford the delayed-PID case, which by construction costs more than the PID
// budget. Only the harness uses them; every real caller gets the defaults.
//
// The budgets are chosen against one quantity, and it is a skew between two
// clocks rather than any single duration. Write D for pidDelay and P for pid.
// Every deadline below is a soft polling bound: readWatcherChildPID does its
// read before it checks the clock, so a file appearing just past the nominal
// deadline is still accepted on the final iteration, and a budget may be
// exceeded by whatever polling, read and scheduling latency intervenes.
//
// delayed-PID, which needs the child to publish inside a window. Take C for
// when the child begins its deliberate delay and R for when the body enters
// reap, both absolute, so that s = C-R is a difference over a common origin.
// The child publishes at C+D; the body's deadline falls at R+P and the
// cleanup's a further P after that:
//
//	after the body's deadline    D - P + s > 0    ->  3s of slack
//	before the cleanup's         2*P - D - s > 0  ->  3s of slack
//
// Those two slacks sum to P whatever D is, so a budget change that improves one
// takes it from the other. Equal margins are A = B with A = D-P+s and
// B = 2*P-D-s, which is D = 1.5*P - s: it moves with the skew, and D = 1.5*P
// centres the two only at nominal zero skew. That is the schedule set here, and
// what it buys is the best worst case, not equality under load. The first
// absorbs a body that reaches reap late relative to the child, the second a
// child that starts late relative to the body. No ordering of D against P
// establishes either on its own, and an earlier version of this comment claimed
// otherwise.
//
// s is not the child's sleep -- that is D, and it is deliberate. s is only the
// uncontrolled handoff between cmd.Start and the child reaching its first
// statement, set against whatever the parent does in the same interval, and it
// is that handoff the three seconds are compared against. Measured under 20ms
// on an unloaded darwin box, so three seconds is a runner stalling by two
// orders of magnitude.
//
// fail-after-spawn never enters reap, so the cleanup runs on grace and the
// relevant skew is a different one: write s' = C-U, with U the moment the
// cleanup starts polling. D is zero for this case, so the child publishes at C:
//
//	child published before the cleanup gives up    grace - s' > 0  ->  10s of slack
//
// grace is generous because it is nearly free: readWatcherChildPID returns the
// moment the file appears, so a child that publishes inside the budget pays
// none of it. A child that publishes late enough to be absent on the final
// accepted read, or never, costs the full bounded wait and is missed. Widening
// grace therefore widens the window a child can publish in, and in the same
// stroke delays the report that none ever did.
//
// Budgets alone do not make the first delayed-PID relation safe, only unlikely,
// so the test does not rely on them: it asserts which path the harness took.
// If the skew ever wins, the case fails loudly instead of passing on the reap
// it exists to prove cannot happen.
var harnessWatcherChildWaits = watcherChildWaits{
	pid:   6 * time.Second,
	exit:  20 * time.Second,
	grace: 10 * time.Second,
}

// TestWatcherChildReapedWhenTestFails pins the property that the failure
// cleanup, not the test body, is what reaps a watcher child when the test that
// spawned it fails. It is the bounded property the helper actually implements:
// each case builds a child that publishes inside the budgets, and asserts it
// was reaped. A child that publishes outside them is left running by design and
// is not what this test is about.
//
// It has to be a re-exec: the observable is a process that survives a *failed*
// test after its cleanups have run, and a test cannot fail itself and then
// inspect the world afterwards. So the harness body runs in a second copy of
// this binary -- the mechanism startAFKDaemonChild already uses -- and this
// parent reads the PID that copy's watcher child recorded and asks whether it
// is still alive.
//
// A green run is not the observable. Both cases below pass trivially if the
// assertion is deleted, and both fail with a live PID if the reap is disarmed
// before the child has actually been waited for.
func TestWatcherChildReapedWhenTestFails(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kase     string
		pidDelay time.Duration
		hold     time.Duration
		// wantPath is the harness failure that proves the case reached the
		// state it is about, checked before the survivor assertion. Without it
		// a delayed-PID run whose child published early would reap in the body,
		// leave no survivor, and pass -- green, and no longer a test of the
		// cleanup at all.
		wantPath string
	}{
		{
			name:     "failure between the spawn and the reap",
			kase:     caseFailAfterSpawn,
			hold:     2 * time.Second,
			wantPath: "harness: failure injected between the spawn and the reap",
		},
		{
			name:     "child announces itself after the body's PID budget",
			kase:     caseDelayedPID,
			pidDelay: 9 * time.Second,
			hold:     3 * time.Second,
			wantPath: "watcher child never recorded a PID at",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pidPath := filepath.Join(t.TempDir(), "watcher-child.pid")
			out, err := runWatcherChildHarness(t, pidPath, tc.kase, tc.pidDelay, tc.hold)
			if err == nil {
				t.Fatalf("harness body must fail, got success:\n%s", out)
			}
			if !strings.Contains(out, tc.wantPath) {
				t.Fatalf("harness failed by a path this case is not about: want %q in the output, which is the only evidence that the branch this case exists for is the one that ran. Whether anything was left alive is the survivor check below, not this. Widen pidDelay against pid if the child is publishing early. Got:\n%s", tc.wantPath, out)
			}
			// Wait past the child's own delay before judging: under a disarmed
			// cleanup the harness exits before the child has even announced
			// itself, and reading the PID file at that instant would report an
			// absent child rather than the live one this test is looking for.
			pid, ok := readWatcherChildPID(pidPath, tc.pidDelay+6*time.Second)
			if !ok {
				t.Fatalf("harness recorded no watcher child PID at %s, so this case proves nothing:\n%s", pidPath, out)
			}
			if isProcessAlive(pid) {
				_ = killWatcherChild(pid)
				t.Fatalf("watcher child PID %d outlived the failing harness run:\n%s", pid, out)
			}
		})
	}
}

func runWatcherChildHarness(t *testing.T, pidPath, kase string, pidDelay, hold time.Duration) (string, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locating test executable: %v", err)
	}
	args := []string{"-test.run=^TestWatcherChildFailureCleanupProcess$", "-test.count=1", "-test.v"}
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-test.gocoverdir=") {
			args = append(args, arg)
		}
	}
	cmd := exec.Command(executable, args...)
	cmd.Env = append(os.Environ(),
		watcherChildCaseEnv+"="+kase,
		watcherChildPIDEnv+"="+pidPath,
		watcherChildPIDDelayEnv+"="+pidDelay.String(),
		watcherChildHoldEnv+"="+hold.String(),
	)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func killWatcherChild(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

// TestWatcherChildFailureCleanupProcess is the re-exec entry point for the test
// above: it deliberately fails with a watcher child in flight. It is skipped in
// an ordinary run and selected only by runWatcherChildHarness.
func TestWatcherChildFailureCleanupProcess(t *testing.T) {
	kase, pidPath := os.Getenv(watcherChildCaseEnv), os.Getenv(watcherChildPIDEnv)
	if kase == "" || pidPath == "" {
		t.Skip("watcher child failure-cleanup entry point")
	}

	captainHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(captainHome, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	child := armWatcherChildWith(t, pidPath, harnessWatcherChildWaits)
	if err := EnsureWatcher(captainHome, true); err != nil {
		t.Fatalf("EnsureWatcher(true): %v", err)
	}

	switch kase {
	case caseFailAfterSpawn:
		t.Fatal("harness: failure injected between the spawn and the reap")
	case caseDelayedPID:
		child.reap(t)
		t.Fatal("harness: reap was expected to fail on the delayed PID")
	default:
		t.Fatalf("harness: unknown case %q", kase)
	}
}
