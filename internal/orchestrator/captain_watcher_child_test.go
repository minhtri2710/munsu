package orchestrator

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// watcherChildPIDEnv names the file the re-exec'd watcher child records its PID
// in. EnsureWatcher copies os.Environ() into the child, so a t.Setenv in the
// parent test reaches it.
const watcherChildPIDEnv = "MUNSU_TEST_WATCHER_CHILD_PIDFILE"

// The child also honours two delays, so that the failure-cleanup regression
// below can build a state this handshake must survive rather than describe one.
// Both default to zero, which is what every ordinary run gets.
const (
	// watcherChildPIDDelayEnv holds the child back before it announces itself,
	// which is how a body reap is made to fail while a real child is alive.
	watcherChildPIDDelayEnv = "MUNSU_TEST_WATCHER_CHILD_PID_DELAY"
	// watcherChildHoldEnv keeps the child alive after it has announced itself,
	// which is what makes a leaked child observable instead of coincidentally
	// already gone.
	watcherChildHoldEnv = "MUNSU_TEST_WATCHER_CHILD_HOLD"
)

// exitAsWatcherChild reports whether this process is the watcher child
// EnsureWatcher re-exec'd -- "<executable> watch --home <dir>",
// captain_watcher.go:74 -- and, when it is, records the PID the parent test
// reaps by.
//
// The real munsu binary has a watch command; the test binary does not, and
// testing's flag.Parse stops at the non-flag argument "watch", so the child
// inherits no -test.run filter and re-runs the whole package. That re-enters
// TestEnsureWatcher_StartsWhenChildWorkInFlight, which starts a grandchild.
// Measured on darwin without this gate: one process became eight in three
// seconds, each holding cmd.Dir -- a t.TempDir -- as its working directory.
//
// The child cannot be selected with -test.run the way startAFKDaemonChild
// selects TestAFKDaemonChildProcess: production owns the argv, so TestMain is
// the only entry point available.
func exitAsWatcherChild() bool {
	if len(os.Args) < 2 || os.Args[1] != "watch" {
		return false
	}
	sleepEnvDuration(watcherChildPIDDelayEnv)
	if path := os.Getenv(watcherChildPIDEnv); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600)
	}
	sleepEnvDuration(watcherChildHoldEnv)
	return true
}

func sleepEnvDuration(key string) {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil && d > 0 {
		time.Sleep(d)
	}
}

// reapState records how far the parent got in consuming the child, where
// consuming means the bounded PID-file protocol below and nothing stronger.
// The distinction is load-bearing and was the defect in the first version of
// this helper: a single "reaped" bool was set on entry to reap, so a body reap
// that failed -- the missing-PID path below, reachable whenever the child is
// slow -- disarmed the cleanup that exists to catch exactly that. The states
// are written only where they are true.
type reapState int

const (
	// reapNotAttempted: the body never called reap. The parent may well know a
	// child exists -- a nil return from EnsureWatcher means cmd.Start
	// succeeded -- but this field does not record that, so the state alone
	// cannot separate a failure before that call from one after it. The
	// cleanup therefore treats the two alike, which is why it spends only
	// grace here.
	reapNotAttempted reapState = iota
	// reapAttempted: a reap was entered and did not finish waiting. A child was
	// expected and has not been accounted for.
	reapAttempted
	// reapCompleted: awaitProcessExit returned for a PID the child published.
	// That is the convention this helper runs on, not a fact it establishes:
	// awaitProcessExit discards the errors from proc.Wait and Kill and reads
	// any os.FindProcess failure as already gone, so on some platforms and
	// error paths this records that the process was waited for rather than
	// proving it. Nor is it a claim that no child is running: a child that
	// never published a PID within budget is not recorded anywhere and is
	// outside what this state can speak about.
	reapCompleted
)

// watcherChildWaits are the budgets the handshake runs on.
type watcherChildWaits struct {
	// pid is how long a spawned child has to announce itself. It also bounds
	// the cleanup's own wait once the body has tried and failed: the child was
	// proven late, not proven absent, so it is given that budget once more. A
	// child slower than twice this budget escapes unreaped; that is a bound,
	// not a guarantee, and it is the most this handshake can offer while the
	// parent holds a PID rather than a process handle.
	pid time.Duration
	// exit is how long an announced child has to exit before it is killed.
	exit time.Duration
	// grace is the cleanup's wait when the body never reached its reap, where
	// the spawn itself is unconfirmed and a full budget would be spent on every
	// test that fails early for unrelated reasons. A child that announces
	// itself later than this escapes that path unreaped, which is the price of
	// not charging every unrelated failure a pid budget.
	grace time.Duration
}

