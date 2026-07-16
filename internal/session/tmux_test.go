package session

import (
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

func TestDefault_ReturnsTmux(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	t.Setenv("TMUX", "/tmp/tmux-test")
	b := Default()
	if _, ok := b.(*TmuxBackend); !ok {
		t.Errorf("Default() with $TMUX set returned %T, want *TmuxBackend", b)
	}
}

func TestDefault_ReturnsHerdr(t *testing.T) {
	// No TMUX, but HERDR_ENV set
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "1")
	b := Default()
	if _, ok := b.(*HerdrBackend); !ok {
		t.Errorf("Default() with $HERDR_ENV set returned %T, want *HerdrBackend", b)
	}
}

func TestDefault_ReturnsNilWhenNoBackend(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	t.Setenv("TMUX", "")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")
	b := Default()
	if b != nil {
		t.Errorf("Default() returned %T, want nil when no backend available", b)
	}
}

func TestDefault_ColdStartPrefersTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "")

	// Create a temp directory with fake tmux and herdr binaries on PATH
	fakeBin := t.TempDir()
	for _, name := range []string{"tmux", "herdr"} {
		path := filepath.Join(fakeBin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	b := Default()
	if _, ok := b.(*TmuxBackend); !ok {
		t.Errorf("Default() with both binaries on PATH but no env returned %T, want *TmuxBackend", b)
	}
}

func TestSelect_RejectsUnknownNames(t *testing.T) {
	unknown := []string{"zellij", "cmux", "orca", "foobar", ""}
	for _, name := range unknown {
		_, err := Select(name)
		if err == nil {
			t.Errorf("Select(%q) expected error, got nil", name)
		} else if !strings.Contains(err.Error(), "unknown session backend") {
			t.Errorf("Select(%q) unexpected error: %v", name, err)
		}
	}
}

func TestSelect_ReturnsKnownBackends(t *testing.T) {
	known := []string{"tmux", "herdr"}
	for _, name := range known {
		bk, err := Select(name)
		if err != nil {
			t.Errorf("Select(%q) unexpected error: %v", name, err)
		}
		if bk == nil {
			t.Errorf("Select(%q) returned nil backend", name)
		}
	}
}

func TestResolve_ErrorsOnUnknownBackendConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("zellij\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := Resolve(tmpDir, "")
	if err == nil {
		t.Fatal("expected error for unknown backend name in config")
	} else if !strings.Contains(err.Error(), "unknown session backend") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolve_Precedence(t *testing.T) {
	t.Run("override beats config", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "config")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			t.Fatal(err)
		}
		// Write config saying "herdr" but override says "tmux"
		if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
			t.Fatal(err)
		}
		bk, name, err := Resolve(tmpDir, "tmux")
		if err != nil {
			t.Fatal(err)
		}
		if name != "tmux" {
			t.Errorf("Resolve with override 'tmux' returned name %q, want 'tmux'", name)
		}
		if _, ok := bk.(*TmuxBackend); !ok {
			t.Errorf("Resolve with override 'tmux' returned %T, want *TmuxBackend", bk)
		}
	})

	t.Run("config beats default", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "config")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			t.Fatal(err)
		}
		// Write config saying "herdr" with no override
		if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
			t.Fatal(err)
		}
		bk, name, err := Resolve(tmpDir, "")
		if err != nil {
			t.Fatal(err)
		}
		if name != "herdr" {
			t.Errorf("Resolve with config 'herdr' returned name %q, want 'herdr'", name)
		}
		if _, ok := bk.(*HerdrBackend); !ok {
			t.Errorf("Resolve with config 'herdr' returned %T, want *HerdrBackend", bk)
		}
	})

	t.Run("default is tmux when TMUX env set", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("TMUX", "/tmp/tmux-xxx/default")
		t.Setenv("HERDR_ENV", "")
		bk, name, err := Resolve(tmpDir, "")
		if err != nil {
			t.Fatal(err)
		}
		if name != "tmux" {
			t.Errorf("Resolve with $TMUX returned name %q, want 'tmux'", name)
		}
		if _, ok := bk.(*TmuxBackend); !ok {
			t.Errorf("Resolve with $TMUX returned %T, want *TmuxBackend", bk)
		}
	})

	t.Run("HERDR_ENV selects herdr when no override and no config", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("HERDR_ENV", "1")
		bk, name, err := Resolve(tmpDir, "")
		if err != nil {
			t.Fatal(err)
		}
		if name != "herdr" {
			t.Errorf("Resolve with HERDR_ENV returned name %q, want 'herdr'", name)
		}
		if _, ok := bk.(*HerdrBackend); !ok {
			t.Errorf("Resolve with HERDR_ENV returned %T, want *HerdrBackend", bk)
		}
	})

	t.Run("config pin beats active TMUX", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "config")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			t.Fatal(err)
		}
		// Write config saying "herdr" but set active TMUX
		if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMUX", "/tmp/tmux-xxx")
		t.Setenv("HERDR_ENV", "")

		bk, name, err := Resolve(tmpDir, "")
		if err != nil {
			t.Fatal(err)
		}
		if name != "herdr" {
			t.Errorf("Resolve with config 'herdr' and active $TMUX returned name %q, want 'herdr'", name)
		}
		if _, ok := bk.(*HerdrBackend); !ok {
			t.Errorf("Resolve with config 'herdr' and active $TMUX returned %T, want *HerdrBackend", bk)
		}
	})

	t.Run("config pin beats active HERDR_ENV", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "config")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			t.Fatal(err)
		}
		// Write config saying "tmux" but set active HERDR_ENV
		if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("tmux\n"), 0644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HERDR_ENV", "1")

		bk, name, err := Resolve(tmpDir, "")
		if err != nil {
			t.Fatal(err)
		}
		if name != "tmux" {
			t.Errorf("Resolve with config 'tmux' and active $HERDR_ENV returned name %q, want 'tmux'", name)
		}
		if _, ok := bk.(*TmuxBackend); !ok {
			t.Errorf("Resolve with config 'tmux' and active $HERDR_ENV returned %T, want *TmuxBackend", bk)
		}
	})

	t.Run("override beats config and context", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "config")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			t.Fatal(err)
		}
		// Write config saying "herdr" and set active TMUX
		if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMUX", "/tmp/tmux-xxx")

		// Override (--backend flag) beats both config and context
		bk, name, err := Resolve(tmpDir, "tmux")
		if err != nil {
			t.Fatal(err)
		}
		if name != "tmux" {
			t.Errorf("Resolve with override 'tmux' returned name %q, want 'tmux'", name)
		}
		if _, ok := bk.(*TmuxBackend); !ok {
			t.Errorf("Resolve with override 'tmux' returned %T, want *TmuxBackend", bk)
		}
	})
}

