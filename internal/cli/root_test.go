package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckTangle(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a bare origin repo
	originDir := filepath.Join(tmpDir, "origin.git")
	runCmd(t, "", "git", "init", "--bare", originDir)

	// Clone from the bare origin so origin/HEAD is set up
	repoDir := filepath.Join(tmpDir, "repo")
	runCmd(t, "", "git", "clone", originDir, repoDir)

	// Create an initial commit on the default branch
	runCmd(t, repoDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, repoDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "initial")
	runCmd(t, repoDir, "git", "push", "-u", "origin", "HEAD")
	runCmd(t, repoDir, "git", "remote", "set-head", "origin", "--auto")

	// Determine the default branch from origin/HEAD
	out, err := exec.Command("git", "-C", repoDir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		t.Fatalf("origin/HEAD not set: %v", err)
	}
	defaultBranch := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
	t.Logf("default branch: %s", defaultBranch)

	// Test 1: on default branch -> no tangle
	if err := checkTangle(repoDir, "test-project"); err != nil {
		t.Fatalf("expected no error on default branch %q, got: %v", defaultBranch, err)
	}

	// Test 2: create and switch to a feature branch -> tangle
	runCmd(t, repoDir, "git", "checkout", "-b", "feature-branch")
	err = checkTangle(repoDir, "test-project")
	if err == nil {
		t.Fatal("expected tangle error for feature branch, got nil")
	}
	if !strings.Contains(err.Error(), "cannot spawn") {
		t.Fatalf("expected 'cannot spawn' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "test-project") {
		t.Fatalf("expected project name 'test-project' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "feature-branch") {
		t.Fatalf("expected branch name 'feature-branch' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Use a detached HEAD or a worktree") {
		t.Fatalf("expected remediation suggestion in error, got: %v", err)
	}

	// Test 3: switch back to default branch -> no tangle
	runCmd(t, repoDir, "git", "checkout", defaultBranch)
	if err := checkTangle(repoDir, "test-project"); err != nil {
		t.Fatalf("expected no error on default branch, got: %v", err)
	}

	// Test 4: detached HEAD -> no tangle
	runCmd(t, repoDir, "git", "checkout", "--detach", defaultBranch)
	if err := checkTangle(repoDir, "test-project"); err != nil {
		t.Fatalf("expected no error on detached HEAD, got: %v", err)
	}

	// Test 5: non-existent directory -> no error (skip check gracefully)
	if err := checkTangle(filepath.Join(tmpDir, "nonexistent"), "test-project"); err != nil {
		t.Fatalf("expected no error for nonexistent dir, got: %v", err)
	}

	// Test 6: non-git directory -> no error (skip check gracefully)
	plainDir := filepath.Join(tmpDir, "plain")
	if err := os.MkdirAll(plainDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := checkTangle(plainDir, "test-project"); err != nil {
		t.Fatalf("expected no error for non-git dir, got: %v", err)
	}
	// Test 7: no remote but main branch exists (fallback path)
	noRemoteDir := filepath.Join(tmpDir, "no-remote")
	runCmd(t, "", "git", "init", noRemoteDir)
	runCmd(t, noRemoteDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, noRemoteDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(noRemoteDir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, noRemoteDir, "git", "add", ".")
	runCmd(t, noRemoteDir, "git", "commit", "-m", "initial")

	// On main (default) -> no tangle
	if err := checkTangle(noRemoteDir, "test-project"); err != nil {
		t.Fatalf("expected no error on default branch (no remote), got: %v", err)
	}

	// On feature branch -> tangle (main detected via fallback)
	runCmd(t, noRemoteDir, "git", "checkout", "-b", "feature-branch")
	err = checkTangle(noRemoteDir, "test-project")
	if err == nil {
		t.Fatal("expected tangle error on feature branch (no remote, fallback), got nil")
	}
	if !strings.Contains(err.Error(), "cannot spawn") {
		t.Fatalf("expected 'cannot spawn' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "feature-branch") {
		t.Fatalf("expected branch name 'feature-branch' in error, got: %v", err)
	}

	// Detached HEAD on no-remote repo -> no tangle
	defaultBranchBR := "main"
	runCmd(t, noRemoteDir, "git", "checkout", "--detach", defaultBranchBR)
	if err := checkTangle(noRemoteDir, "test-project"); err != nil {
		t.Fatalf("expected no error on detached HEAD (no remote), got: %v", err)
	}
}

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %s %v failed: %v\n%s", name, args, err, string(out))
	}
}
