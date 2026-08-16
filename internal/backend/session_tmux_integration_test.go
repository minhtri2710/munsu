//go:build integration

package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tmuxSetupTimeout bounds the harness's own tmux calls so a hung server fails
// the test instead of hanging until the job timeout.
const tmuxSetupTimeout = 10 * time.Second

// sessionSeq makes disposable session names unique within a test binary.
var sessionSeq atomic.Int64

// tmuxAnchorSession is the session that owns this test binary's tmux server for
// the whole package run. See TestMain.
var tmuxAnchorSession = fmt.Sprintf("munsu-it-anchor-%d", os.Getpid())

// TestMain gives this package its own tmux server and owns its whole lifetime:
// one server started before any test runs, one kill after the last one.
//
// tmux shuts a server down when its last session goes away. Every tmux test
// here creates a session and kills it in t.Cleanup, so on the shared default
// socket the kill at the end of one test could be the one that removed the
// server's last session — and the next test's `new-session` then raced a server
// in the middle of exiting ("server exited unexpectedly", BEO-82). BEO-26 gave
// each test ownership of its *session*, which moved that shared state rather
// than removing it; the server was still nobody's.
//
// Two changes close it together. TMUX_TMPDIR puts the socket in a directory
// only this process knows, so no developer session, no other test binary and no
// stray `tmux` command shares this server. The anchor session then holds it
// open for the entire package, so no test cleanup can ever remove the last
// session. Order-dependence and shared-state dependence both stop existing
// rather than getting less likely.
func TestMain(m *testing.M) {
	code, err := runWithOwnedTmuxServer(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backend integration TestMain: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// runWithOwnedTmuxServer runs the suite against a package-owned tmux server. It
// returns the suite's exit code rather than calling os.Exit so its teardown
// defers actually run.
func runWithOwnedTmuxServer(m *testing.M) (int, error) {
	if !hasTmux() {
		return m.Run(), nil
	}

	// Under /tmp, not os.TempDir(): a unix socket path is capped near 104
	// bytes, and darwin's per-user temp directory alone spends most of that.
	dir, err := os.MkdirTemp("/tmp", "munsu-it-tmux-")
	if err != nil {
		return 0, fmt.Errorf("creating the tmux socket directory: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := os.Setenv("TMUX_TMPDIR", dir); err != nil {
		return 0, fmt.Errorf("pointing tmux at the private socket directory: %w", err)
	}
	// A tmux client that thinks it is nested refuses to create sessions.
	if err := os.Unsetenv("TMUX"); err != nil {
		return 0, fmt.Errorf("clearing TMUX: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), tmuxSetupTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", tmuxAnchorSession).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("starting the package tmux server: %v: %s", err, strings.TrimSpace(string(out)))
	}
	defer func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), tmuxSetupTimeout)
		defer killCancel()
		_ = exec.CommandContext(killCtx, "tmux", "kill-server").Run()
	}()

	return m.Run(), nil
}

// tmuxSessions lists the sessions on this package's tmux server. A server that
// is gone is a failure, not an empty list: the anchor session means there is
// always at least one session to report.
func tmuxSessions(t *testing.T) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), tmuxSetupTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}").CombinedOutput()
	if err != nil {
		t.Fatalf("tmux list-sessions: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.Fields(string(out))
}

// killSession kills one session on this package's tmux server. The "=" target
// prefix forces an exact name match, as in disposableSessionName.
func killSession(t *testing.T, session string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), tmuxSetupTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", "="+session).CombinedOutput(); err != nil {
		t.Fatalf("tmux kill-session -t =%s: %v: %s", session, err, strings.TrimSpace(string(out)))
	}
}

// TestTmux_AnchorKeepsServerAliveAcrossSessionChurn forces the condition that
// made TestTmux_Alive_UnknownWindow flaky — a test kills the session it created
// and the next test immediately creates one — and pins the invariant that makes
// the race impossible: the session count never reaches zero, so the server is
// never in the middle of exiting when the next `new-session` arrives.
//
// Without the anchor this fails on the first iteration rather than
// occasionally: killing the only session leaves no server for list-sessions to
// talk to at all.
func TestTmux_AnchorKeepsServerAliveAcrossSessionChurn(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	for i := 0; i < 5; i++ {
		session := newDisposableSession(t)
		killSession(t, session)

		sessions := tmuxSessions(t)
		if !slices.Contains(sessions, tmuxAnchorSession) {
			t.Fatalf("iteration %d: anchor session %s missing from %v; the server's lifetime is not owned by this package",
				i, tmuxAnchorSession, sessions)
		}
	}
}

