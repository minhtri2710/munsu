package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hasTmux reports whether tmux is available on PATH.
func hasTmux() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// fakeExecutables writes an executable stub for each name into a temp dir and
// returns that dir, for PATH-controlled capability-verification tests.
func fakeExecutables(t *testing.T, names ...string) string {
	t.Helper()
	fakeBin := t.TempDir()
	for _, name := range names {
		path := filepath.Join(fakeBin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return fakeBin
}

func TestTmuxBin_Found(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}
	path, err := tmuxBin()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("tmuxBin() returned empty path")
	}
	if !strings.Contains(path, "tmux") {
		t.Errorf("tmuxBin() = %q, expected path containing 'tmux'", path)
	}
}

func TestTmuxBin_NotFound(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	_, err := tmuxBin()
	if err == nil {
		t.Fatal("expected error when tmux is not on PATH")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefault_IsDeletedFromOperationPath(t *testing.T) {
	// Default() is removed from the operation path — no auto-detection exists.
	// Resolve with an empty requested identity must fail closed even when every
	// adapter is present.
	fakeBin := fakeExecutables(t, "tmux", "herdr")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)
	t.Setenv("TMUX", "/tmp/tmux-socket")
	t.Setenv("HERDR_ENV", "1")

	if _, _, err := Resolve(t.TempDir(), ""); err == nil {
		t.Fatal("Resolve('') must fail CLOSED — no auto-detection (Default() is gone)")
	}
}

func TestSelect_RejectsUnknownNames(t *testing.T) {
	unknown := []string{"foobar", ""}
	for _, name := range unknown {
		_, err := constructBackend(name)
		if err == nil {
			t.Errorf("constructBackend(%q) expected error, got nil", name)
		} else if !strings.Contains(err.Error(), "unknown session backend") {
			t.Errorf("constructBackend(%q) unexpected error: %v", name, err)
		}
	}
}
func TestSelect_ReturnsKnownBackendsWhenRequestedBinaryPresent(t *testing.T) {
	known := []string{"tmux", "herdr", "zellij", "cmux", "orca"}
	fakeBin := fakeExecutables(t, known...)
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	for _, name := range known {
		bk, err := constructBackend(name)
		if err != nil {
			t.Errorf("constructBackend(%q) with %q on PATH: %v", name, name, err)
		}
		if bk == nil {
			t.Errorf("constructBackend(%q) returned nil backend", name)
		}
	}
}

func TestSelect_FailsClosedWhenRequestedBinaryAbsent(t *testing.T) {
	known := []string{"tmux", "herdr", "zellij", "cmux", "orca"}
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")

	for _, name := range known {
		bk, err := constructBackend(name)
		if err == nil {
			t.Errorf("constructBackend(%q) succeeded (%T) with %q absent from PATH — must fail closed", name, bk, name)
		}
		if !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("constructBackend(%q) unexpected error: %v", name, err)
		}
	}
}

func TestResolve_EmptyIdentityFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	// Even with env markers set, an empty requested identity must NOT auto-detect.
	t.Setenv("TMUX", "/tmp/tmux-socket")
	t.Setenv("HERDR_ENV", "1")
	if _, _, err := Resolve(tmpDir, ""); err == nil {
		t.Fatal("Resolve with empty identity must fail CLOSED, never auto-detect from env/PATH")
	} else if !strings.Contains(err.Error(), "no session backend identity") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolve_AutoIdentityFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	if _, _, err := Resolve(tmpDir, "auto"); err == nil {
		t.Fatal("Resolve('auto') must fail CLOSED (auto-detection is gone)")
	} else if !strings.Contains(err.Error(), "no session backend identity") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolve_ExplicitIdentityIgnoresEnvAndConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	// A config/backend file must NOT be read by Resolve (composition translates
	// config once into the typed snapshot before operation start).
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "/tmp/tmux-xxx")
	t.Setenv("HERDR_ENV", "1")

	// Controlled PATH: the requested binary must be verifiably present, and
	// only the explicit identity counts (no real tmux install required).
	fakeBin := fakeExecutables(t, "tmux")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	bk, name, err := Resolve(tmpDir, "tmux")
	if err != nil {
		t.Fatal(err)
	}
	if name != "tmux" {
		t.Errorf("name = %q, want tmux (only the explicit identity counts)", name)
	}
	tkb, ok := bk.(*TmuxBackend)
	if !ok {
		t.Fatalf("Resolve('tmux') returned %T, want *TmuxBackend", bk)
	}
	if tkb.Tag == "" {
		t.Error("tmux backend tag should be set from homeDir workspace labeling")
	}
}

func TestResolve_UnknownExplicitIdentityFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	if _, _, err := Resolve(tmpDir, "nonexistent"); err == nil {
		t.Fatal("expected error for unknown backend name")
	} else if !strings.Contains(err.Error(), "unknown session backend") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestResolve_FailsClosedWhenRequestedBinaryAbsent verifies the shared
// verified-construction path: Resolve must FAIL CLOSED at resolution when the
// requested capability's binary is absent from PATH — no adapter is returned
// and nothing is deferred to first exec.
func TestResolve_FailsClosedWhenRequestedBinaryAbsent(t *testing.T) {
	known := []string{"tmux", "herdr", "zellij", "cmux", "orca"}
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")

	for _, name := range known {
		bk, gotName, err := Resolve(t.TempDir(), name)
		if err == nil {
			t.Errorf("Resolve(%q) succeeded (%T) with %q absent from PATH — must fail closed", name, bk, name)
			continue
		}
		if bk != nil {
			t.Errorf("Resolve(%q) returned a backend (%T) for an absent capability — must return nil", name, bk)
		}
		if gotName != "" {
			t.Errorf("Resolve(%q) returned name %q on failure, want empty", name, gotName)
		}
		if !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("Resolve(%q) unexpected error: %v", name, err)
		}
	}
}

// TestResolve_SucceedsWithControlledPATH verifies the shared verified-construction
// path succeeds when the requested binary is present on a controlled PATH, and
// that the workspace-label binding (tmux Tag) is applied from homeDir — layout
// layered on the verified base, never selection.
func TestResolve_SucceedsWithControlledPATH(t *testing.T) {
	known := []string{"tmux", "herdr", "zellij", "cmux", "orca"}
	fakeBin := fakeExecutables(t, known...)
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	for _, name := range known {
		homeDir := t.TempDir()
		bk, gotName, err := Resolve(homeDir, name)
		if err != nil {
			t.Errorf("Resolve(%q) with %q on PATH: %v", name, name, err)
			continue
		}
		if gotName != name {
			t.Errorf("Resolve(%q) name = %q, want %q", name, gotName, name)
		}
		switch name {
		case "tmux":
			tb, ok := bk.(*TmuxBackend)
			if !ok {
				t.Errorf("Resolve('tmux') returned %T, want *TmuxBackend", bk)
			} else if tb.Tag != Hometag(homeDir) {
				t.Errorf("Resolve('tmux') Tag = %q, want %q (homeDir workspace labeling)", tb.Tag, Hometag(homeDir))
			}
		case "herdr":
			if _, ok := bk.(*HerdrBackend); !ok {
				t.Errorf("Resolve('herdr') returned %T, want *HerdrBackend", bk)
			}
		case "zellij":
			if _, ok := bk.(*ZellijBackend); !ok {
				t.Errorf("Resolve('zellij') returned %T, want *ZellijBackend", bk)
			}
		case "cmux":
			if _, ok := bk.(*CmuxBackend); !ok {
				t.Errorf("Resolve('cmux') returned %T, want *CmuxBackend", bk)
			}
		case "orca":
			if _, ok := bk.(*OrcaBackend); !ok {
				t.Errorf("Resolve('orca') returned %T, want *OrcaBackend", bk)
			}
		}
	}
}

func TestTmuxWindowNameUsesCompleteCallerLabel(t *testing.T) {
	tk := &TmuxBackend{Tag: "home"}
	if got := tk.windowName("mu-api-w1"); got != "mu-api-w1" {
		t.Fatalf("windowName() = %q, want complete caller label", got)
	}
}
func TestTmux_Alive_TmuxNotOnPath(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	tk := &TmuxBackend{}
	if alive, _ := tk.CheckAlive("@0"); alive {
		t.Error("CheckAlive() returned true when tmux is not on PATH")
	}
}

