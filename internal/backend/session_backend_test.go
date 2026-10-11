package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestBackendIdentity is the one table for the backend identity owner,
// requireIdentity, through both of its callers. The tmux binary is present and
// the env markers name tmux and herdr, so a refused row cannot be a missing
// binary or an auto-detection.
func TestBackendIdentity(t *testing.T) {
	testutil.PrependPath(t, fakeExecutables(t, "tmux"))
	t.Setenv("TMUX", "/tmp/tmux-socket")
	t.Setenv("HERDR_ENV", "1")
	home := t.TempDir()

	resolve := func(name string) func() (Backend, string, error) {
		return func() (Backend, string, error) { return Resolve(name) }
	}
	bound := func(meta map[string]string) func() (Backend, string, error) {
		return func() (Backend, string, error) { return BackendForTask(home, meta) }
	}
	tests := []struct {
		name string
		call func() (Backend, string, error)
		want string // "" allows
	}{
		{"Resolve empty", resolve(""), `no session backend identity: ""`},
		{"Resolve auto", resolve("auto"), `no session backend identity: "auto"`},
		{"Resolve unknown", resolve("nonexistent"), `unknown session backend: "nonexistent"`},
		{"Resolve tmux", resolve("tmux"), ""},
		{"BackendForTask nil meta", bound(nil), `meta["backend"]: no session backend identity: ""`},
		{"BackendForTask meta without backend", bound(map[string]string{"window": "@test"}), `meta["backend"]: no session backend identity: ""`},
		{"BackendForTask empty backend", bound(map[string]string{"backend": ""}), `meta["backend"]: no session backend identity: ""`},
		{"BackendForTask auto", bound(map[string]string{"backend": "auto"}), `meta["backend"]: no session backend identity: "auto"`},
		{"BackendForTask unknown", bound(map[string]string{"backend": "nonexistent"}), `unknown bound session backend: "nonexistent"`},
		{"BackendForTask tmux", bound(map[string]string{"backend": "tmux"}), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bk, name, err := tt.call()
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("error = %v, want %q", err, tt.want)
				}
				if bk != nil || name != "" {
					t.Fatalf("refused call returned backend %T, name %q", bk, name)
				}
				return
			}
			if _, ok := bk.(*TmuxBackend); err != nil || !ok || name != "tmux" {
				t.Fatalf("got %T, %q, %v; want the tmux backend", bk, name, err)
			}
		})
	}
}

// TestBackendForTask_BoundHerdrSessionBinding verifies that the herdr branch
// keeps session BINDING from task metadata (session binding is not selection).
// The bound binary is provided on a controlled PATH so the test is
// deterministic — verification precedes binding.
func TestBackendForTask_BoundHerdrSessionBinding(t *testing.T) {
	fakeBin := fakeExecutables(t, "herdr")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	tmpDir := t.TempDir()
	meta := map[string]string{
		"backend":       "herdr",
		"herdr_session": "my-lab-session",
		"window":        "w1",
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
		t.Errorf("Session = %q, want my-lab-session (bound from meta)", hb.Session)
	}
}

// TestBackendForTask_HerdrFailsClosedWhenAbsent verifies the REWORK ruling:
// the herdr branch of BackendForTask verifies capability health BEFORE session
// binding — herdr absent from PATH FAILS CLOSED at resolution, and no
// session-bind metadata can mask the failure.
func TestBackendForTask_HerdrFailsClosedWhenAbsent(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")

	tmpDir := t.TempDir()
	meta := map[string]string{
		"backend":       "herdr",
		"herdr_session": "bound-session",
		"window":        "w1",
	}
	bk, name, err := BackendForTask(tmpDir, meta)
	if err == nil {
		t.Fatalf("BackendForTask(herdr) succeeded (%T) with herdr absent from PATH — must fail closed", bk)
	}
	if bk != nil {
		t.Errorf("BackendForTask returned a backend (%T) for an absent capability — must return nil", bk)
	}
	if name != "" {
		t.Errorf("name = %q on failure, want empty", name)
	}
	if !strings.Contains(err.Error(), "herdr: not found on PATH") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestBackendForTask_HerdrVerifiesBeforeSessionBinding verifies that with the
// herdr binary verifiably present (controlled PATH), the same verified
// construction path is used and session binding from meta is applied AFTER
// verification — including durable teardown seeds and the hometag.
func TestBackendForTask_HerdrVerifiesBeforeSessionBinding(t *testing.T) {
	fakeBin := fakeExecutables(t, "herdr")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	tmpDir := t.TempDir()
	meta := map[string]string{
		"backend":            "herdr",
		"herdr_session":      "bound-session",
		"window":             "w1",
		"herdr_tab_id":       "tab-1",
		"herdr_workspace_id": "ws-1",
		"home":               tmpDir,
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
	if hb.Session != "bound-session" {
		t.Errorf("Session = %q, want bound-session (meta herdr_session binds AFTER verification)", hb.Session)
	}
	if hb.TeardownTabID != "tab-1" {
		t.Errorf("TeardownTabID = %q, want tab-1 (durable meta seed)", hb.TeardownTabID)
	}
	if hb.TeardownWorkspaceID != "ws-1" {
		t.Errorf("TeardownWorkspaceID = %q, want ws-1 (durable meta seed)", hb.TeardownWorkspaceID)
	}
	if hb.Hometag != WorkspaceTag(tmpDir) {
		t.Errorf("Hometag = %q, want %q (home binding)", hb.Hometag, WorkspaceTag(tmpDir))
	}
}

func TestHerdrBackendFindTabByLabelRefusesDuplicateTabsDefaultLane(t *testing.T) {
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
