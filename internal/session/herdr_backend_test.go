package session

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeHerdr creates a fake herdr executable in dir that responds to
// expected commands: workspace list, workspace create, tab create, pane get,
// pane close, pane send-text, pane send-keys, pane read.
func writeFakeHerdr(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	suffix := fmt.Sprintf("%x", rand.Int63())
	script := "#!/usr/bin/env bash\n" +
		`case "$1" in` + "\n" +
		"  workspace)\n" +
		`    if [ "$2" = "list" ]; then` + "\n" +
		`      cat <<'JSON'` + "\n" +
		`{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[{"label":"test-ws","workspace_id":"wTest"}]}}` + "\n" +
		"JSON\n" +
		"      exit 0\n" +
		"    fi\n" +
		`    if [ "$2" = "create" ]; then` + "\n" +
		`      cat <<'JSON'` + "\n" +
		`{"id":"cli:workspace:create","result":{"workspace":{"workspace_id":"w` + suffix + `"},"type":"workspace_created"}}` + "\n" +
		"JSON\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"  tab)\n" +
		`    if [ "$2" = "create" ]; then` + "\n" +
		`      cat <<'JSON'` + "\n" +
		`{"id":"cli:tab:create","result":{"root_pane":{"pane_id":"wTest:p1"},"tab":{"tab_id":"wTest:t1","workspace_id":"wTest"},"type":"tab_created"}}` + "\n" +
		"JSON\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"  pane)\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo '{"id":"cli:pane:get","result":{"pane_id":"wTest:p1"}}'` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		`    if [ "$2" = "close" ]; then` + "\n" +
		`      echo '{"id":"cli:pane:close","result":{"type":"ok"}}'` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		`    if [ "$2" = "send-text" ]; then` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		`    if [ "$2" = "send-keys" ]; then` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		`    if [ "$2" = "read" ]; then` + "\n" +
		"      echo 'line 1'\n" +
		"      echo 'line 2'\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"esac\n" +
		`echo '{"error":{"code":"unknown_command"}}'` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHerdrBackend_NewWindow_CreatesWorkspace(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	h := &HerdrBackend{}
	win, err := h.NewWindow("test-ws", "task-1")
	if err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}
	if win != "wTest:p1" {
		t.Errorf("windowID = %q, want wTest:p1", win)
	}
}

func TestHerdrBackend_NewWindow_CreatesMissingWorkspace(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	h := &HerdrBackend{}
	win, err := h.NewWindow("missing-ws", "task-2")
	if err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}
	if !strings.HasPrefix(win, "w") || !strings.Contains(win, ":p1") {
		t.Errorf("windowID = %q, expected format like w<pfx>:p1", win)
	}
}

func TestHerdrBackend_Alive(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	h := &HerdrBackend{}
	if !h.Alive("wTest:p1") {
		t.Error("Alive returned false for existing pane")
	}
}

func TestHerdrBackend_SendKeys(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	h := &HerdrBackend{}
	if err := h.SendKeys("wTest:p1", "hello"); err != nil {
		t.Errorf("SendKeys failed: %v", err)
	}
}

func TestHerdrBackend_Capture(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	h := &HerdrBackend{}
	out, err := h.Capture("wTest:p1", 5)
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}
	if !strings.Contains(out, "line 1") || !strings.Contains(out, "line 2") {
		t.Errorf("Capture output missing expected lines: %s", out)
	}
}

func TestHerdrBackend_Teardown_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakePath+":"+oldPath)

	h := &HerdrBackend{}
	if err := h.Teardown("wTest:p1"); err != nil {
		t.Errorf("Teardown failed: %v", err)
	}
}

// writeFakeHerdrNotFound creates a fake herdr that always returns pane_not_found on stderr.
func writeFakeHerdrNotFound(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		">&2 echo '{\"error\":{\"code\":\"pane_not_found\",\"message\":\"pane x not found\"}}'\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHerdrBackend_Teardown_NotFoundIgnored(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrNotFound(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmp+":"+oldPath)

	h := &HerdrBackend{}
	if err := h.Teardown("x"); err != nil {
		t.Errorf("Teardown should ignore not-found: %v", err)
	}
}

func TestHerdrBackend_Alive_ReturnsFalseWhenNotFound(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrNotFound(t, tmp)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmp+":"+oldPath)

	h := &HerdrBackend{}
	if h.Alive("nonexistent") {
		t.Error("Alive returned true for nonexistent pane")
	}
}
