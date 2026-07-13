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

	if err := WriteMeta("test-task-1", meta); err != nil {
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

	meta, err := ReadMeta("test-task-2")
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

	_, err := ReadMeta("nonexistent")
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

	meta, err := ReadMeta("empty")
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

	meta, err := ReadMeta("comments")
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

	if err := WriteMeta("roundtrip", original); err != nil {
		t.Fatal(err)
	}

	got, err := ReadMeta("roundtrip")
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

	if err := AppendStatus("test-task-3", "working: started"); err != nil {
		t.Fatal(err)
	}
	if err := AppendStatus("test-task-3", "done: complete"); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadStatus("test-task-3")
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

	lines, err := ReadStatus("nonexistent")
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

	fullPath, err := metaPath("test-id")
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

	if err := WriteMeta("dir-test", map[string]string{"window": "@1"}); err != nil {
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