// disposableSessionName reserves a tmux session name scoped to this process —
// so it never collides with a developer's own sessions — and registers the
// t.Cleanup that kills it. It does NOT create the session: use it when the code
// under test is what creates the session, and newDisposableSession when the
// test needs one to already exist. The cleanup is safe either way: killing a
// name that was never created fails harmlessly, and the "=" target prefix
// forces tmux to match that exact name — without it tmux falls back to prefix
// matching, so cleaning up an uncreated "munsu-it-<pid>-1" would kill a live
// "munsu-it-<pid>-11" belonging to another test.
func disposableSessionName(t *testing.T) string {
	t.Helper()

	session := fmt.Sprintf("munsu-it-%d-%d", os.Getpid(), sessionSeq.Add(1))

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), tmuxSetupTimeout)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "kill-session", "-t", "="+session).Run()
	})

	return session
}

// newDisposableSession starts a dedicated detached tmux session — creating a
// tmux server if none is running — and returns its name. The name is scoped to
// this process so it never collides with a developer's own sessions, and
// t.Cleanup kills it so nothing leaks into another test or the dev machine.
func newDisposableSession(t *testing.T) string {
	t.Helper()

	session := disposableSessionName(t)

	ctx, cancel := context.WithTimeout(context.Background(), tmuxSetupTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", session).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session -d -s %s: %v: %s", session, err, strings.TrimSpace(string(out)))
	}

	return session
}

// TestTmux_NewWindow creates its own tmux server/session so it runs for real
// wherever tmux is installed, instead of borrowing whatever session happens to
// be open (and skipping when none is).
func TestTmux_NewWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	session := newDisposableSession(t)
	tk := &TmuxBackend{}

	wid, err := tk.NewWindow(session, "munsu-test")
	if err != nil {
		t.Fatal(err)
	}
	if wid == "" {
		t.Fatal("NewWindow returned empty window ID")
	}

	// Verify the window is alive
	if alive, _ := tk.CheckAlive(wid); !alive {
		t.Fatal("NewWindow window not alive after creation")
	}

	// Clean up
	if err := tk.Teardown(wid); err != nil {
		t.Errorf("Teardown failed: %v", err)
	}

	// Verify it's gone
	if alive, _ := tk.CheckAlive(wid); alive {
		t.Error("window still alive after Teardown")
	}
}

func TestTmux_Alive_UnknownWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	// CheckAlive talks to the server, so an unknown window is only
	// distinguishable from a dead server when a server is running — start our
	// own rather than depending on one already being up.
	newDisposableSession(t)

	tk := &TmuxBackend{}
	// An unknown window should return false (ErrPaneNotFound)
	if alive, err := tk.CheckAlive("@99999"); alive || !errors.Is(err, ErrPaneNotFound) {
		t.Errorf("CheckAlive('@99999') = %v, %v; want false + ErrPaneNotFound", alive, err)
	}
}

func TestTmux_NewWindow_SessionAutoCreated(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	// The session must NOT exist beforehand — that is the point of the test —
	// so reserve a per-process name and let NewWindow create it. Teardown below
	// only kills the window; the auto-created session is the harness's to clean
	// up, and the registered t.Cleanup is what keeps it off the dev machine.
	session := disposableSessionName(t)
	// With F1.1, a nonexistent session is auto-created
	wid, err := tk.NewWindow(session, "test")
	if err != nil {
		t.Fatalf("NewWindow should auto-create session, got error: %v", err)
	}
	if wid == "" {
		t.Fatal("NewWindow returned empty window ID")
	}
	// Verify the window is alive
	if alive, _ := tk.CheckAlive(wid); !alive {
		t.Fatal("NewWindow window not alive after session auto-create")
	}
	// Clean up
	if err := tk.Teardown(wid); err != nil {
		t.Errorf("Teardown failed: %v", err)
	}
}

func TestTmux_SendKeys_NoWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	err := tk.SendKeys("@99999", "echo hello")
	if err == nil {
		t.Fatal("expected error for unknown window")
	}
}

func TestTmux_Capture_NoWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	_, err := tk.Capture("@99999", 10)
	if err == nil {
		t.Fatal("expected error for unknown window")
	}
}

func TestTmux_Teardown_SuppressesErrors(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	// Teardown on a window that doesn't exist should not error
	if err := tk.Teardown("@99999"); err != nil {
		t.Errorf("Teardown on unknown window should not error: %v", err)
	}
}
