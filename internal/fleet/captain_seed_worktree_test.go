package fleet

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// assertNoCaptainExcludesFile fails when a failed seed left the captain
// worktree's core.excludesFile set.
func assertNoCaptainExcludesFile(t *testing.T, home string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", home, "config", "--worktree", "--get", "core.excludesFile").Output(); err == nil {
		t.Errorf("failed seed set worktree core.excludesFile = %q", out)
	}
}

func TestWriteWorktreeExcludesFailsWhenGitDirCannotBeResolved(t *testing.T) {
	_, home, _ := excludeFixture(t)
	realGitRun := gitRun
	t.Cleanup(func() { gitRun = realGitRun })
	gitRun = func(args ...string) (string, error) {
		if len(args) > 0 && args[len(args)-1] == "--absolute-git-dir" {
			return "fatal: not a git repository", errors.New("exit status 128")
		}
		return realGitRun(args...)
	}
	err := writeWorktreeExcludes(home, piIntegrationPath)
	if err == nil || !strings.Contains(err.Error(), "resolving worktree git dir") {
		t.Fatalf("err = %v, want worktree git dir resolution failure", err)
	}
	gitDir := strings.TrimSpace(gitTestRun(t, home, "rev-parse", "--absolute-git-dir"))
	if _, serr := os.Stat(filepath.Join(gitDir, worktreeExcludeFileName)); !os.IsNotExist(serr) {
		t.Errorf("failed seed wrote the excludes file: %v", serr)
	}
	assertNoCaptainExcludesFile(t, home)
}

func TestWriteWorktreeExcludesFailsWhenExcludesFileCannotBeWritten(t *testing.T) {
	_, home, _ := excludeFixture(t)
	gitDir := strings.TrimSpace(gitTestRun(t, home, "rev-parse", "--absolute-git-dir"))
	// A non-empty directory at the excludes path cannot be replaced by rename.
	blocker := filepath.Join(gitDir, worktreeExcludeFileName)
	if err := os.MkdirAll(filepath.Join(blocker, "occupied"), 0755); err != nil {
		t.Fatal(err)
	}
	err := writeWorktreeExcludes(home, piIntegrationPath)
	if err == nil || !strings.Contains(err.Error(), "writing "+blocker) {
		t.Fatalf("err = %v, want excludes file write failure", err)
	}
	assertNoCaptainExcludesFile(t, home)
	if leftovers, _ := filepath.Glob(filepath.Join(gitDir, ".home-write-*")); len(leftovers) != 0 {
		t.Errorf("failed seed left temp files: %v", leftovers)
	}
}
