package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// setHomeEnv sets MUNSU_HOME for the duration of a test.
func setHomeEnv(t *testing.T, path string) {
	t.Helper()
	os.Setenv("MUNSU_HOME", path)
	t.Cleanup(func() { os.Unsetenv("MUNSU_HOME") })
}

func TestDurableFilePathValidatesSuffixWithoutRejectingDotExtensions(t *testing.T) {
	dir := t.TempDir()
	for _, suffix := range []string{".meta", ".receipt"} {
		path, err := DurableFilePath(dir, "task-1", suffix)
		if err != nil {
			t.Fatalf("DurableFilePath suffix %q: %v", suffix, err)
		}
		if filepath.Dir(path) != filepath.Clean(dir) {
			t.Fatalf("DurableFilePath suffix %q escaped dir: %q", suffix, path)
		}
	}
	for _, suffix := range []string{"/escape", `\escape`, "/../escape", string([]byte{'x', 0, 'y'})} {
		if _, err := DurableFilePath(dir, "task-1", suffix); err == nil {
			t.Errorf("DurableFilePath suffix %q succeeded, want validation error", suffix)
		}
	}
}

func TestTaskMetadataRejectsStateSymlinkWithoutTouchingTarget(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, StateDir(home)); err != nil {
		t.Fatal(err)
	}
	if err := WriteMeta(home, "task-1", map[string]string{"kind": "ship"}); err == nil {
		t.Fatal("WriteMeta followed a symlinked state directory")
	}
	if err := AppendStatus(home, "task-1", "working: test"); err == nil {
		t.Fatal("AppendStatus followed a symlinked state directory")
	}
	if _, err := ReadMeta(home, "task-1"); err == nil {
		t.Fatal("ReadMeta accepted a symlinked state directory")
	}
	if _, err := ReadStatus(home, "task-1"); err == nil {
		t.Fatal("ReadStatus accepted a symlinked state directory")
	}
	if _, err := ListMeta(home); err == nil {
		t.Fatal("ListMeta accepted a symlinked state directory")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("state symlink target was modified: %v", entries)
	}
}

