package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// TestWorktreeReclaimRefusesUnreadableMeta is the F024 oracle. `worktree
// reclaim` decides which worktrees are live from the task-meta projection; a
// task whose .meta cannot be read is not evidence that it holds no worktree.
// Before the fix the command swallowed the ReadMeta error and skipped the
// task, so a live worktree behind an unreadable .meta would be classified
// orphaned and destroyed. The command must instead refuse and reclaim nothing.
//
// The .meta is made unreadable the way F003 proved destructive: a single line
// larger than bufio's default 64KB token makes ReadMeta fail with
// bufio.ErrTooLong while the file stays a readable regular file the reclaim
// path could otherwise act on. The refusal returns before any backend call, so
// the assertion does not depend on which worktree provider is selected.
func TestWorktreeReclaimRefusesUnreadableMeta(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	const id = "task-live"
	if err := home.WriteMeta(tmpDir, id, map[string]string{"worktree": "/pool/wt-live"}); err != nil {
		t.Fatalf("seeding task meta: %v", err)
	}

	metaPath, err := home.MetaFilePath(tmpDir, id)
	if err != nil {
		t.Fatalf("resolving meta path: %v", err)
	}
	oversized := "worktree=" + strings.Repeat("x", 70*1024) + "\n"
	if err := os.WriteFile(metaPath, []byte(oversized), 0o644); err != nil {
		t.Fatalf("corrupting task meta: %v", err)
	}
	// Precondition: the corrupted file is a readable regular file whose read now
	// fails; that is the state the reclaim path must refuse rather than skip.
	if _, err := home.ReadMeta(tmpDir, id); err == nil {
		t.Fatal("precondition: ReadMeta must fail on the oversized meta")
	}

	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "reclaim"})

	err = root.Execute()
	if err == nil {
		t.Fatal("reclaim must refuse when a listed task's meta is unreadable")
	}
	if !strings.Contains(err.Error(), "reading task meta") {
		t.Fatalf("reclaim must fail closed on the unreadable projection, got: %v", err)
	}
}
