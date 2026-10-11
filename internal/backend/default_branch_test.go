package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultBranch(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	defaultBranchGit(t, "", "git", "init", "--bare", "-b", "release/1", remote)
	repo := filepath.Join(root, "repo")
	defaultBranchGit(t, "", "git", "init", "-b", "release/1", repo)
	defaultBranchGit(t, repo, "git", "config", "user.email", "test@test.com")
	defaultBranchGit(t, repo, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	defaultBranchGit(t, repo, "git", "add", ".")
	defaultBranchGit(t, repo, "git", "commit", "-m", "init")
	defaultBranchGit(t, repo, "git", "remote", "add", "origin", remote)
	defaultBranchGit(t, repo, "git", "push", "origin", "release/1")

	t.Run("empty repo has no default branch", func(t *testing.T) {
		empty := filepath.Join(root, "empty")
		defaultBranchGit(t, "", "git", "init", empty)
		if _, err := DefaultBranch(empty); err == nil || !strings.Contains(err.Error(), "cannot resolve default branch") {
			t.Fatalf("DefaultBranch = %v, want unresolved refusal", err)
		}
	})

	t.Run("origin/HEAD with a slash in the branch name", func(t *testing.T) {
		defaultBranchGit(t, repo, "git", "remote", "set-head", "origin", "release/1")
		got, err := DefaultBranch(repo)
		if err != nil || got != "release/1" {
			t.Fatalf("DefaultBranch = %q, %v; want release/1", got, err)
		}
	})

	t.Run("unset origin/HEAD falls back to origin/main", func(t *testing.T) {
		defaultBranchGit(t, repo, "git", "remote", "set-head", "origin", "--delete")
		defaultBranchGit(t, repo, "git", "push", "origin", "release/1:main")
		got, err := DefaultBranch(repo)
		if err != nil || got != "main" {
			t.Fatalf("DefaultBranch = %q, %v; want main", got, err)
		}
		if _, err := OriginHeadBranch(repo); err == nil {
			t.Fatal("OriginHeadBranch resolved with origin/HEAD unset; it has no fallback")
		}
	})

	t.Run("malformed origin/HEAD is refused, not skipped", func(t *testing.T) {
		defaultBranchGit(t, repo, "git", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/upstream/main")
		if _, err := DefaultBranch(repo); err == nil || !strings.Contains(err.Error(), "unexpected origin/HEAD symbolic ref format") {
			t.Fatalf("DefaultBranch = %v, want malformed-format refusal", err)
		}
	})
}

func defaultBranchGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v failed: %v\n%s", args, err, out)
	}
}