var defaultWatcherChildWaits = watcherChildWaits{
	pid:   30 * time.Second,
	exit:  30 * time.Second,
	grace: 5 * time.Second,
}

// watcherChild is the parent side of that handshake: the PID file the child
// writes, and how far the test body got in consuming it.
type watcherChild struct {
	pidPath string
	waits   watcherChildWaits
	state   reapState
}

// armWatcherChild directs the next watcher child to record its PID.
//
// The reaping cleanup is registered here, before the spawn, so that a failure
// between the spawn and the explicit reap still gets a reap attempt: a live
// child holds the captain home as its working directory, and on windows that
// blocks the t.TempDir removal -- the same failure the gate above exists to
// prevent, reappearing through its own fix. Registering after the first
// t.TempDir call also orders that attempt ahead of the removal, cleanups being
// run last-registered-first.
//
// An attempt is all it is. This is a PID-file protocol on fixed budgets: a
// failure that never reached the body's reap gives the child waits.grace to
// announce itself, one that reached it and failed gives a further waits.pid,
// and a child slower than that is never seen and is left running. Widening the
// budgets moves that window and does not close it. Closing it needs a handle on
// the process rather than a number the child published, and EnsureWatcher
// discards its *exec.Cmd.
func armWatcherChild(t *testing.T) *watcherChild {
	t.Helper()
	return armWatcherChildWith(t, filepath.Join(t.TempDir(), "watcher-child.pid"), defaultWatcherChildWaits)
}

// armWatcherChildWith is armWatcherChild over an explicit PID path and budgets,
// so the regression harness can place the PID file where its parent process can
// read it and can drive the same code with budgets a test lane can afford.
func armWatcherChildWith(t *testing.T, pidPath string, waits watcherChildWaits) *watcherChild {
	t.Helper()
	child := &watcherChild{pidPath: pidPath, waits: waits}
	t.Setenv(watcherChildPIDEnv, pidPath)
	t.Cleanup(child.reapUnclaimed)
	return child
}

// reap asserts that the child announced itself and then waits for it to exit.
// EnsureWatcher discards its *exec.Cmd, so nothing else will ever wait on that
// process: on unix it stays a zombie for the lifetime of the test binary, and
// on windows it keeps both the captain home it runs in and the test executable
// open against deletion.
//
// The state only reaches reapCompleted once awaitProcessExit has returned --
// on that function's own terms, which discard errors; see reapCompleted. Every
// earlier exit from this function leaves it at reapAttempted, which the cleanup
// treats as work still owed.
func (c *watcherChild) reap(t *testing.T) {
	t.Helper()
	c.state = reapAttempted
	pid, ok := readWatcherChildPID(c.pidPath, c.waits.pid)
	if !ok {
		t.Fatalf("watcher child never recorded a PID at %s", c.pidPath)
	}
	if pid == os.Getpid() {
		t.Fatalf("watcher child reported the test process PID %d", pid)
	}
	exited := awaitProcessExit(pid, c.waits.exit)
	c.state = reapCompleted
	if !exited {
		t.Fatalf("watcher child PID %d did not exit", pid)
	}
}

// reapUnclaimed is the failure-path half of the same job. It makes one bounded
// attempt to reap whatever the child recorded, and reports nothing, so a test
// that has already failed keeps its own failure. A missing PID is tolerated:
// the test may have failed before EnsureWatcher ever ran, and a child that has
// not published within the budget cannot be told apart from one that never
// existed.
//
// It records reapCompleted on the same terms reap does -- only once
// awaitProcessExit has returned -- so the terminal state covers every path that
// consumed the child and no path that merely tried. Nothing reads the state
// after a cleanup has run, so that assignment changes no behaviour today; it
// is there because the two writers of this field disagreeing about what it
// means is precisely how this helper was wrong before.
func (c *watcherChild) reapUnclaimed() {
	if c.state == reapCompleted {
		return
	}
	wait := c.waits.grace
	if c.state == reapAttempted {
		wait = c.waits.pid
	}
	pid, ok := readWatcherChildPID(c.pidPath, wait)
	if !ok || pid == os.Getpid() {
		return
	}
	awaitProcessExit(pid, c.waits.exit)
	c.state = reapCompleted
}

func readWatcherChildPID(pidPath string, within time.Duration) (int, bool) {
	deadline := time.Now().Add(within)
	for {
		if data, err := os.ReadFile(pidPath); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid, true
			}
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// awaitProcessExit waits for pid to exit, killing it at the deadline, and
// reports whether it exited on its own. os.FindProcess never fails on unix and
// opens the process on windows, where a failure means it is already gone.
func awaitProcessExit(pid int, timeout time.Duration) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		_ = proc.Kill()
		<-done
		return false
	}
}
