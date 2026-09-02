//go:build integration

package backend

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// hasHerdr reports whether herdr is available on PATH.
func hasHerdr() bool {
	_, err := exec.LookPath("herdr")
	return err == nil
}

// writeFakeHerdr creates a fake herdr executable in dir that responds to
// expected commands: workspace list, workspace create, workspace close,
// tab create, tab close, pane get, pane close, pane send-text, pane send-keys, pane read.
// It asserts that --session is present on every call, and --no-focus on tab create.
func writeFakeHerdr(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	suffix := fmt.Sprintf("%x", rand.Int63())
	// Build the script. Lines without $ must be raw strings for readability.
	// Lines with $ must use double-quoted strings.
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--session" ]; then` + "\n" +
		`  SESSION="$2"` + "\n" +
		`  shift 2` + "\n" +
		`fi` + "\n" +
		`if [ -z "$SESSION" ]; then` + "\n" +
		`  >&2 echo 'fake herdr: --session missing'` + "\n" +
		`  exit 1` + "\n" +
		`fi` + "\n" +
		`case "$1" in` + "\n" +
		"  workspace)\\\n" +
		`    if [ "$2" = "list" ]; then` + "\n" +
		`      cat <<'JSON'` + "\n" +
		`{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[{"label":"test-ws","workspace_id":"wTest","tab_count":1}]}}` + "\n" +
		"JSON\n" +
		"      exit 0\n" +
		"    fi\n" +
		`    if [ "$2" = "create" ]; then` + "\n" +
		`      cat <<'JSON'` + "\n" +
		`{"id":"cli:workspace:create","result":{"workspace":{"workspace_id":"w` + suffix + `"},"type":"workspace_created"}}` + "\n" +
		"JSON\n" +
		"      exit 0\n" +
		"    fi\n" +
		`    if [ "$2" = "close" ]; then` + "\n" +
		`      echo "workspace $3 closed" >> "` + dir + `/herdr-trace"` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"  tab)\n" +
		`    if [ "$2" = "create" ]; then` + "\n" +
		`      if [ "$3" != "--workspace" ] || [ "$5" != "--label" ] || [ "$7" != "--no-focus" ]; then` + "\n" +
		"        >&2 echo 'fake herdr: tab create missing --no-focus'\n" +
		"        exit 1\n" +
		"      fi\n" +
		// Use echo with fixed workspace-prefixed pane_id
		`      echo '{"id":"cli:tab:create","result":{"root_pane":{"pane_id":"wTest:p1"},"tab":{"tab_id":"wTest:t1","workspace_id":"wTest"},"type":"tab_created"}}'` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		`    if [ "$2" = "close" ]; then` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"  pane)\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo '{"id":"cli:pane:get","result":{"pane_id":"'$3'"}}'` + "\n" +
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
	testutil.WriteFakeExecutable(t, bin, script)
	return dir
}

func TestHerdrBackend_NewWindow_CreatesWorkspace(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-session")
	win, err := h.NewWindow("test-ws", "task-1")
	if err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}
	want := "test-session:wTest:p1"
	if win != want {
		t.Errorf("windowID = %q, want %q", win, want)
	}
	// Verify meta extras
	extras := h.MetaExtras()
	if extras == nil {
		t.Fatal("MetaExtras returned nil")
	}
	if extras["herdr_session"] != "test-session" {
		t.Errorf("herdr_session = %q, want test-session", extras["herdr_session"])
	}
	if extras["herdr_tab_id"] != "wTest:t1" {
		t.Errorf("herdr_tab_id = %q, want wTest:t1", extras["herdr_tab_id"])
	}
	if extras["herdr_pane_id"] != "wTest:p1" {
		t.Errorf("herdr_pane_id = %q, want wTest:p1", extras["herdr_pane_id"])
	}
}

func TestHerdrBackend_NewWindow_CreatesMissingWorkspace(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-ms")
	win, err := h.NewWindow("missing-ws", "task-2")
	if err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}
	want := "test-ms:"
	if !strings.HasPrefix(win, want) {
		t.Errorf("windowID = %q, want prefix %q", win, want)
	}
	if !strings.Contains(win, ":p1") {
		t.Errorf("windowID = %q, expected :p1 suffix", win)
	}
}

func TestHerdrBackend_Alive(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-s")
	if alive, _ := h.CheckAlive("wTest:p1"); !alive {
		t.Error("CheckAlive returned false for existing pane")
	}
	if alive, _ := h.CheckAlive("test-s:wTest:p1"); !alive {
		t.Error("CheckAlive returned false for session:prefix pane")
	}
}

