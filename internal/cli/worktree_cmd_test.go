package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func setupClaimedWorktreeReturn(t *testing.T) (worktreePath, tracePath string) {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	initCLITestHome(t, homeDir)
	worktreePath = filepath.Join(t.TempDir(), "claimed-worktree")
	if err := os.Mkdir(worktreePath, 0o755); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}
	if err := home.WriteMeta(homeDir, "claimed-task", map[string]string{"worktree": worktreePath}); err != nil {
		t.Fatalf("writing claimed task meta: %v", err)
	}
	tracePath = filepath.Join(homeDir, "treehouse-returns")
	testutil.FakeOnPath(t, "treehouse", "#!/bin/sh\n"+
		"if [ \"$1\" = \"status\" ] && [ \"$2\" = \"--json\" ]; then\n"+
		"  printf '[{\"path\":\"%s\",\"lease_holder\":\"\"}]\\n' '"+worktreePath+"'\n"+
		"  exit 0\n"+
		"fi\n"+
		"if [ \"$1\" = \"return\" ]; then\n"+
		"  printf '%s\\n' \"$3\" >> \""+tracePath+"\"\n"+
		"  exit 0\n"+
		"fi\n"+
		"exit 1\n")
	return worktreePath, tracePath
}

func TestWorktreeReturnRefusesClaimedWorktree(t *testing.T) {
	worktreePath, tracePath := setupClaimedWorktreeReturn(t)
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "return", worktreePath})

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "refusing to return claimed worktree") {
		t.Fatalf("return claimed worktree error = %v, want refusal", err)
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("backend return should not run, trace stat error = %v", err)
	}
}

func TestWorktreeReturnRefusesAliases(t *testing.T) {
	worktreePath, tracePath := setupClaimedWorktreeReturn(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(worktreePath, alias); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting cwd: %v", err)
	}
	relative, err := filepath.Rel(cwd, worktreePath)
	if err != nil {
		t.Fatalf("making relative path: %v", err)
	}
	for _, path := range []string{relative, alias} {
		root := NewRootCommand()
		root.SetOut(new(strings.Builder))
		root.SetErr(new(strings.Builder))
		root.SetArgs([]string{"worktree", "return", path})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "refusing to return claimed worktree") {
			t.Fatalf("return %q error = %v, want refusal", path, err)
		}
	}
	if _, err := os.Stat(tracePath); !os.IsNotExist(err) {
		t.Fatalf("backend return should not run, trace stat error = %v", err)
	}
}

func TestWorktreeReturnForceOverridesClaim(t *testing.T) {
	worktreePath, tracePath := setupClaimedWorktreeReturn(t)
	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "return", "--force", worktreePath})

	if err := root.Execute(); err != nil {
		t.Fatalf("forced return: %v", err)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("reading backend return trace: %v", err)
	}
	if !strings.Contains(string(trace), worktreePath) {
		t.Fatalf("backend return trace = %q, want %q", trace, worktreePath)
	}
}

func TestWorktreeReturnForceSkipsUnreadableClaims(t *testing.T) {
	worktreePath, tracePath := setupClaimedWorktreeReturn(t)
	homeDir := os.Getenv("MUNSU_HOME")
	metaPath, err := home.MetaFilePath(homeDir, "claimed-task")
	if err != nil {
		t.Fatalf("finding task meta: %v", err)
	}
	if err := os.WriteFile(metaPath, []byte(strings.Repeat("x", 128*1024)), 0o600); err != nil {
		t.Fatalf("corrupting task meta: %v", err)
	}

	root := NewRootCommand()
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	root.SetArgs([]string{"worktree", "return", "--force", worktreePath})

	if err := root.Execute(); err != nil {
		t.Fatalf("forced return with unreadable claims: %v", err)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("reading backend return trace: %v", err)
	}
	if !strings.Contains(string(trace), worktreePath) {
		t.Fatalf("backend return trace = %q, want %q", trace, worktreePath)
	}
}
