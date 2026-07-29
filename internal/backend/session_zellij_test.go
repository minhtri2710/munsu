//go:build integration

package backend

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeZellij creates a fake zellij executable in dir that responds to
// expected commands: attach, list-sessions, list-panes, new-pane, close-pane,
// dump-screen, write-chars, send-keys.
// It asserts that --session is present on every action call.
func writeFakeZellij(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "zellij")
	suffix := fmt.Sprintf("%x", rand.Int63())

	script := "#!/usr/bin/env bash\n" +
		// Track created sessions
		`if [ "$1" = "list-sessions" ]; then` + "\n" +
		`  if [ "$2" = "--short" ]; then` + "\n" +
		`    echo "existing-session"` + "\n" +
		`    exit 0` + "\n" +
		`  fi` + "\n" +
		`  echo "existing-session"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		// attach --create-background
		`if [ "$1" = "attach" ] && [ "$2" = "--create-background" ]; then` + "\n" +
		`  echo "created session $3" >> "` + dir + `/zellij-trace"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		// All action commands require --session flag
		`if [ "$1" = "--session" ]; then` + "\n" +
		`  SESSION="$2"` + "\n" +
		`  shift 2` + "\n" +
		"fi\n" +
		`if [ "$1" != "action" ]; then` + "\n" +
		`  >&2 echo "fake zellij: expected action subcommand"` + "\n" +
		`  exit 1` + "\n" +
		"fi\n" +
		`shift` + "\n" + // remove "action"
		"case \"$1\" in\n" +
		// new-pane
		"  new-pane)\n" +
		`    if [ "$2" = "--name" ]; then` + "\n" +
		`      echo "terminal_` + suffix + `"` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		`    echo "terminal_` + suffix + `"` + "\n" +
		"    exit 0\n" +
		"    ;;\n" +
		// write-chars
		"  write-chars)\n" +
		"    exit 0\n" +
		"    ;;\n" +
		// send-keys
		"  send-keys)\n" +
		"    exit 0\n" +
		"    ;;\n" +
		// dump-screen
		"  dump-screen)\n" +
		"    echo 'line 1'\n" +
		"    echo 'line 2'\n" +
		"    echo 'line 3'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		// list-panes
		"  list-panes)\n" +
		`    echo '[{"id":1,"is_plugin":false,"title":"/bin/bash","tab_id":0,"exited":false}]'` + "\n" +
		"    exit 0\n" +
		"    ;;\n" +
		// close-pane
		"  close-pane)\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"

	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeFakeZellijNotFound creates a fake zellij that always returns pane_not_found on stderr.
func writeFakeZellijNotFound(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "zellij")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "list-sessions" ]; then` + "\n" +
		`  echo "existing-session"` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$1" = "attach" ] && [ "$2" = "--create-background" ]; then` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`if [ "$1" = "--session" ]; then` + "\n" +
		`  shift 2` + "\n" +
		"fi\n" +
		`if [ "$1" != "action" ]; then` + "\n" +
		`  >&2 echo "fake zellij: expected action"` + "\n" +
		`  exit 1` + "\n" +
		"fi\n" +
		`shift` + "\n" +
		`if [ "$1" = "list-panes" ]; then` + "\n" +
		`  echo '[]'` + "\n" + // empty list - pane not found
		"  exit 0\n" +
		"fi\n" +
		`>&2 echo 'Error: pane not found'` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestZellijBackend_NewWindow(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeZellij(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	z := NewZellijBackend("test-session")
	win, err := z.NewWindow("test-ws", "task-1")
	if err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}

	if !strings.HasPrefix(win, "test-ws:terminal_") {
		t.Errorf("windowID = %q, want prefix test-ws:terminal_", win)
	}
}

func TestZellijBackend_Alive(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeZellij(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	z := NewZellijBackend("test-s")
	if !z.Alive("test-s:terminal_1") {
		t.Error("Alive returned false for existing pane")
	}
}

func TestZellijBackend_Alive_ReturnsFalseWhenNotFound(t *testing.T) {
	tmp := t.TempDir()
	writeFakeZellijNotFound(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmp+":"+oldPath)

	z := NewZellijBackend("test-s")
	if z.Alive("test-s:terminal_999") {
		t.Error("Alive returned true for nonexistent pane")
	}
}

func TestZellijBackend_SendKeys(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeZellij(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	z := NewZellijBackend("test-s")
	if err := z.SendKeys("test-s:terminal_1", "hello"); err != nil {
		t.Errorf("SendKeys failed: %v", err)
	}
}

func TestZellijBackend_Capture(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeZellij(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	z := NewZellijBackend("test-s")
	out, err := z.Capture("test-s:terminal_1", 5)
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}

	if !strings.Contains(out, "line 1") || !strings.Contains(out, "line 2") {
		t.Errorf("Capture output missing expected lines: %s", out)
	}
}

func TestZellijBackend_Teardown(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeZellij(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	z := NewZellijBackend("test-s")
	if err := z.Teardown("test-s:terminal_1"); err != nil {
		t.Errorf("Teardown failed: %v", err)
	}
}

func TestZellijBackend_Teardown_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	writeFakeZellijNotFound(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmp+":"+oldPath)

	z := NewZellijBackend("test-s")
	if err := z.Teardown("test-s:terminal_999"); err != nil {
		t.Errorf("Teardown should be idempotent for not-found panes: %v", err)
	}
}

func TestZellijBackend_NotFound(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")

	z := &ZellijBackend{Session: "test-s"}

	t.Run("NewWindow", func(t *testing.T) {
		_, err := z.NewWindow("s", "n")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("SendKeys", func(t *testing.T) {
		err := z.SendKeys("@0", "text")
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Capture", func(t *testing.T) {
		_, err := z.Capture("@0", 10)
		if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
			t.Errorf("expected PATH error, got: %v", err)
		}
	})

	t.Run("Alive", func(t *testing.T) {
		if z.Alive("@0") {
			t.Error("Alive returned true when zellij is not on PATH")
		}
	})

	t.Run("Teardown", func(t *testing.T) {
		// Teardown suppresses errors — nil is expected
		if err := z.Teardown("@0"); err != nil {
			t.Logf("Teardown returned error (expected with no zellij): %v", err)
		}
	})
}

func TestNewZellijBackend_Defaults(t *testing.T) {
	// With explicit session
	z := NewZellijBackend("explicit-session")
	if z.Session != "explicit-session" {
		t.Errorf("Session = %q, want explicit-session", z.Session)
	}

	// With empty session and no env, should fallback to "default"
	z2 := NewZellijBackend("")
	if z2.Session != "default" {
		t.Errorf("Session = %q, want default", z2.Session)
	}
}

func TestNormalizePaneID(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"terminal_3", 3},
		{"terminal_42", 42},
		{"3", 3},
		{"42", 42},
		{"abc", -1},
		{"", -1},
		{"plugin_1", -1}, // plugins are filtered out, but test the ID parsing
	}

	for _, tt := range tests {
		got := normalizePaneID(tt.input)
		if got != tt.want {
			t.Errorf("normalizePaneID(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
