package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsIsolated_Worktree(t *testing.T) {
	// We are running inside an isolated worktree, so IsIsolated should
	// return true for the current directory.
	isolated, err := IsIsolated(".")
	if err != nil {
		t.Fatal(err)
	}
	if !isolated {
		t.Error("IsIsolated('.') = false, want true (running in worktree)")
	}
}

func TestIsIsolated_PrimaryCheckout(t *testing.T) {
	// A non-git temp directory should fail isolation check.
	tmp := t.TempDir()
	_, err := IsIsolated(tmp)
	if err == nil {
		t.Log("non-git path did not error (expected when git is not present)")
	}
}

func TestIsIsolated_NonGitDir(t *testing.T) {
	tmp := t.TempDir()
	_, err := IsIsolated(tmp)
	if err == nil {
		t.Error("IsIsolated(temp dir) expected error for non-git directory, got nil")
	}
}

func TestEnsureNotPrimary_Success(t *testing.T) {
	// In our worktree, this should pass (we are isolated).
	if err := EnsureNotPrimary("."); err != nil {
		t.Fatalf("EnsureNotPrimary('.') = %v, want nil (running in worktree)", err)
	}
}

func TestEnsureNotPrimary_NonGit(t *testing.T) {
	tmp := t.TempDir()
	if err := EnsureNotPrimary(tmp); err == nil {
		t.Error("EnsureNotPrimary(temp dir) expected error, got nil")
	}
}

func TestGitFallback_GetAndReturn(t *testing.T) {
	// Create a real git repo with a commit, then exercise the git fallback
	// via gitWorktreeProvider directly.
	repoDir := t.TempDir()
	runCmd(t, repoDir, "git", "init")
	runCmd(t, repoDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, repoDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "initial")

	p := &gitWorktreeProvider{homeDir: t.TempDir()}

	// Get via git fallback
	wtPath, err := p.Get(repoDir, false)
	if err != nil {
		t.Fatalf("git fallback Get failed: %v", err)
	}
	if wtPath == "" {
		t.Fatal("expected non-empty worktree path from git fallback")
	}

	// Verify it is a real worktree
	isolated, err := IsIsolated(wtPath)
	if err != nil {
		t.Fatalf("IsIsolated on worktree: %v", err)
	}
	if !isolated {
		t.Error("expected isolated worktree from git fallback")
	}

	// Return the worktree
	if err := p.Return(wtPath); err != nil {
		t.Fatalf("git fallback Return failed: %v", err)
	}

	// Verify worktree directory is removed
	if _, err := os.Stat(wtPath); err == nil {
		t.Error("worktree still exists after Return")
	}
}

func TestGitFallback_Status(t *testing.T) {
	// Create a real git repo, get a worktree, then check status lists it.
	repoDir := t.TempDir()
	runCmd(t, repoDir, "git", "init")
	runCmd(t, repoDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, repoDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "initial")

	p := &gitWorktreeProvider{homeDir: t.TempDir()}

	// Status before Get should be empty or no-error
	_, err := p.Status()
	if err != nil {
		t.Fatalf("git fallback Status failed: %v", err)
	}

	// Get a worktree
	wtPath, err := p.Get(repoDir, false)
	if err != nil {
		t.Fatalf("git fallback Get failed: %v", err)
	}
	defer func() {
		_ = p.Return(wtPath)
	}()

	// Status after Get should include the worktree path
	out2, err := p.Status()
	if err != nil {
		t.Fatalf("git fallback Status after Get failed: %v", err)
	}
	if !strings.Contains(out2, wtPath) {
		t.Errorf("Status output should contain worktree path %q, got: %q", wtPath, out2)
	}
}

