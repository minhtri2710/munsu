//go:build integration

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitFallback_GetAndReturnWorktree(t *testing.T) {
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

	// Verify it is a real linked worktree: git marks one with a .git *file*
	// pointing at the common dir, where a primary checkout has a directory.
	// Asserted locally — internal/backend does not classify checkout identity
	// (ADR-0009) and must not import internal/fleet to do so.
	info, err := os.Lstat(filepath.Join(wtPath, ".git"))
	if err != nil {
		t.Fatalf("stat .git in worktree: %v", err)
	}
	if info.IsDir() {
		t.Error("expected linked worktree from git fallback, got a primary checkout (.git is a directory)")
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
	os.Setenv("PATH", oldPath)
	fakeTreehouseOnPath(t, fakeCmd{stdout: "ok"})
	p2, err2 := selectProvider("")
	if err2 != nil {
		t.Fatalf("selectProvider with treehouse on PATH should succeed, got: %v", err2)
	}
	if _, ok := p2.(*treehouseProvider); !ok {
		t.Errorf("expected treehouseProvider when treehouse present, got %T", p2)
	}
}
func TestGet_EmptyPath_ReturnsError(t *testing.T) {
	// A treehouse that returns empty stdout.
	fakeTreehouseOnPath(t, fakeCmd{})

	repoDir := t.TempDir()

	// Get without lease should return empty -> error
	_, err := GetWorktree(t.TempDir(), repoDir, false)
	if err == nil {
		t.Fatal("expected error for empty path from treehouse, got nil")
	}
	if !strings.Contains(err.Error(), "empty path") {
		t.Errorf("expected 'empty path' in error, got: %v", err)
	}
}

func TestGet_WithLease_ReturnsPath(t *testing.T) {
	// A treehouse that returns a path.
	fakeTreehouseOnPath(t, fakeCmd{stdout: "/tmp/wt-12345"})

	repoDir := t.TempDir()

	path, err := GetWorktree(t.TempDir(), repoDir, true)
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
	// treehouse return without --force prompts interactively when the worktree
	// has uncommitted changes. With stdin closed / no tty, it prints "Aborted"
	// and exits 0. Our Return must detect this as an error.
	fakeTreehouseOnPath(t, fakeCmd{stdout: "Aborted"})

	err := ReturnWorktree(t.TempDir(), "/some/wt-path")
	if err == nil {
		t.Fatal("expected error for Aborted output, got nil")
	}
	if !strings.Contains(err.Error(), "Aborted") {
		t.Errorf("expected 'Aborted' in error, got: %v", err)
	}
}

func TestReturn_Clean_ReturnsNil(t *testing.T) {
	// Clean success: "worktree returned to pool" and exit 0.
	fakeTreehouseOnPath(t, fakeCmd{stdout: "worktree returned to pool"})

	err := ReturnWorktree(t.TempDir(), "/some/wt-path")
	if err != nil {
		t.Fatalf("expected no error for clean return, got: %v", err)
	}
}

func TestReturn_ErrorExit_ReturnsError(t *testing.T) {
	// treehouse exit non-zero (e.g. path not found)
	fakeTreehouseOnPath(t, fakeCmd{stdout: "path not found", exitCode: 1})

	err := ReturnWorktree(t.TempDir(), "/some/wt-path")
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

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %s %v failed: %v\n%s", name, args, err, string(out))
	}
}

func TestGet_RelativeRepoPath_PassesAbsolute(t *testing.T) {
	// Regression: treehouseProvider.Get must pass an ABSOLUTE repo path to
	// `treehouse get` and use it as cmd.Dir. A relative repoPath previously
	// produced a cryptic "not a directory" error.
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	fakeTreehouseOnPath(t, fakeCmd{stdout: "/tmp/wt-rel", argsFile: argsFile})

	// Build a real repo dir and reference it by a RELATIVE path from its parent.
	repoDir := t.TempDir()
	parent := filepath.Dir(repoDir)
	rel := filepath.Base(repoDir)

	// chdir to parent so the relative name resolves (package tests are sequential).
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}

	path, err := GetWorktree(t.TempDir(), rel, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if path != "/tmp/wt-rel" {
		t.Errorf("expected path '/tmp/wt-rel', got: %q", path)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	// The fake records one argument per line in its own language's line
	// ending, so normalize before splitting: a windows CRLF would otherwise
	// leave a trailing \r on every recorded argument (#549 group 8).
	parts := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")), "\n")
	if len(parts) < 2 {
		t.Fatalf("expected >=2 args recorded, got %#v", parts)
	}
	if parts[0] != "get" {
		t.Errorf("first arg = %q, want \"get\"", parts[0])
	}
	if !filepath.IsAbs(parts[1]) {
		t.Errorf("repo arg = %q, want an absolute path", parts[1])
	}
	want, _ := filepath.Abs(rel)
	if parts[1] != want {
		t.Errorf("repo arg = %q, want %q", parts[1], want)
	}
}