// fakeFailingTmux writes a tmux stub that prints msg and exits non-zero, then
// puts it first on PATH for the duration of the test. It makes the failure mode
// of the tmux CLI deterministic — a real server that dies at exactly the right
// moment is not reproducible.
func fakeFailingTmux(t *testing.T, msg string) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %q >&2\nexit 1\n", msg)
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestTmux_Alive_ServerFailureIsNotPaneNotFound pins the classification
// invariant in CheckAlive: only an exact per-target absence is authoritative
// absence (ErrPaneNotFound). Server/socket failures are operational errors — a
// caller must never read "the server is gone" as "this pane is gone".
func TestTmux_Alive_ServerFailureIsNotPaneNotFound(t *testing.T) {
	operational := []struct {
		name string
		msg  string
	}{
		{"no server running", "no server running on /tmp/tmux-1000/default"},
		{"connection refused", "error connecting to /tmp/tmux-1000/default (Connection refused)"},
		// tmux 3.x wording; `strings` on a 3.7b binary has no "lost server".
		{"server exited unexpectedly", "server exited unexpectedly"},
		// Legacy wording, emitted by tmux <= 2.x. Kept so the classification
		// still holds for a stale server binary on an old host.
		{"lost server (legacy tmux <= 2.x)", "lost server"},
		{"permission denied", "error connecting to /tmp/tmux-1000/default (Permission denied)"},
		// The four cases above avoid every isTmuxTargetAbsent token by
		// accident, so on their own they cannot tell a strict allowlist from a
		// broad one. These two say the quiet part out loud: CheckAlive's doc
		// comment promises generic/broad "not found" is operational, so a
		// bare "not found" token must never be read as authoritative absence.
		{"generic not found", "not found"},
		{"tmux binary missing from a wrapper's PATH", "tmux: command not found"},
	}
	for _, tc := range operational {
		t.Run(tc.name, func(t *testing.T) {
			fakeFailingTmux(t, tc.msg)

			tk := &TmuxBackend{}
			alive, err := tk.CheckAlive("@99999")
			if alive {
				t.Error("CheckAlive returned true for a failing tmux server")
			}
			if err == nil {
				t.Fatal("CheckAlive returned nil error for a failing tmux server")
			}
			if errors.Is(err, ErrPaneNotFound) {
				t.Errorf("CheckAlive misclassified server failure %q as ErrPaneNotFound", tc.msg)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error %v does not carry the tmux message %q", err, tc.msg)
			}
		})
	}

	// Counterpart: the same harness must still yield ErrPaneNotFound for an
	// exact per-target absence, so the assertions above are discrimination, not
	// a blanket "never ErrPaneNotFound". One case per absence wording tmux can
	// emit, so narrowing the classification — the dangerous direction, where a
	// dead pane starts reading as an operational error and the task looks alive
	// — turns this test red.
	absent := []struct {
		name string
		msg  string
	}{
		{"can't find window", "can't find window: @99999"},
		{"can't find pane", "can't find pane: %9"},
		{"can't find session", "can't find session: nope"},
		{"no such window", "no such window: @99999"},
		{"no such pane", "no such pane: %9"},
		{"no such session", "no such session: nope"},
	}
	for _, tc := range absent {
		t.Run("target absent is ErrPaneNotFound: "+tc.name, func(t *testing.T) {
			fakeFailingTmux(t, tc.msg)

			tk := &TmuxBackend{}
			alive, err := tk.CheckAlive("@99999")
			if alive {
				t.Error("CheckAlive returned true for an absent window")
			}
			if !errors.Is(err, ErrPaneNotFound) {
				t.Errorf("CheckAlive err = %v for %q, want ErrPaneNotFound", err, tc.msg)
			}
		})
	}
}