func TestWriteMetaRejectsControlCharactersWithoutPublishing(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]string
	}{
		{name: "empty key", meta: map[string]string{"": "value"}},
		{name: "equals in key", meta: map[string]string{"bad=key": "value"}},
		{name: "newline in key", meta: map[string]string{"bad\nkey": "value"}},
		{name: "newline in value", meta: map[string]string{"kind": "ship\nforged=field"}},
		{name: "carriage return in value", meta: map[string]string{"kind": "ship\rforged"}},
		{name: "control in value", meta: map[string]string{"kind": "ship\x01forged"}},
		{name: "control in key", meta: map[string]string{"bad\x01key": "value"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.name == "newline in value" {
				if err := WriteMeta(home, "task-1", map[string]string{"kind": "ship"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := WriteMeta(home, "task-1", tc.meta); err == nil {
				t.Fatal("WriteMeta accepted invalid metadata")
			}
			path, err := MetaFilePath(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "newline in value" {
				meta, err := ReadMeta(home, "task-1")
				if err != nil {
					t.Fatal(err)
				}
				if meta["kind"] != "ship" || meta["forged"] != "" {
					t.Fatalf("valid metadata was corrupted: %v", meta)
				}
				return
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("invalid metadata published a file: %v", err)
			}
		})
	}
}

func TestTaskMetadataRejectsPathTraversal(t *testing.T) {
	tmp := t.TempDir()
	for _, id := range []string{"", ".", "..", "../escape", "nested/task"} {
		if err := WriteMeta(tmp, id, map[string]string{"kind": "ship"}); err == nil {
			t.Errorf("WriteMeta(%q) succeeded, want validation error", id)
		}
		if err := AppendStatus(tmp, id, "working: test"); err == nil {
			t.Errorf("AppendStatus(%q) succeeded, want validation error", id)
		}
	}
}

func TestTaskMetadataDoesNotIncreaseExistingDirectoryPermissions(t *testing.T) {
	tmp := t.TempDir()
	state := filepath.Join(tmp, "state")
	if err := os.MkdirAll(state, 0755); err != nil {
		t.Fatal(err)
	}
	testutil.MakeDirectoryReadOnly(t, state)
	if err := WriteMeta(tmp, "restricted", map[string]string{"kind": "ship"}); err == nil {
		t.Fatal("WriteMeta succeeded in read-only state directory")
	}
	testutil.AssertOwnerReadOnly(t, state)
}

func TestTaskMetadataUsesPrivatePermissions(t *testing.T) {
	tmp := t.TempDir()
	if err := WriteMeta(tmp, "private", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendStatus(tmp, "private", "working: test"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(tmp, "state", "private.meta"),
		filepath.Join(tmp, "state", "private.meta.lock"),
		filepath.Join(tmp, "state", "private.status"),
	} {
		testutil.AssertOwnerPrivate(t, path)
	}
	testutil.AssertOwnerPrivate(t, filepath.Join(tmp, "state"))
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

func TestAppendStatusConcurrentSameTaskPreservesAllLinesAndMeta(t *testing.T) {
	home := t.TempDir()
	const taskID = "concurrent-task"
	initialMeta := map[string]string{"kind": "ship", "window": "window-initial"}
	if err := WriteMeta(home, taskID, initialMeta); err != nil {
		t.Fatalf("initial WriteMeta: %v", err)
	}

	const appenders = 16
	const metaWriters = 4
	start := make(chan struct{})
	errs := make(chan error, appenders+metaWriters)
	var wg sync.WaitGroup
	for i := 0; i < appenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			line := fmt.Sprintf("working: concurrent update %02d %s", i, strings.Repeat("x", 128))
			if err := AppendStatus(home, taskID, line); err != nil {
				errs <- fmt.Errorf("AppendStatus %02d: %w", i, err)
			}
		}(i)
	}
	for i := 0; i < metaWriters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			meta := map[string]string{"kind": "ship", "window": fmt.Sprintf("window-%02d", i)}
			if err := WriteMeta(home, taskID, meta); err != nil {
				errs <- fmt.Errorf("WriteMeta %02d: %w", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	lines, err := ReadStatus(home, taskID)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	gotLines := make(map[string]int, len(lines))
	for _, line := range lines {
		gotLines[line]++
	}
	if len(gotLines) != appenders || len(lines) != appenders {
		t.Fatalf("status lines = %d unique / %d total, want %d unique / %d total: %v", len(gotLines), len(lines), appenders, appenders, lines)
	}
	for i := 0; i < appenders; i++ {
		want := fmt.Sprintf("working: concurrent update %02d %s", i, strings.Repeat("x", 128))
		if gotLines[want] != 1 {
			t.Errorf("status line %q count = %d, want 1", want, gotLines[want])
		}
	}

	meta, err := ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta after concurrent writes: %v", err)
	}
	if meta["kind"] != "ship" || !strings.HasPrefix(meta["window"], "window-") {
		t.Fatalf("meta after concurrent writes = %v, want a complete valid generation", meta)
	}
}

func TestStateDir(t *testing.T) {
	root := absTestPath(t, "tmp", "munsu-home")
	dir := StateDir(root)
	want := filepath.Join(root, "state")
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
		"pr_base_ref", "pr_head_ref", "pr_head_sha", "pr_timestamp",
		"delivery_state", "pr_identity_revision",
		"amend_expected_head", "amend_started_at",
		"amendment_history",
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

	fullPath, err := MetaFilePath(tmp, "test-id")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, "state", "test-id.meta")
	if fullPath != want {
		t.Errorf("MetaFilePath() = %q, want %q", fullPath, want)
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

	h, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if h != tmp {
		t.Errorf("home.Resolve() = %q, want %q", h, tmp)
	}
}

func TestIsValidStatusState(t *testing.T) {
	valid := []string{"working", "review-ready", "amending", "needs-decision", "blocked", "paused", "resolved", "done", "failed"}
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
		"working", "review-ready", "amending", "needs-decision", "blocked", "paused",
		"awaiting_approval", "resolved", "done", "failed", "delivered",
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
