package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
// path could otherwise act on.
func TestWorktreeReclaimRefusesUnreadableMeta(t *testing.T) {
	if _, err := exec.LookPath("treehouse"); err == nil {
		t.Skip("requires the git worktree fallback provider")
	}

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
	orphanDir := filepath.Join(tmpDir, ".worktrees", "orphan")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatalf("creating orphan worktree: %v", err)
	}
	orphanGit := filepath.Join(orphanDir, ".git")
	if err := os.WriteFile(orphanGit, []byte("gitdir: /nowhere"), 0o644); err != nil {
		t.Fatalf("seeding orphan worktree: %v", err)
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

	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdout pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = stdoutWriter
	outputDone := make(chan string, 1)
	go func() {
		output, readErr := io.ReadAll(stdout)
		if readErr != nil {
			outputDone <- "read stdout: " + readErr.Error()
			return
		}
		outputDone <- string(output)
	}()

	err = root.Execute()
	stdoutWriter.Close()
	os.Stdout = oldStdout
	output := <-outputDone
	stdout.Close()
	if err == nil {
		t.Fatal("reclaim must refuse when a listed task's meta is unreadable")
	}
	if !strings.Contains(err.Error(), "reading task meta") {
		t.Fatalf("reclaim must fail closed on the unreadable projection, got: %v", err)
	}
	if strings.Contains(output, "returning orphaned worktree") || strings.Contains(output, "Reclaimed") {
		t.Fatalf("reclaim must not act on an orphan after a meta read failure, got stdout: %q", output)
	}
	if _, err := os.Stat(orphanGit); err != nil {
		t.Fatalf("reclaim must leave the orphan worktree untouched: %v", err)
	}
}
