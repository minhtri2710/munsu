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

// disposableSessionName reserves a tmux session name scoped to this process —
// so it never collides with a developer's own sessions — and registers the
// t.Cleanup that kills it. It does NOT create the session: use it when the code
// under test is what creates the session, and newDisposableSession when the
// test needs one to already exist. Killing a session that was never created is
// a no-op, so the cleanup is safe either way.
func disposableSessionName(t *testing.T) string {
	t.Helper()

	session := fmt.Sprintf("munsu-it-%d-%d", os.Getpid(), sessionSeq.Add(1))

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), tmuxSetupTimeout)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "kill-session", "-t", session).Run()
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
