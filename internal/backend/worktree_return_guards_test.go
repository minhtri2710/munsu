package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// installReturnTrace puts a treehouse on PATH that records each invocation. A
// refused Return never reaches it, so the trace must stay absent.
func installReturnTrace(t *testing.T) string {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "treehouse-invoked")
	testutil.FakeOnPath(t, "treehouse", fmt.Sprintf("#!/bin/sh\n: > %q\nexit 0\n", trace))
	return trace
}

func assertNoReturnInvocation(t *testing.T, trace string) {
	t.Helper()
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("provider return invoked on a refused worktree: stat error=%v", err)
	}
}

func TestTreehouseReturnRefusesRelativePath(t *testing.T) {
	trace := installReturnTrace(t)
	err := (&treehouseProvider{}).Return("relative/worktree")
	if err == nil || !strings.Contains(err.Error(), "refusing to return worktree with a non-absolute path") {
		t.Fatalf("Return error = %v, want non-absolute path refusal", err)
	}
	assertNoReturnInvocation(t, trace)
}

func TestTreehouseReturnRefusesDirectoryGitMarker(t *testing.T) {
	trace := installReturnTrace(t)
	worktree := t.TempDir()
	if err := os.Mkdir(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := (&treehouseProvider{}).Return(worktree)
	if err == nil || !strings.Contains(err.Error(), "refusing to return worktree with a non-regular .git marker") {
		t.Fatalf("Return error = %v, want non-regular .git marker refusal", err)
	}
	assertNoReturnInvocation(t, trace)
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err != nil {
		t.Fatalf("directory .git marker was disturbed: %v", err)
	}
}

func TestTreehouseReturnRefusesNonGitdirMarker(t *testing.T) {
	trace := installReturnTrace(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("not a gitdir pointer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := (&treehouseProvider{}).Return(worktree)
	if err == nil || !strings.Contains(err.Error(), "unexpected .git file format: not a gitdir pointer") {
		t.Fatalf("Return error = %v, want unexpected .git file format refusal", err)
	}
	assertNoReturnInvocation(t, trace)
}

func TestGitWorktreeReturnRefusesRelativePath(t *testing.T) {
	p := &gitWorktreeProvider{homeDir: t.TempDir()}
	err := p.Return("relative/worktree")
	if err == nil || !strings.Contains(err.Error(), "refusing to remove worktree with a non-absolute path") {
		t.Fatalf("Return error = %v, want non-absolute path refusal", err)
	}
}

func TestGitWorktreeReturnRefusesDirectoryGitMarker(t *testing.T) {
	p := &gitWorktreeProvider{homeDir: t.TempDir()}
	worktree := t.TempDir()
	if err := os.Mkdir(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(worktree, "user-data.txt")
	if err := os.WriteFile(sentinel, []byte("must survive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := p.Return(worktree)
	if err == nil || !strings.Contains(err.Error(), "refusing to remove worktree with a non-regular .git marker") {
		t.Fatalf("Return error = %v, want non-regular .git marker refusal", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "must survive\n" {
		t.Fatalf("worktree content = %q, err=%v; refused return must not remove it", got, err)
	}
}