func TestHerdrBackend_SendKeys(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-s")
	if err := h.SendKeys("wTest:p1", "hello"); err != nil {
		t.Errorf("SendKeys failed for bare pane: %v", err)
	}
	if err := h.SendKeys("test-s:wTest:p1", "hello"); err != nil {
		t.Errorf("SendKeys failed for session:pane: %v", err)
	}
}

func TestHerdrBackend_Capture(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-s")
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
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-s")
	if err := h.Teardown("wTest:p1"); err != nil {
		t.Errorf("Teardown failed: %v", err)
	}
	if err := h.Teardown("test-s:wTest:p1"); err != nil {
		t.Errorf("Teardown with session prefix failed: %v", err)
	}
}

// writeFakeHerdrNotFound creates a fake herdr that always returns pane_not_found on stderr.
func writeFakeHerdrNotFound(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--session" ]; then` + "\n" +
		`  shift 2` + "\n" +
		`fi` + "\n" +
		`>&2 echo '{"error":{"code":"pane_not_found","message":"pane not found"}}'` + "\n" +
		"exit 1\n"
	testutil.WriteFakeExecutable(t, bin, script)
	return dir
}

func TestHerdrBackend_Teardown_NotFoundIgnored(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrNotFound(t, tmp)
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	if err := h.Teardown("x"); err != nil {
		t.Errorf("Teardown should ignore not-found: %v", err)
	}
}

func TestHerdrBackend_Alive_ReturnsFalseWhenNotFound(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrNotFound(t, tmp)
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	if alive, err := h.CheckAlive("nonexistent"); alive || err == nil {
		t.Error("CheckAlive returned alive/err==nil for nonexistent pane")
	}
}

// writeFakeHerdrWithAgent creates a fake herdr that handles both pane get and agent get.
// When agentStatus is empty, agent get returns agent_not_found (no agent).
// When agentStatus is non-empty, agent get returns that status (e.g. "idle", "working").
func writeFakeHerdrWithAgent(t *testing.T, dir string, agentStatus string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")

	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--session" ]; then` + "\n" +
		`  SESSION="$2"` + "\n" +
		`  shift 2` + "\n" +
		`fi` + "\n" +
		`if [ -z "$SESSION" ]; then` + "\n" +
		`  >&2 echo 'fake herdr: --session missing'` + "\n" +
		`  exit 1` + "\n" +
		`fi` + "\n" +
		`case "$1" in` + "\n" +
		"  agent)\n" +
		`    if [ "$2" = "get" ]; then` + "\n"
	if agentStatus == "" {
		script += `      >&2 echo '{"error":{"code":"agent_not_found","message":"no agent in pane"}}'` + "\n" +
			"      exit 1\n" +
			"    fi\n"
	} else {
		jsonStr := `{"result":{"agent":{"agent_status":"` + agentStatus + `"}}}`
		script += "      echo '" + jsonStr + "'\n" +
			"      exit 0\n" +
			"    fi\n"
	}
	script += "    ;;\n" +
		"  pane)\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo '{"id":"cli:pane:get","result":{"pane_id":"'"$3"'"}}'` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 1\n"

	testutil.WriteFakeExecutable(t, bin, script)
	return dir
}

func TestHerdrBackend_CheckAgentAlive_WithAgent(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrWithAgent(t, tmp, "working")
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	alive, agentAlive, err := h.CheckAgentAlive("wTest:p1")
	if err != nil {
		t.Fatalf("CheckAgentAlive error: %v", err)
	}
	if !alive {
		t.Error("CheckAgentAlive alive=false, want true")
	}
	if !agentAlive {
		t.Error("CheckAgentAlive agentAlive=false, want true")
	}
}

func TestHerdrBackend_CheckAgentAlive_DoneAgentPaneRemainsAlive(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrWithAgent(t, tmp, "done")
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	paneAlive, agentAlive, err := h.CheckAgentAlive("wTest:p1")
	if err != nil {
		t.Fatalf("CheckAgentAlive error: %v", err)
	}
	if !paneAlive {
		t.Fatal("pane should remain alive")
	}
	if !agentAlive {
		t.Fatal("agent_status=done with a live pane must remain alive")
	}
}

