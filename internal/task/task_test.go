package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// setHomeEnv sets MUNSU_HOME for the duration of a test.
func setHomeEnv(t *testing.T, path string) {
	t.Helper()
	os.Setenv("MUNSU_HOME", path)
	t.Cleanup(func() { os.Unsetenv("MUNSU_HOME") })
}

func TestWriteMeta(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	meta := map[string]string{
		"window":   "@42",
		"worktree": "/tmp/wt",
		"project":  "munsu",
		"harness":  "pi",
		"model":    "claude-sonnet-4-20250515",
		"kind":     "ship",
		"mode":     "no-mistakes",
		"yolo":     "off",
	}

	if err := WriteMeta(tmp, "test-task-1", meta); err != nil {
		t.Fatal(err)
	}

	// Verify the file exists
	p := filepath.Join(tmp, "state", "test-task-1.meta")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("meta file not created: %v", err)
	}

	// Verify content
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for k, v := range meta {
		expected := k + "=" + v
		if !strings.Contains(content, expected) {
			t.Errorf("meta file missing %q", expected)
		}
	}
}

func TestReadMeta(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	// Write a meta file directly
	dir := filepath.Join(tmp, "state")
	os.MkdirAll(dir, 0755)
	metaFile := filepath.Join(dir, "test-task-2.meta")
	content := "window=@42\nworktree=/tmp/wt\nproject=munsu\nharness=pi\nmodel=claude-sonnet-4-20250515\nkind=ship\nmode=no-mistakes\nyolo=off\n"
	if err := os.WriteFile(metaFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadMeta(tmp, "test-task-2")
	if err != nil {
		t.Fatal(err)
	}

	expected := map[string]string{
		"window":   "@42",
		"worktree": "/tmp/wt",
		"project":  "munsu",
		"harness":  "pi",
		"model":    "claude-sonnet-4-20250515",
		"kind":     "ship",
		"mode":     "no-mistakes",
		"yolo":     "off",
	}
	for k, v := range expected {
		if got := meta[k]; got != v {
			t.Errorf("meta[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestReadMeta_NotFound(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	_, err := ReadMeta(tmp, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent meta file")
	}
}

func TestReadMeta_EmptyFile(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	dir := filepath.Join(tmp, "state")
	os.MkdirAll(dir, 0755)
	metaFile := filepath.Join(dir, "empty.meta")
	if err := os.WriteFile(metaFile, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadMeta(tmp, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 0 {
		t.Errorf("expected empty map, got %v", meta)
	}
}

func TestReadMeta_SkipsCommentsAndBlanks(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	dir := filepath.Join(tmp, "state")
	os.MkdirAll(dir, 0755)
	metaFile := filepath.Join(dir, "comments.meta")
	content := "# This is a comment\n\nwindow=@1\n\n# Another comment\nyolo=on\n"
	if err := os.WriteFile(metaFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadMeta(tmp, "comments")
	if err != nil {
		t.Fatal(err)
	}
	if meta["window"] != "@1" {
		t.Errorf("meta[window] = %q, want @1", meta["window"])
	}
	if meta["yolo"] != "on" {
		t.Errorf("meta[yolo] = %q, want on", meta["yolo"])
	}
	if _, ok := meta["#"]; ok {
		t.Error("comments should not be parsed as keys")
	}
}

func TestRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	original := map[string]string{
		"window":   "@99",
		"worktree": "/tmp/test-wt",
		"project":  "test-project",
		"harness":  "codex",
		"model":    "gpt-5.2-codex",
		"effort":   "80",
		"kind":     "scout",
	}

	if err := WriteMeta(tmp, "roundtrip", original); err != nil {
		t.Fatal(err)
	}

	got, err := ReadMeta(tmp, "roundtrip")
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range original {
		if got[k] != v {
			t.Errorf("meta[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestAppendStatus(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := AppendStatus(tmp, "test-task-3", "working: started"); err != nil {
		t.Fatal(err)
	}
	if err := AppendStatus(tmp, "test-task-3", "done: complete"); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadStatus(tmp, "test-task-3")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 status lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "working: started" {
		t.Errorf("line 0 = %q, want %q", lines[0], "working: started")
	}
	if lines[1] != "done: complete" {
		t.Errorf("line 1 = %q, want %q", lines[1], "done: complete")
	}
}

func TestReadStatus_Nonexistent(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	lines, err := ReadStatus(tmp, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if lines != nil {
		t.Errorf("expected nil for nonexistent status, got %v", lines)
	}
}

func TestStateDir(t *testing.T) {
	dir := StateDir("/tmp/munsu-home")
	want := "/tmp/munsu-home/state"
	if dir != want {
		t.Errorf("StateDir = %q, want %q", dir, want)
	}
}

func TestValidMetaFields(t *testing.T) {
	expected := []string{
		"window", "worktree", "project", "harness",
		"model", "effort", "kind", "mode", "yolo",
		"backend", "herdr_session", "herdr_workspace_id", "herdr_tab_id", "herdr_pane_id",
		"pr_provider", "pr_owner", "pr_repo", "pr_number", "pr_url",
		"pr_base", "pr_base_ref", "pr_head_ref", "pr_head", "pr_head_sha", "pr_timestamp",
	}
	if len(ValidMetaFields) != len(expected) {
		t.Fatalf("ValidMetaFields length = %d, want %d", len(ValidMetaFields), len(expected))
	}
	for i, f := range expected {
		if ValidMetaFields[i] != f {
			t.Errorf("ValidMetaFields[%d] = %q, want %q", i, ValidMetaFields[i], f)
		}
	}
}

func TestMetaPath_RespectsHomeOverride(t *testing.T) {
	os.Unsetenv("MUNSU_HOME")
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	fullPath, err := metaPath(tmp, "test-id")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, "state", "test-id.meta")
	if fullPath != want {
		t.Errorf("metaPath() = %q, want %q", fullPath, want)
	}
}

// TestStateDirCreatedByWrite checks that WriteMeta creates the state dir.
func TestStateDirCreatedByWrite(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	statePath := filepath.Join(tmp, "state")
	if _, err := os.Stat(statePath); err == nil {
		os.RemoveAll(statePath)
	}

	if err := WriteMeta(tmp, "dir-test", map[string]string{"window": "@1"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("state dir should have been created: %v", err)
	}
}

func TestResolveIntegration(t *testing.T) {
	// Verify home.Resolve works with MUNSU_HOME set
	os.Unsetenv("MUNSU_HOME")
	tmp := t.TempDir()
	os.Setenv("MUNSU_HOME", tmp)
	defer os.Unsetenv("MUNSU_HOME")

	h, err := home.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if h != tmp {
		t.Errorf("home.Resolve() = %q, want %q", h, tmp)
	}
}

func TestIsValidStatusState(t *testing.T) {
	valid := []string{"working", "needs-decision", "blocked", "paused", "resolved", "done", "failed"}
	for _, s := range valid {
		if !IsValidStatusState(s) {
			t.Errorf("%q should be a valid status state", s)
		}
	}

	invalid := []string{"", "unknown", "pending", "in-progress", "started"}
	for _, s := range invalid {
		if IsValidStatusState(s) {
			t.Errorf("%q should not be a valid status state", s)
		}
	}
}

func TestValidStatusStates(t *testing.T) {
	expected := []string{
		"working", "needs-decision", "blocked", "paused",
		"awaiting_approval", "resolved", "done", "failed",
	}
	if len(ValidStatusStates) != len(expected) {
		t.Fatalf("ValidStatusStates length = %d, want %d", len(ValidStatusStates), len(expected))
	}
	for i, s := range expected {
		if ValidStatusStates[i] != s {
			t.Errorf("ValidStatusStates[%d] = %q, want %q", i, ValidStatusStates[i], s)
		}
	}
}

func TestParseStatusKey(t *testing.T) {
	tests := []struct {
		line    string
		message string
		key     string
	}{
		{"working: started", "working: started", ""},
		{"needs-decision: pick approach [key=approach]", "needs-decision: pick approach", "approach"},
		{"resolved: chose A [key=approach]", "resolved: chose A", "approach"},
		{"blocked: waiting [key=dep]", "blocked: waiting", "dep"},
		{"done: all done [key=task-1]", "done: all done", "task-1"},
		{"key only [key=]", "key only [key=]", ""},
		{"no brackets", "no brackets", ""},
		{"[key=value]", "", "value"}, // bare key, extracts key with empty message
	}

	for _, tt := range tests {
		msg, key := ParseStatusKey(tt.line)
		if msg != tt.message {
			t.Errorf("ParseStatusKey(%q) message = %q, want %q", tt.line, msg, tt.message)
		}
		if key != tt.key {
			t.Errorf("ParseStatusKey(%q) key = %q, want %q", tt.line, key, tt.key)
		}
	}
}

func TestRemoveStatusKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"working: started", "working: started"},
		{"done: done [key=x]", "done: done"},
		{"blocked: blocked [key=dep]", "blocked: blocked"},
		{"no key here", "no key here"},
	}

	for _, tt := range tests {
		got := RemoveStatusKey(tt.input)
		if got != tt.want {
			t.Errorf("RemoveStatusKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPromoteMeta(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	// Create a scout meta
	if err := WriteMeta(tmp, "scout-task", map[string]string{
		"kind":     "scout",
		"window":   "@1",
		"worktree": "/tmp/wt",
	}); err != nil {
		t.Fatal(err)
	}

	// Promote it
	if err := PromoteMeta(tmp, "scout-task"); err != nil {
		t.Fatal(err)
	}

	// Verify kind changed
	meta, err := ReadMeta(tmp, "scout-task")
	if err != nil {
		t.Fatal(err)
	}
	if meta["kind"] != "ship" {
		t.Errorf("kind = %q, want ship", meta["kind"])
	}
	// Other fields preserved
	if meta["window"] != "@1" {
		t.Errorf("window = %q, want @1", meta["window"])
	}
}

func TestPromoteMeta_NotScout(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := WriteMeta(tmp, "ship-task", map[string]string{
		"kind": "ship",
	}); err != nil {
		t.Fatal(err)
	}

	err := PromoteMeta(tmp, "ship-task")
	if err == nil {
		t.Fatal("expected error promoting non-scout task")
	}
}

func TestPromoteMeta_NoMeta(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	err := PromoteMeta(tmp, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestPromoteMeta_EmptyKind(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := WriteMeta(tmp, "no-kind", map[string]string{}); err != nil {
		t.Fatal(err)
	}

	err := PromoteMeta(tmp, "no-kind")
	if err == nil {
		t.Fatal("expected error promoting task with empty kind")
	}
}

func TestPromoteMeta_PreservesAllFields(t *testing.T) {
	tmp := t.TempDir()
	setHomeEnv(t, tmp)

	if err := WriteMeta(tmp, "full-scout", map[string]string{
		"kind":     "scout",
		"window":   "@42",
		"worktree": "/tmp/test-wt",
		"project":  "test-project",
		"harness":  "pi",
		"model":    "claude-sonnet-4-20250515",
		"mode":     "no-mistakes",
		"yolo":     "off",
	}); err != nil {
		t.Fatal(err)
	}

	if err := PromoteMeta(tmp, "full-scout"); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadMeta(tmp, "full-scout")
	if err != nil {
		t.Fatal(err)
	}

	if meta["kind"] != "ship" {
		t.Errorf("kind = %q, want ship", meta["kind"])
	}
	if meta["window"] != "@42" {
		t.Errorf("window = %q, want @42", meta["window"])
	}
	if meta["harness"] != "pi" {
		t.Errorf("harness = %q, want pi", meta["harness"])
	}
}