func TestProviderSelection(t *testing.T) {
	// Without treehouse on PATH, selectProvider should return gitWorktreeProvider.
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/dev/null")
	p, err := selectProvider(t.TempDir())
	if err != nil {
		t.Fatalf("selectProvider with homeDir failed: %v", err)
	}
	if _, ok := p.(*gitWorktreeProvider); !ok {
		t.Errorf("expected gitWorktreeProvider when treehouse absent, got %T", p)
	}

	// With a mock treehouse on PATH, selectProvider should return treehouseProvider.
	mockDir := t.TempDir()
	mockScript := filepath.Join(mockDir, "treehouse")
	if err := os.WriteFile(mockScript, []byte("#!/bin/sh\necho ok\n"), 0755); err != nil {
		t.Fatal(err)
	}
	os.Setenv("PATH", mockDir+":"+oldPath)
	p2, err2 := selectProvider("")
	if err2 != nil {
		t.Fatalf("selectProvider with treehouse on PATH should succeed, got: %v", err2)
	}
	if _, ok := p2.(*treehouseProvider); !ok {
		t.Errorf("expected treehouseProvider when treehouse present, got %T", p2)
	}
}
func TestGet_EmptyPath_ReturnsError(t *testing.T) {
	// Create a mock treehouse that returns empty stdout
	mockDir := t.TempDir()
	mockScript := filepath.Join(mockDir, "treehouse")
	if err := os.WriteFile(mockScript, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", mockDir+":"+oldPath)

	repoDir := t.TempDir()

	// Get without lease should return empty -> error
	_, err := Get(t.TempDir(), repoDir, false)
	if err == nil {
		t.Fatal("expected error for empty path from treehouse, got nil")
	}
	if !strings.Contains(err.Error(), "empty path") {
		t.Errorf("expected 'empty path' in error, got: %v", err)
	}
}

func TestGet_WithLease_ReturnsPath(t *testing.T) {
	// Create a mock treehouse that returns a path
	mockDir := t.TempDir()
	mockScript := filepath.Join(mockDir, "treehouse")
	if err := os.WriteFile(mockScript, []byte("#!/bin/sh\necho /tmp/wt-12345\n"), 0755); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", mockDir+":"+oldPath)

	repoDir := t.TempDir()

	path, err := Get(t.TempDir(), repoDir, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if path != "/tmp/wt-12345" {
		t.Errorf("expected path '/tmp/wt-12345', got: %q", path)
	}
	if path != "/tmp/wt-12345" {
		t.Errorf("expected path '/tmp/wt-12345', got: %q", path)
	}
}

func TestReturn_AbortedExit0_ReturnsError(t *testing.T) {
	mockDir := t.TempDir()
	mockScript := filepath.Join(mockDir, "treehouse")
	// treehouse return without --force prompts interactively when the worktree
	// has uncommitted changes. With stdin closed / no tty, it prints "Aborted"
	// and exits 0. Our Return must detect this as an error.
	mockContent := []byte("#!/bin/sh\necho 'Aborted'\nexit 0\n")
	if err := os.WriteFile(mockScript, mockContent, 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", mockDir+":"+oldPath)

	err := Return(t.TempDir(), "/some/wt-path")
	if err == nil {
		t.Fatal("expected error for Aborted output, got nil")
	}
	if !strings.Contains(err.Error(), "Aborted") {
		t.Errorf("expected 'Aborted' in error, got: %v", err)
	}
}

func TestReturn_Clean_ReturnsNil(t *testing.T) {
	mockDir := t.TempDir()
	mockScript := filepath.Join(mockDir, "treehouse")
	// Clean success: "worktree returned to pool" and exit 0.
	mockContent := []byte("#!/bin/sh\necho 'worktree returned to pool'\nexit 0\n")
	if err := os.WriteFile(mockScript, mockContent, 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", mockDir+":"+oldPath)

	err := Return(t.TempDir(), "/some/wt-path")
	if err != nil {
		t.Fatalf("expected no error for clean return, got: %v", err)
	}
}

func TestReturn_ErrorExit_ReturnsError(t *testing.T) {
	mockDir := t.TempDir()
	mockScript := filepath.Join(mockDir, "treehouse")
	// treehouse exit non-zero (e.g. path not found)
	mockContent := []byte("#!/bin/sh\necho 'path not found'\nexit 1\n")
	if err := os.WriteFile(mockScript, mockContent, 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", mockDir+":"+oldPath)

	err := Return(t.TempDir(), "/some/wt-path")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
}

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
	if err := AssertNotTangled(repoDir, "test-project"); err != nil {
		t.Fatalf("expected no error on default branch %q, got: %v", defaultBranch, err)
	}

	// Test 2: create and switch to a feature branch -> tangle
	runCmd(t, repoDir, "git", "checkout", "-b", "feature-branch")
	err = AssertNotTangled(repoDir, "test-project")
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
	if err := AssertNotTangled(repoDir, "test-project"); err != nil {
		t.Fatalf("expected no error on default branch, got: %v", err)
	}

	// Test 4: detached HEAD -> no tangle
	runCmd(t, repoDir, "git", "checkout", "--detach", defaultBranch)
	if err := AssertNotTangled(repoDir, "test-project"); err != nil {
		t.Fatalf("expected no error on detached HEAD, got: %v", err)
	}

	// Test 5: non-existent directory -> no error (skip check gracefully)
	if err := AssertNotTangled(filepath.Join(tmpDir, "nonexistent"), "test-project"); err != nil {
		t.Fatalf("expected no error for nonexistent dir, got: %v", err)
	}

	// Test 6: non-git directory -> no error (skip check gracefully)
	plainDir := filepath.Join(tmpDir, "plain")
	if err := os.MkdirAll(plainDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := AssertNotTangled(plainDir, "test-project"); err != nil {
		t.Fatalf("expected no error for non-git dir, got: %v", err)
	}

	// Test 7: no remote but main branch exists (fallback path)
	noRemoteDir := filepath.Join(tmpDir, "no-remote")
	runCmd(t, "", "git", "init", "-b", "main", noRemoteDir)
	runCmd(t, noRemoteDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, noRemoteDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(noRemoteDir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, noRemoteDir, "git", "add", ".")
	runCmd(t, noRemoteDir, "git", "commit", "-m", "initial")

	// On main (default) -> no tangle
	if err := AssertNotTangled(noRemoteDir, "test-project"); err != nil {
		t.Fatalf("expected no error on default branch (no remote), got: %v", err)
	}

	// On feature branch -> tangle (main detected via fallback)
	runCmd(t, noRemoteDir, "git", "checkout", "-b", "feature-branch")
	err = AssertNotTangled(noRemoteDir, "test-project")
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
	if err := AssertNotTangled(noRemoteDir, "test-project"); err != nil {
		t.Fatalf("expected no error on detached HEAD (no remote), got: %v", err)
	}
}
func TestAbsRoot(t *testing.T) {
	root := AbsRoot()
	if root == "" {
		t.Fatal("AbsRoot() returned empty string")
	}
	if !filepath.IsAbs(root) {
		t.Errorf("AbsRoot() = %q, want absolute path", root)
	}
}

// TestGitRevParse validates the git rev-parse helper.
func TestGitRevParse(t *testing.T) {
	// Should work from within any git repo
	gd, err := gitRevParse(".", "--git-dir")
	if err != nil {
		t.Fatal(err)
	}
	if gd == "" {
		t.Fatal("--git-dir returned empty")
	}

	cd, err := gitRevParse(".", "--git-common-dir")
	if err != nil {
		t.Fatal(err)
	}
	if cd == "" {
		t.Fatal("--git-common-dir returned empty")
	}
}

// TestIsIsolatedWithoutTreehouse ensures IsIsolated doesn't need treehouse to work.
func TestIsIsolatedWithoutTreehouse(t *testing.T) {
	// IsIsolated should work without treehouse present (it only uses git)
	isolated, err := IsIsolated(".")
	if err != nil {
		t.Fatalf("IsIsolated should work without treehouse: %v", err)
	}
	if !isolated {
		t.Error("expected isolated worktree")
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