func TestHerdrBackend_CheckAgentAlive_NoAgent(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrWithAgent(t, tmp, "") // agent_not_found
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	alive, agentAlive, err := h.CheckAgentAlive("wTest:p1")
	if err != nil {
		t.Fatalf("CheckAgentAlive error: %v", err)
	}
	if !alive {
		t.Error("CheckAgentAlive alive=false, want true (pane exists)")
	}
	if agentAlive {
		t.Error("CheckAgentAlive agentAlive=true, want false")
	}
	// Pane-present/no-agent is NOT authoritative absence: it must never be
	// reported as ErrPaneNotFound, the sole relaunch authority.
	if errors.Is(err, ErrPaneNotFound) {
		t.Error("CheckAgentAlive pane-present/no-agent must not be ErrPaneNotFound")
	}
}

func TestHerdrBackend_CheckAgentAlive_PaneNotFound(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrNotFound(t, tmp)
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	alive, agentAlive, err := h.CheckAgentAlive("nonexistent")
	if alive {
		t.Error("CheckAgentAlive alive=true, want false")
	}
	if agentAlive {
		t.Error("CheckAgentAlive agentAlive=true, want false")
	}
	if !errors.Is(err, ErrPaneNotFound) {
		t.Errorf("CheckAgentAlive error = %v, want ErrPaneNotFound", err)
	}
}

func TestParseWindow(t *testing.T) {
	tests := []struct {
		handle      string
		wantSession string
		wantPaneID  string
	}{
		{"test-session:wTest:p1", "test-session", "wTest:p1"},
		{"bare:p1", "bare", "p1"},
		{"nocolon", "", "nocolon"},
		{"a:b:c", "a", "b:c"},
	}
	for _, tt := range tests {
		session, paneID := ParseWindow(tt.handle)
		if session != tt.wantSession {
			t.Errorf("ParseWindow(%q) session = %q, want %q", tt.handle, session, tt.wantSession)
		}
		if paneID != tt.wantPaneID {
			t.Errorf("ParseWindow(%q) paneID = %q, want %q", tt.handle, paneID, tt.wantPaneID)
		}
	}
}

func TestHerdrBackend_PaneIDAndEffectiveSession(t *testing.T) {
	h := NewHerdrBackend("default")

	// 2-colon window handle (session:workspace:pane)
	if sess := h.effectiveSession("default:w6E:p3"); sess != "default" {
		t.Errorf("effectiveSession(default:w6E:p3) = %q, want default", sess)
	}
	if p := herdrPaneID("default:w6E:p3"); p != "w6E:p3" {
		t.Errorf("herdrPaneID(default:w6E:p3) = %q, want w6E:p3", p)
	}

	// 1-colon window handle (workspace:pane)
	if sess := h.effectiveSession("w6E:p3"); sess != "default" {
		t.Errorf("effectiveSession(w6E:p3) = %q, want default", sess)
	}
	if p := herdrPaneID("w6E:p3"); p != "w6E:p3" {
		t.Errorf("herdrPaneID(w6E:p3) = %q, want w6E:p3", p)
	}

	// Foreign session handle (foreign-session:wTest:p1)
	if sess := h.effectiveSession("foreign-session:wTest:p1"); sess != "foreign-session" {
		t.Errorf("effectiveSession(foreign-session:wTest:p1) = %q, want foreign-session", sess)
	}
	if p := herdrPaneID("foreign-session:wTest:p1"); p != "wTest:p1" {
		t.Errorf("herdrPaneID(foreign-session:wTest:p1) = %q, want wTest:p1", p)
	}
}

func TestHerdrBackend_CheckAlive_PaneNotFound(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`echo '{"error":{"code":"pane_not_found","message":"pane w6E:p3 not found"}}'` + "\n" +
		"exit 1\n"
	testutil.WriteFakeExecutable(t, bin, script)
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("default")
	alive, err := h.CheckAlive("default:w6E:p3")
	if alive {
		t.Error("CheckAlive returned true for pane_not_found")
	}
	if !errors.Is(err, ErrPaneNotFound) {
		t.Errorf("CheckAlive error = %v, want ErrPaneNotFound", err)
	}
	if alive, _ := h.CheckAlive("default:w6E:p3"); alive {
		t.Error("CheckAlive returned true for pane_not_found")
	}
}

func TestNewHerdrBackend_Defaults(t *testing.T) {
	// With explicit session
	h := NewHerdrBackend("explicit-session")
	if h.Session != "explicit-session" {
		t.Errorf("Session = %q, want explicit-session", h.Session)
	}

	// With empty session and no env, should fallback to "default"
	h2 := NewHerdrBackend("")
	if h2.Session != "default" {
		t.Errorf("Session = %q, want default", h2.Session)
	}
}