// TestTmux_NewWindow requires an active tmux server.
func TestTmux_NewWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	// Check there's a tmux server running
	check := exec.Command("tmux", "list-sessions")
	if err := check.Run(); err != nil {
		t.Skip("no tmux server running")
	}

	tk := &TmuxBackend{}

	// Try creating a window in the first available session
	sessionsOut, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		t.Fatal(err)
	}
	sessions := strings.Fields(string(sessionsOut))
	if len(sessions) == 0 {
		t.Skip("no tmux sessions available")
	}

	session := sessions[0]
	wid, err := tk.NewWindow(session, "munsu-test")
	if err != nil {
		t.Fatal(err)
	}
	if wid == "" {
		t.Fatal("NewWindow returned empty window ID")
	}

	// Verify the window is alive
	if !tk.Alive(wid) {
		t.Fatal("NewWindow window not alive after creation")
	}

	// Clean up
	if err := tk.Teardown(wid); err != nil {
		t.Errorf("Teardown failed: %v", err)
	}

	// Verify it's gone
	if tk.Alive(wid) {
		t.Error("window still alive after Teardown")
	}
}

func TestTmux_Alive_UnknownWindow(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	// An unknown window should return false
	if tk.Alive("@99999") {
		t.Error("Alive('@99999') returned true for unknown window")
	}
}

func TestTmux_NewWindow_SessionAutoCreated(t *testing.T) {
	if !hasTmux() {
		t.Skip("tmux not on PATH")
	}

	tk := &TmuxBackend{}
	// With F1.1, a nonexistent session is auto-created
	wid, err := tk.NewWindow("munsu-test-session-12345", "test")
	if err != nil {
		t.Fatalf("NewWindow should auto-create session, got error: %v", err)
	}
	if wid == "" {
		t.Fatal("NewWindow returned empty window ID")
	}
	// Verify the window is alive
	if !tk.Alive(wid) {
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

func TestTmux_Alive_TmuxNotOnPath(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	tk := &TmuxBackend{}
	if tk.Alive("@0") {
		t.Error("Alive() returned true when tmux is not on PATH")
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
		if tk.Alive("@0") {
			t.Error("Alive returned true when tmux is not on PATH")
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

func (f *fakeBackend) Alive(windowID string) bool {
	if f.aliveFn != nil {
		return f.aliveFn(windowID)
	}
	return f.windows[windowID]
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
	if !f.Alive(wid) {
		t.Error("Alive should return true after NewWindow")
	}
}

func TestFakeBackend_AliveUnknown(t *testing.T) {
	f := newFakeBackend()
	if f.Alive("@nonexistent") {
		t.Error("Alive should return false for unknown window")
	}
}

func TestFakeBackend_TeardownRemoves(t *testing.T) {
	f := newFakeBackend()
	wid, _ := f.NewWindow("s", "n")
	f.Teardown(wid)
	if f.Alive(wid) {
		t.Error("Alive should return false after Teardown")
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
	if f.Alive(wid) {
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
	// Verify that Select returns the expected types for known backends
	bk, err := Select("tmux")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bk.(*TmuxBackend); !ok {
		t.Errorf("Select('tmux') returned %T, want *TmuxBackend", bk)
	}

	bk, err = Select("herdr")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bk.(*HerdrBackend); !ok {
		t.Errorf("Select('herdr') returned %T, want *HerdrBackend", bk)
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
		if h.Alive("@0") {
			t.Error("Alive returned true when herdr is not on PATH")
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
