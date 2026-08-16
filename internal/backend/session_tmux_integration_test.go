//go:build integration

package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// TestMain owns the tmux server these tests talk to, for the whole package run.
//
// Two properties come from that, and both are the point:
//
//   - TMUX_TMPDIR moves the default socket into a per-run directory, so the
//     server is this binary's alone — never the developer's, never another
//     job's on the same runner.
//   - The anchor session lives from before the first test to after the last
//     one, so the server never has zero sessions while a test is running. A
//     tmux server exits when its last session dies, and a `new-session` issued
//     into that shutdown fails with "server exited unexpectedly". That is how
//     TestTmux_Alive_UnknownWindow failed on CI (run 31805831782): it created
//     its session in the moment TestTmux_NewWindow's t.Cleanup had just killed
//     the previous — and last — one. With the anchor there is no such moment,
//     so run order and inter-test timing stop being inputs to the verdict.
func TestMain(m *testing.M) {
	if !hasTmux() {
		os.Exit(m.Run())
	}

	// Under /tmp, not the default temp root: the socket path lands in
	// sun_path, which is ~104 bytes on darwin, and macOS's per-user temp
	// directory alone eats most of that.
	dir, err := os.MkdirTemp("/tmp", "munsu-it-tmux-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "backend TestMain: tmux socket dir: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("TMUX_TMPDIR", dir)

	anchor := fmt.Sprintf("munsu-it-anchor-%d", os.Getpid())
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", anchor).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "backend TestMain: tmux new-session -d -s %s: %v: %s\n", anchor, err, strings.TrimSpace(string(out)))
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()

	// kill-server, not kill-session: the private socket has nothing on it worth
	// keeping, and this leaves no server and no socket behind on a dev machine.
	_ = exec.Command("tmux", "kill-server").Run()
	os.RemoveAll(dir)
	os.Exit(code)
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

// TestTmuxServerOutlivesItsLastDisposableSession forces the condition that made
// TestTmux_Alive_UnknownWindow fail on CI instead of waiting for a runner to
// produce it: every disposable session this test owns is gone, so the server
// would have exited if the anchor in TestMain did not exist. Both assertions
// below are red without that anchor — `list-sessions` reports "no server
// running" and the follow-up `new-session` is the one that races the shutdown.
func TestTmuxServerOutlivesItsLastDisposableSession(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	session := newDisposableSession(t)

	// Kill it exactly the way the registered t.Cleanup would, but now, while
	// this test can still observe what the server does next.
	ctx, cancel := context.WithTimeout(context.Background(), tmuxSetupTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", "="+session).CombinedOutput(); err != nil {
		t.Fatalf("tmux kill-session -t =%s: %v: %s", session, err, strings.TrimSpace(string(out)))
	}

	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}").CombinedOutput()
	if err != nil {
		t.Fatalf("the tmux server died with the last disposable session: tmux list-sessions: %v: %s", err, strings.TrimSpace(string(out)))
	}

	// A server that survived must also still take new sessions: this is the
	// call that returned "server exited unexpectedly" on CI.
	newDisposableSession(t)
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