func TestMetaExtras_NilBeforeNewWindow(t *testing.T) {
	h := NewHerdrBackend("test-s")
	if extras := h.MetaExtras(); extras != nil {
		t.Errorf("MetaExtras before NewWindow = %v, want nil", extras)
	}
}

func TestResolve_HerdrUsesDefaultSessionNotHometag(t *testing.T) {
	if !hasHerdr() {
		t.Skip("herdr not on PATH (Resolve verifies the requested capability)")
	}

	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Resolve for "herdr" — Session should be "default" (or HERDR_SESSION), not the hometag
	bk, name, err := Resolve(homeDir, "herdr")
	if err != nil {
		t.Fatal(err)
	}
	if name != "herdr" {
		t.Errorf("name = %q, want herdr", name)
	}
	hb, ok := bk.(*HerdrBackend)
	if !ok {
		t.Fatalf("expected *HerdrBackend, got %T", bk)
	}
	// hometag of homeDir should be something like "d671e5b" based on hash
	// Session should never match a hash-like hometag
	if hb.Session == "default" {
		return // OK — default when no HERDR_SESSION set
	}
	if strings.HasPrefix(hb.Session, "d") && len(hb.Session) == 7 {
		t.Errorf("Resolve herdr Session = %q, appears to be a hometag; want 'default' or HERDR_SESSION value", hb.Session)
	}
}

func TestBackendForTask_HerdrSessionFromMeta(t *testing.T) {
	if !hasHerdr() {
		t.Skip("herdr not on PATH (BackendForTask verifies the requested capability)")
	}

	tmpDir := t.TempDir()

	// With herdr_session in meta
	meta := map[string]string{
		"backend":       "herdr",
		"herdr_session": "my-lab-session",
		"window":        "default:w1:p1",
	}
	bk, name, err := BackendForTask(tmpDir, meta)
	if err != nil {
		t.Fatal(err)
	}
	if name != "herdr" {
		t.Errorf("name = %q, want herdr", name)
	}
	hb, ok := bk.(*HerdrBackend)
	if !ok {
		t.Fatalf("expected *HerdrBackend, got %T", bk)
	}
	if hb.Session != "my-lab-session" {
		t.Errorf("Session = %q, want my-lab-session (from meta herdr_session)", hb.Session)
	}
}

func TestBackendForTask_HerdrSessionFromWindow(t *testing.T) {
	if !hasHerdr() {
		t.Skip("herdr not on PATH (BackendForTask verifies the requested capability)")
	}

	tmpDir := t.TempDir()

	// With window session prefix but no herdr_session
	meta := map[string]string{
		"backend": "herdr",
		"window":  "my-session:w1:p1",
	}
	bk, name, err := BackendForTask(tmpDir, meta)
	if err != nil {
		t.Fatal(err)
	}
	if name != "herdr" {
		t.Errorf("name = %q, want herdr", name)
	}
	hb, ok := bk.(*HerdrBackend)
	if !ok {
		t.Fatalf("expected *HerdrBackend, got %T", bk)
	}
	if hb.Session != "my-session" {
		t.Errorf("Session = %q, want my-session (from ParseWindow(window))", hb.Session)
	}
}

func TestBackendForTask_HerdrSessionFallsBackToDefault(t *testing.T) {
	if !hasHerdr() {
		t.Skip("herdr not on PATH (BackendForTask verifies the requested capability)")
	}

	tmpDir := t.TempDir()

	// No herdr_session, no window session prefix
	meta := map[string]string{
		"backend": "herdr",
		"window":  "bare-pane",
	}
	bk, name, err := BackendForTask(tmpDir, meta)
	if err != nil {
		t.Fatal(err)
	}
	if name != "herdr" {
		t.Errorf("name = %q, want herdr", name)
	}
	hb, ok := bk.(*HerdrBackend)
	if !ok {
		t.Fatalf("expected *HerdrBackend, got %T", bk)
	}
	if hb.Session != "default" {
		t.Errorf("Session = %q, want default (fallback when no HERDR_SESSION)", hb.Session)
	}
}

