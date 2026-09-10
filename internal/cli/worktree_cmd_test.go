package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func setupClaimedWorktreeReturn(t *testing.T) (tracePath string) {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	initCLITestHome(t, homeDir)
	const worktreePath = "/pool/claimed-worktree"
	if err := home.WriteMeta(homeDir, "claimed-task", map[string]string{"worktree": worktreePath}); err != nil {
		t.Fatalf("writing claimed task meta: %v", err)
	}
	tracePath = homeDir + "/treehouse-returns"
	testutil.FakeOnPath(t, "treehouse", "#!/bin/sh\n"+
		"if [ \"$1\" = \"status\" ] && [ \"$2\" = \"--json\" ]; then\n"+
		"  printf '[{\\\"path\\\":\\\"/pool/claimed-worktree\\\",\\\"lease_holder\\\":\\\"\\\"}]\\n'\n"+
		"  exit 0\n"+
		"fi\n"+
		"if [ \"$1\" = \"return\" ]; then\n"+
		"  printf '%s\\n' \"$3\" >> \""+tracePath+"\"\n"+
		"  exit 0\n"+
		"fi\n"+
		"exit 1\n")
	return tracePath
}

func TestWorktreeReturnRefusesClaimedWorktree(t *testing.T) {
	tracePath := setupClaimedWorktreeReturn(t)
	const worktreePath = "/pool/claimed-worktree"
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

func TestWorktreeReturnForceOverridesClaim(t *testing.T) {
	tracePath := setupClaimedWorktreeReturn(t)
	const worktreePath = "/pool/claimed-worktree"
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