// TestTmux_Backend_NotFound tests every method returns an error when tmux is missing.
func TestTmux_Backend_NotFound(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	tk := &TmuxBackend{}

	t.Run("NewWindow", func(t *testing.T) {
		_, err := tk.NewWindow("s", "n")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("SendKeys", func(t *testing.T) {
		err := tk.SendKeys("@0", "text")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Capture", func(t *testing.T) {
		_, err := tk.Capture("@0", 10)
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Alive", func(t *testing.T) {
		if alive, _ := tk.CheckAlive("@0"); alive {
			t.Error("CheckAlive returned true when tmux is not on PATH")
		}
	})

	t.Run("Teardown", func(t *testing.T) {
		err := tk.Teardown("@0")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// FakeBackend — test double for the Backend interface
// ---------------------------------------------------------------------------

// fakeBackend implements Backend for testing purposes.
// Tracks windows, captures, and errors in memory.
type fakeBackend struct {
	windows     map[string]bool   // windowID -> alive
	captures    map[string]string // windowID -> captured content
	newWindowFn func(session, name string) (string, error)
	sendKeysFn  func(windowID, text string) error
	captureFn   func(windowID string, lines int) (string, error)
	aliveFn     func(windowID string) bool
	teardownFn  func(windowID string) error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		windows:  make(map[string]bool),
		captures: make(map[string]string),
	}
}

func (f *fakeBackend) NewWindow(session, name string) (string, error) {
	if f.newWindowFn != nil {
		return f.newWindowFn(session, name)
	}
	wid := session + "/" + name
	f.windows[wid] = true
	return wid, nil
}

func (f *fakeBackend) SendKeys(windowID, text string) error {
	if f.sendKeysFn != nil {
		return f.sendKeysFn(windowID, text)
	}
	if !f.windows[windowID] {
		return fmt.Errorf("window %s not found", windowID)
	}
	return nil
}

func (f *fakeBackend) Capture(windowID string, lines int) (string, error) {
	if f.captureFn != nil {
		return f.captureFn(windowID, lines)
	}
	if !f.windows[windowID] {
		return "", fmt.Errorf("window %s not found", windowID)
	}
	if c, ok := f.captures[windowID]; ok {
		return c, nil
	}
	return "", nil
}

func (f *fakeBackend) CheckAlive(windowID string) (bool, error) {
	if f.aliveFn != nil {
		return f.aliveFn(windowID), nil
	}
	return f.windows[windowID], nil
}

func (f *fakeBackend) Teardown(windowID string) error {
	if f.teardownFn != nil {
		return f.teardownFn(windowID)
	}
	delete(f.windows, windowID)
	return nil
}

// ---------------------------------------------------------------------------
// FakeBackend contract tests
// ---------------------------------------------------------------------------

func TestFakeBackend_NewWindow(t *testing.T) {
	f := newFakeBackend()
	wid, err := f.NewWindow("munsu", "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	if wid == "" {
		t.Fatal("NewWindow returned empty window ID")
	}
	if !f.windows[wid] {
		t.Error("window should be recorded as alive")
	}
}

func TestFakeBackend_AliveAfterNewWindow(t *testing.T) {
	f := newFakeBackend()
	wid, _ := f.NewWindow("s", "n")
	if alive, _ := f.CheckAlive(wid); !alive {
		t.Error("CheckAlive should return true after NewWindow")
	}
}

func TestFakeBackend_AliveUnknown(t *testing.T) {
	f := newFakeBackend()
	if alive, _ := f.CheckAlive("@nonexistent"); alive {
		t.Error("CheckAlive should return false for unknown window")
	}
}

func TestFakeBackend_TeardownRemoves(t *testing.T) {
	f := newFakeBackend()
	wid, _ := f.NewWindow("s", "n")
	f.Teardown(wid)
	if alive, _ := f.CheckAlive(wid); alive {
		t.Error("CheckAlive should return false after Teardown")
	}
}

func TestFakeBackend_SendKeysToUnknown(t *testing.T) {
	f := newFakeBackend()
	err := f.SendKeys("@nonexistent", "echo hi")
	if err == nil {
		t.Fatal("expected error for unknown window")
	}
}

func TestFakeBackend_CaptureToUnknown(t *testing.T) {
	f := newFakeBackend()
	_, err := f.Capture("@nonexistent", 10)
	if err == nil {
		t.Fatal("expected error for unknown window")
	}
}

func TestFakeBackend_CaptureAfterSend(t *testing.T) {
	f := newFakeBackend()
	wid, _ := f.NewWindow("s", "n")
	f.captures[wid] = "output line 1\noutput line 2\n"

	out, err := f.Capture(wid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if out != "output line 1\noutput line 2\n" {
		t.Errorf("captured = %q, want %q", out, "output line 1\noutput line 2\n")
	}
}

func TestFakeBackend_Lifecycle(t *testing.T) {
	f := newFakeBackend()

	// Create
	wid, err := f.NewWindow("munsu", "agent")
	if err != nil {
		t.Fatal(err)
	}

	// Use
	if err := f.SendKeys(wid, "cd /tmp && go test"); err != nil {
		t.Fatal(err)
	}
	f.captures[wid] = "ok\n"
	out, err := f.Capture(wid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok\n" {
		t.Errorf("capture = %q, want %q", out, "ok\n")
	}

	// Teardown
	if err := f.Teardown(wid); err != nil {
		t.Fatal(err)
	}
	if alive, _ := f.CheckAlive(wid); alive {
		t.Error("should not be alive after teardown")
	}
}

func TestFakeBackend_CustomNewWindowFn(t *testing.T) {
	f := newFakeBackend()
	f.newWindowFn = func(session, name string) (string, error) {
		return "", fmt.Errorf("custom error")
	}
	_, err := f.NewWindow("s", "n")
	if err == nil || !strings.Contains(err.Error(), "custom error") {
		t.Errorf("expected 'custom error', got %v", err)
	}
}

func TestFakeBackend_CustomSendKeysFn(t *testing.T) {
	f := newFakeBackend()
	f.sendKeysFn = func(windowID, text string) error {
		return fmt.Errorf("send failed")
	}
	err := f.SendKeys("@w0", "text")
	if err == nil || !strings.Contains(err.Error(), "send failed") {
		t.Errorf("expected 'send failed', got %v", err)
	}
}

func TestFakeBackend_BackendSelectStaysSame(t *testing.T) {
	// Verify that Select verifies and returns the expected adapter types for
	// explicitly requested backends when their binaries are on PATH.
	fakeBin := fakeExecutables(t, "tmux", "herdr", "zellij")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	bk, err := constructBackend("tmux")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bk.(*TmuxBackend); !ok {
		t.Errorf("constructBackend('tmux') returned %T, want *TmuxBackend", bk)
	}

	bk, err = constructBackend("herdr")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bk.(*HerdrBackend); !ok {
		t.Errorf("constructBackend('herdr') returned %T, want *HerdrBackend", bk)
	}

	bk, err = constructBackend("zellij")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bk.(*ZellijBackend); !ok {
		t.Errorf("constructBackend('zellij') returned %T, want *ZellijBackend", bk)
	}
}

// ---------------------------------------------------------------------------
// HerdrBackend tests (PATH-based mock)
// ---------------------------------------------------------------------------

// TestHerdrBackend_NotFound tests that HerdrBackend methods fail gracefully
// when herdr is not on PATH.
func TestHerdrBackend_NotFound(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")

	h := &HerdrBackend{}

	t.Run("NewWindow", func(t *testing.T) {
		_, err := h.NewWindow("s", "n")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("SendKeys", func(t *testing.T) {
		err := h.SendKeys("@0", "text")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Capture", func(t *testing.T) {
		_, err := h.Capture("@0", 10)
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Alive", func(t *testing.T) {
		if alive, _ := h.CheckAlive("@0"); alive {
			t.Error("CheckAlive returned true when herdr is not on PATH")
		}
	})

	t.Run("Teardown", func(t *testing.T) {
		err := h.Teardown("@0")
		// Teardown suppresses "not found" errors — nil is expected
		if err != nil {
			t.Logf("Teardown returned error (expected with no herdr): %v", err)
		}
	})
}

func TestTmuxBackendFindOrCreateWindowRefusesDuplicateWindows(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "tmux")
	script := "#!/bin/sh\ncase \"$1\" in\nhas-session) exit 0;;\nlist-windows) printf 'dup\\t@1\\ndup\\t@2\\n';;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)
	_, err := (&TmuxBackend{}).FindOrCreateWindow("s", "dup")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("FindOrCreateWindow error = %v, want ambiguous", err)
	}
}