func TestSeedTeardownTab(t *testing.T) {
	h := NewHerdrBackend("test-s")

	// Before seeding, tabIDToClose should be empty
	if tabID := h.tabIDToClose(); tabID != "" {
		t.Errorf("tabIDToClose before seed = %q, want empty", tabID)
	}

	// Seed a tab ID
	h.SeedTeardownTab("meta-tab-123")

	if tabID := h.tabIDToClose(); tabID != "meta-tab-123" {
		t.Errorf("tabIDToClose after seed = %q, want meta-tab-123", tabID)
	}

	// lastCreate should still be nil (not affected by SeedTeardownTab)
	if h.lastCreate != nil {
		t.Errorf("lastCreate should remain nil after SeedTeardownTab")
	}
}

func TestSeedTeardownTab_WithFakeHerdr(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-s")
	// Seed a tab ID (simulating reconstruction from meta)
	h.SeedTeardownTab("wTest:t1")

	// Teardown should close the tab (fake herdr accepts it) even without lastCreate
	if err := h.Teardown("wTest:p1"); err != nil {
		t.Errorf("Teardown with seeded tab ID failed: %v", err)
	}
}

func TestTeardown_ClosesWorkspace_WhenHometagMatches(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-s")
	h.TeardownWorkspaceID = "wTest"
	h.Hometag = "test-ws" // matches the workspace label in fake herdr
	h.SeedTeardownTab("wTest:t1")

	if err := h.Teardown("wTest:p1"); err != nil {
		t.Errorf("Teardown with matching hometag should succeed: %v", err)
	}
}

func TestTeardown_NoWorkspaceClose_WhenInDenyList(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-s")
	h.TeardownWorkspaceID = "wTest"
	h.Hometag = "test-ws"
	h.DenyCloseWorkspaceIDs = []string{"wTest"}

	if err := h.Teardown("wTest:p1"); err != nil {
		t.Errorf("Teardown with deny-listed workspace should succeed: %v", err)
	}
}

func TestTeardown_NoWorkspaceClose_WhenForeignLabel(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-s")
	h.TeardownWorkspaceID = "wTest"
	h.Hometag = "other" // doesn't match workspace label 'test-ws'

	if err := h.Teardown("wTest:p1"); err != nil {
		t.Errorf("Teardown with non-matching hometag should succeed (no workspace close): %v", err)
	}
}

func TestTeardown_NoWorkspaceClose_WhenEmptyHometag(t *testing.T) {
	tmp := t.TempDir()
	fakePath := writeFakeHerdr(t, tmp)
	testutil.PrependPath(t, fakePath)

	h := NewHerdrBackend("test-s")
	h.TeardownWorkspaceID = "wTest"
	// Hometag is empty — no workspace close should happen

	if err := h.Teardown("wTest:p1"); err != nil {
		t.Errorf("Teardown with empty hometag should succeed: %v", err)
	}
}

func TestWorkspaceIDToClose_PrefersLastCreate(t *testing.T) {
	h := NewHerdrBackend("test-s")
	h.TeardownWorkspaceID = "from-meta"

	// Before lastCreate, should return from TeardownWorkspaceID
	if wsID := h.workspaceIDToClose(); wsID != "from-meta" {
		t.Errorf("workspaceIDToClose = %q, want from-meta", wsID)
	}

	// After setting lastCreate, should return lastCreate.WorkspaceID
	h.lastCreate = &herdrLastCreate{WorkspaceID: "from-new-window"}
	if wsID := h.workspaceIDToClose(); wsID != "from-new-window" {
		t.Errorf("workspaceIDToClose after lastCreate = %q, want from-new-window", wsID)
	}
}

// writeFakeHerdrAgentGetError creates a fake herdr whose `agent get` succeeds
// at the process level and answers with a structured error carrying a code
// other than agent_not_found. That is a different state from the exec-failure
// path above: the command worked, and the backend is being told the agent
// could not be reported on.
func writeFakeHerdrAgentGetError(t *testing.T, dir, code string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--session" ]; then` + "\n" +
		`  shift 2` + "\n" +
		`fi` + "\n" +
		`case "$1" in` + "\n" +
		"  agent)\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo '{"error":{"code":"` + code + `","message":"agent subsystem degraded"}}'` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"  pane)\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo '{"id":"cli:pane:get","result":{"pane_id":"'"$3"'"}}'` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 1\n"
	testutil.WriteFakeExecutable(t, bin, script)
	return dir
}

// A structured agent-get error that is NOT agent_not_found means the backend
// could not determine anything about the agent. Reporting the agent as alive
// would be a guess, and reporting it dead would authorize a relaunch on no
// evidence, so this fails closed with the error surfaced.
func TestHerdrBackend_CheckAgentAliveFailsClosedOnStructuredAgentGetError(t *testing.T) {
	tmp := t.TempDir()
	writeFakeHerdrAgentGetError(t, tmp, "agent_subsystem_unavailable")
	testutil.PrependPath(t, tmp)

	h := NewHerdrBackend("test-s")
	paneAlive, agentAlive, err := h.CheckAgentAlive("wTest:p1")
	if err == nil {
		t.Fatal("CheckAgentAlive accepted a structured agent get error")
	}
	if !strings.Contains(err.Error(), "agent get error") {
		t.Fatalf("error = %v, want the agent-get refusal", err)
	}
	if paneAlive || agentAlive {
		t.Fatalf("liveness = pane:%v agent:%v, want both false on an undetermined answer", paneAlive, agentAlive)
	}
}

// writeFakeHerdrAgentGetProcessError makes `agent get` exit non-zero with
// output that is not a structured agent_not_found error; pane get still
// succeeds. This is the process-level failure return in observeAgent.
func writeFakeHerdrAgentGetProcessError(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--session" ]; then` + "\n" +
		`  shift 2` + "\n" +
		`fi` + "\n" +
		`case "$1" in` + "\n" +
		"  agent)\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo 'agent subsystem crashed' >&2` + "\n" +
		"      exit 3\n" +
		"    fi\n" +
		"    ;;\n" +
		"  pane)\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo '{"id":"cli:pane:get","result":{"pane_id":"'"$3"'"}}'` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 1\n"
	testutil.WriteFakeExecutable(t, bin, script)
	return dir
}

// writeFakeHerdrAgentGetMalformed makes `agent get` succeed (exit 0) with output
// that is not valid JSON; pane get succeeds. This is the malformed-response
// return in observeAgent.
func writeFakeHerdrAgentGetMalformed(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "herdr")
	script := "#!/usr/bin/env bash\n" +
		`if [ "$1" = "--session" ]; then` + "\n" +
		`  shift 2` + "\n" +
		`fi` + "\n" +
		`case "$1" in` + "\n" +
		"  agent)\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo 'not a json response'` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"  pane)\n" +
		`    if [ "$2" = "get" ]; then` + "\n" +
		`      echo '{"id":"cli:pane:get","result":{"pane_id":"'"$3"'"}}'` + "\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 1\n"
	testutil.WriteFakeExecutable(t, bin, script)
	return dir
}

// ObserveAgent is a second public accessor over the same probe. It must honor
// the same fail-closed contract as CheckAgentAlive: when the probe returns an
// error the pane liveness it reports must be false, or a future consumer that
// reads paneAlive before checking err would treat an undetermined answer as a
// live pane. CheckAgentAlive zeroes at the accessor, so testing through it
// passes even with the source bug present; this asserts through ObserveAgent.
// Each case exercises one of observeAgent's three err!=nil return sites so
// re-adding paneAlive:true at any of them fails this test.
func TestHerdrBackend_ObserveAgentFailsClosedOnAgentGetError(t *testing.T) {
	cases := []struct {
		name string
		fake func(*testing.T, string) string
	}{
		{"structured error", func(t *testing.T, dir string) string {
			return writeFakeHerdrAgentGetError(t, dir, "agent_subsystem_unavailable")
		}},
		{"process error", writeFakeHerdrAgentGetProcessError},
		{"malformed json", writeFakeHerdrAgentGetMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			tc.fake(t, tmp)
			testutil.PrependPath(t, tmp)

			h := NewHerdrBackend("test-s")
			paneAlive, agentAlive, recognized, _, err := h.ObserveAgent("wTest:p1")
			if err == nil {
				t.Fatal("ObserveAgent accepted an agent-get error")
			}
			if paneAlive || agentAlive || recognized {
				t.Fatalf("observation = pane:%v agent:%v recognized:%v, want all false alongside err", paneAlive, agentAlive, recognized)
			}
		})
	}
}

func TestHerdrBackendFindTabByLabelRefusesDuplicateTabs(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "herdr")
	script := "#!/bin/sh\nif [ \"$1\" = \"--session\" ]; then shift 2; fi\ncat <<'JSON'\n{\"result\":{\"tabs\":[{\"label\":\"dup\",\"tab_id\":\"t1\"},{\"label\":\"dup\",\"tab_id\":\"t2\"}]}}\nJSON\n"
	testutil.WriteFakeExecutable(t, bin, script)
	testutil.PrependPath(t, tmp)
	h := NewHerdrBackend("test")
	_, err := h.findTabByLabel("w1", "dup")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("findTabByLabel error = %v, want ambiguous", err)
	}
}
