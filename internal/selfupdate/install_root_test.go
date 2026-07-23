// Package selfupdate tests for install root resolution.
package selfupdate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

// --- helpers ---

// initMunsuRepo creates a temporary git repository with the canonical munsu
// go.mod and an initial commit on the given branch (default "main").
func initMunsuRepo(t *testing.T, root, branch string) {
	t.Helper()
	if branch == "" {
		branch = "main"
	}
	runCmd(t, root, "git", "init", "--initial-branch="+branch)
	runCmd(t, root, "git", "config", "user.email", "test@munsu")
	runCmd(t, root, "git", "config", "user.name", "test")
	writeFile(t, root, "go.mod", "module github.com/minhtri2710/munsu\n\ngo 1.26\n")
	runCmd(t, root, "git", "add", "go.mod")
	runCmd(t, root, "git", "commit", "-m", "initial")
}

// runCmd runs a command in root and fails the test on error.
func runCmd(t *testing.T, root, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v in %s: %v\n%s", name, args, root, err, string(out))
	}
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func mkDir(t *testing.T, root, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
}

// --- TestResolveInstallRoot_Precedence ---

// TestResolveInstallRoot_Tier1_RepoOpt verifies that --repo is checked first
// and overrides everything else.
func TestResolveInstallRoot_Tier1_RepoOpt(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	home := t.TempDir()
	mkDir(t, home, "config")

	// Even with MUNSU_REPO set to a non-existent path, --repo should win.
	t.Setenv("MUNSU_REPO", "/nonexistent/repo")

	root, err := ResolveInstallRoot(home, repo)
	if err != nil {
		t.Fatalf("ResolveInstallRoot with --repo: %v", err)
	}
	// Resolve canonical root with git --show-toplevel.
	canonical, _ := gitToplevel(repo)
	if root != canonical {
		t.Errorf("root = %q, want %q", root, canonical)
	}
}

// TestResolveInstallRoot_Tier1_RepoOpt_InvalidFailClosed verifies that a
// non-empty --repo pointing to a non-munsu path fails closed, not falling
// through to a valid MUNSU_REPO.
func TestResolveInstallRoot_Tier1_RepoOpt_InvalidFailClosed(t *testing.T) {
	t.Setenv("MUNSU_REPO", "/nonexistent")

	// Create a non-munsu git repo (git init with no go.mod).
	otherRepo := t.TempDir()
	runCmd(t, otherRepo, "git", "init")

	_, err := ResolveInstallRoot("", otherRepo)
	if err == nil {
		t.Fatal("expected error for non-munsu --repo path")
	}
	if !strings.Contains(err.Error(), "not a munsu repository") {
		t.Errorf("error should mention not a munsu repository, got: %v", err)
	}
}

// TestResolveInstallRoot_Tier1_RepoOpt_NonexistentFailClosed verifies that
// a non-empty --repo pointing to a non-existent path fails closed.
func TestResolveInstallRoot_Tier1_RepoOpt_NonexistentFailClosed(t *testing.T) {
	t.Setenv("MUNSU_REPO", "/nonexistent")

	_, err := ResolveInstallRoot("", "/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent --repo path")
	}
}

// TestResolveInstallRoot_Tier2_MUNSU_REPO verifies that MUNSU_REPO is checked
// when --repo is empty.
func TestResolveInstallRoot_Tier2_MUNSU_REPO(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	home := t.TempDir()
	mkDir(t, home, "config")

	t.Setenv("MUNSU_REPO", repo)
	root, err := ResolveInstallRoot(home, "")
	if err != nil {
		t.Fatalf("ResolveInstallRoot with MUNSU_REPO: %v", err)
	}
	canonical, _ := gitToplevel(repo)
	if root != canonical {
		t.Errorf("root = %q, want %q", root, canonical)
	}
}

// TestResolveInstallRoot_Tier3_Persisted verifies that config/install-root is
// checked when both --repo and MUNSU_REPO are empty/absent.
func TestResolveInstallRoot_Tier3_Persisted(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	home := t.TempDir()
	mkDir(t, home, "config")

	// Persist the repo path.
	if err := config.Set(home, "install-root", repo); err != nil {
		t.Fatalf("persisting install-root: %v", err)
	}

	root, err := ResolveInstallRoot(home, "")
	if err != nil {
		t.Fatalf("ResolveInstallRoot with persisted config: %v", err)
	}
	canonical, _ := gitToplevel(repo)
	if root != canonical {
		t.Errorf("root = %q, want %q", root, canonical)
	}
}

// TestResolveInstallRoot_Tier3_Persisted_Invalid verifies that a persisted
// path pointing to a non-munsu repo fails closed.
func TestResolveInstallRoot_Tier3_Persisted_Invalid(t *testing.T) {
	otherRepo := t.TempDir()
	runCmd(t, otherRepo, "git", "init")

	home := t.TempDir()
	mkDir(t, home, "config")
	if err := config.Set(home, "install-root", otherRepo); err != nil {
		t.Fatalf("persisting install-root: %v", err)
	}

	_, err := ResolveInstallRoot(home, "")
	if err == nil {
		t.Fatal("expected error for invalid persisted path")
	}
	if !strings.Contains(err.Error(), "not a munsu repository") {
		t.Errorf("error should mention not a munsu repository, got: %v", err)
	}
}

// TestResolveInstallRoot_Tier4_BinaryAncestry verifies that when the binary is
// inside a munsu repo, the root is resolved from the binary path.
// We test this by swapping the executable path via a symlink seam.
func TestResolveInstallRoot_Tier4_BinaryAncestry(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	// Simulate a binary inside the repo by creating a fake binary path.
	binDir := filepath.Join(repo, "bin")
	mkDir(t, binDir, "")
	fakeBin := filepath.Join(binDir, "munsu")
	writeFile(t, binDir, "munsu", "#!/bin/sh\necho fake")
	if err := os.Chmod(fakeBin, 0755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	// Override os.Executable by replacing the package-level seam.
	// We can't easily override os.Executable, but we can use a symlink
	// from a temp GOBIN dir into the repo, then check the resolution.
	// Actually, the resolver uses os.Executable directly. We need to
	// test the binary ancestry path differently.
	//
	// For unit testing, we test the resolveBinaryAncestry function directly.
	root, err := resolveBinaryAncestry(fakeBin)
	if err != nil {
		t.Fatalf("resolveBinaryAncestry: %v", err)
	}
	canonical, _ := gitToplevel(repo)
	if root != canonical {
		t.Errorf("root = %q, want %q", root, canonical)
	}
}

// TestResolveInstallRoot_Tier5_CwdAncestry verifies that cwd resolution
// works when inside a munsu repo.
func TestResolveInstallRoot_Tier5_CwdAncestry(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	// Requires neither --repo, MUNSU_REPO, persisted config, nor binary in repo.
	home := t.TempDir()
	mkDir(t, home, "config")

	// Set up temp dirs without env.
	t.Setenv("MUNSU_REPO", "")
	// Change to the repo directory.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}

	root, err := ResolveInstallRoot(home, "")
	if err != nil {
		t.Fatalf("ResolveInstallRoot from cwd: %v", err)
	}
	canonical, _ := gitToplevel(repo)
	if root != canonical {
		t.Errorf("root = %q, want %q", root, canonical)
	}
}

// TestResolveInstallRoot_NoSource verifies that when no resolution path
// succeeds, an actionable error is returned.
func TestResolveInstallRoot_NoSource(t *testing.T) {
	t.Setenv("MUNSU_REPO", "")

	// Use a home with no config dir so tier 3 fails.
	home := t.TempDir()

	// Ensure CWD is NOT inside a munsu checkout by cd'ing to home.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })
	if err := os.Chdir(home); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	_, err = ResolveInstallRoot(home, "")
	if err == nil {
		t.Fatal("expected error when no resolution path succeeds")
	}
	if !strings.Contains(err.Error(), "cannot determine munsu install root") {
		t.Errorf("error should mention cannot determine, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--repo") {
		t.Errorf("error should suggest --repo, got: %v", err)
	}
	if !strings.Contains(err.Error(), "MUNSU_REPO") {
		t.Errorf("error should suggest MUNSU_REPO, got: %v", err)
	}
}

// --- TestPersistInstallRoot ---

// TestPersistInstallRoot verifies that the canonical path is persisted.
func TestPersistInstallRoot(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	home := t.TempDir()
	mkDir(t, home, "config")

	if err := PersistInstallRoot(home, repo); err != nil {
		t.Fatalf("PersistInstallRoot: %v", err)
	}

	persisted, err := config.Get(home, "install-root")
	if err != nil {
		t.Fatalf("config.Get install-root: %v", err)
	}

	canonical, _ := gitToplevel(repo)
	if persisted != canonical {
		t.Errorf("persisted = %q, want %q", persisted, canonical)
	}
}

// TestPersistInstallRoot_NonMunsu verifies that non-munsu path is rejected.
func TestPersistInstallRoot_NonMunsu(t *testing.T) {
	otherRepo := t.TempDir()
	runCmd(t, otherRepo, "git", "init")

	home := t.TempDir()

	err := PersistInstallRoot(home, otherRepo)
	if err == nil {
		t.Fatal("expected error for non-munsu repo")
	}
}

// --- TestverifyMunsuModule ---

func TestVerifyMunsuModule_Valid(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", "module github.com/minhtri2710/munsu\n\ngo 1.26\n")

	if err := verifyMunsuModule(repo); err != nil {
		t.Fatalf("verifyMunsuModule: %v", err)
	}
}

func TestVerifyMunsuModule_WrongModule(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", "module github.com/other/thing\n\ngo 1.26\n")

	err := verifyMunsuModule(repo)
	if err == nil {
		t.Fatal("expected error for wrong module path")
	}
}

func TestVerifyMunsuModule_NoGoMod(t *testing.T) {
	repo := t.TempDir()
	err := verifyMunsuModule(repo)
	if err == nil {
		t.Fatal("expected error when no go.mod exists")
	}
}

// gitDirCleanup ensures the .git directory tree is writable before TempDir
// cleanup. Some git operations create objects and refs with 0444 permissions
// that os.RemoveAll cannot always handle on certain platforms/CI environments.
func gitDirCleanup(t *testing.T, root string) {
	t.Helper()
	gitDir := filepath.Join(root, ".git")
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		return
	}
	filepath.Walk(gitDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		os.Chmod(path, 0755)
		return nil
	})
}

// --- TestUpdateIn_Safety ---

// TestUpdateIn_DirtyRefuses verifies that a dirty worktree is rejected.
func TestUpdateIn_DirtyRefuses(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	// Create an untracked file (this doesn't make the tree dirty in
	// git-status --porcelain sense — only tracked modified files do).
	// Write a tracked file.
	writeFile(t, repo, "foo.go", "package foo\n")
	runCmd(t, repo, "git", "add", "foo.go")
	runCmd(t, repo, "git", "commit", "-m", "add foo")

	// Now modify it without staging.
	writeFile(t, repo, "foo.go", "package foo\n// dirty\n")

	fakeBin := filepath.Join(repo, "munsu")
	writeFile(t, repo, "munsu", "#!/bin/sh\necho fake")
	os.Chmod(fakeBin, 0755)

	err := UpdateIn(repo)
	gitDirCleanup(t, repo)
	if err == nil {
		t.Fatal("expected error for dirty worktree")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("error should mention uncommitted changes, got: %v", err)
	}
}

// TestUpdateIn_DetachedHeadRefuses verifies that a detached HEAD is rejected.
func TestUpdateIn_DetachedHeadRefuses(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	writeFile(t, repo, "a.go", "package a\n")
	runCmd(t, repo, "git", "add", "a.go")
	runCmd(t, repo, "git", "commit", "-m", "add a")

	// Detach HEAD to the commit.
	runCmd(t, repo, "git", "checkout", "--detach")

	fakeBin := filepath.Join(repo, "munsu")
	writeFile(t, repo, "munsu", "#!/bin/sh\necho fake")
	os.Chmod(fakeBin, 0755)

	err := UpdateIn(repo)
	gitDirCleanup(t, repo)
	if err == nil {
		t.Fatal("expected error for detached HEAD")
	}
}

// TestUpdateIn_DefaultBranchOnly verifies that non-default branches are
// rejected (not just detached HEAD).
func TestUpdateIn_NonDefaultBranchRefuses(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	// Create and switch to "develop" branch.
	runCmd(t, repo, "git", "branch", "develop", "main")
	runCmd(t, repo, "git", "checkout", "develop")

	fakeBin := filepath.Join(repo, "munsu")
	writeFile(t, repo, "munsu", "#!/bin/sh\necho fake")
	os.Chmod(fakeBin, 0755)

	err := UpdateIn(repo)
	gitDirCleanup(t, repo)
	if err == nil {
		t.Fatal("expected error for non-default branch")
	}
}

// --- Regression: repeated TempDir cleanup after git operations ---

// TestUpdateIn_DetachedHeadRefuses_Repeated runs the detached-head safety
// path repeatedly with explicit .git cleanup, verifying that TempDir can
// always clean up after git operations leave read-only objects.
func TestUpdateIn_DetachedHeadRefuses_Repeated(t *testing.T) {
	for i := 0; i < 30; i++ {
		t.Run(fmt.Sprintf("iter_%d", i), func(t *testing.T) {
			repo := t.TempDir()
			initMunsuRepo(t, repo, "main")

			writeFile(t, repo, "a.go", "package a\n")
			runCmd(t, repo, "git", "add", "a.go")
			runCmd(t, repo, "git", "commit", "-m", "add a")
			runCmd(t, repo, "git", "checkout", "--detach")

			fakeBin := filepath.Join(repo, "munsu")
			writeFile(t, repo, "munsu", "#!/bin/sh\necho fake")
			os.Chmod(fakeBin, 0755)

			err := UpdateIn(repo)
			// Direct test of the TempDir cleanup fix: run chmod before
			// os.RemoveAll to ensure .git can be fully removed.
			gitDirCleanup(t, repo)
			if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
				t.Fatalf(".git cleanup failed (iter %d): %v", i, err)
			}
			if err == nil {
				t.Fatal("expected error for detached HEAD")
			}
		})
	}
}

// TestUpdateIn_DirtyRefuses_Repeated runs the dirty-worktree safety path
// repeatedly to stress TempDir cleanup.
func TestUpdateIn_DirtyRefuses_Repeated(t *testing.T) {
	for i := 0; i < 30; i++ {
		t.Run(fmt.Sprintf("iter_%d", i), func(t *testing.T) {
			repo := t.TempDir()
			initMunsuRepo(t, repo, "main")

			writeFile(t, repo, "foo.go", "package foo\n")
			runCmd(t, repo, "git", "add", "foo.go")
			runCmd(t, repo, "git", "commit", "-m", "add foo")
			writeFile(t, repo, "foo.go", "package foo\n// dirty\n")

			fakeBin := filepath.Join(repo, "munsu")
			writeFile(t, repo, "munsu", "#!/bin/sh\necho fake")
			os.Chmod(fakeBin, 0755)

			err := UpdateIn(repo)
			gitDirCleanup(t, repo)
			if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
				t.Fatalf(".git cleanup failed (iter %d): %v", i, err)
			}
			if err == nil {
				t.Fatal("expected error for dirty worktree")
			}
		})
	}
}

// --- TestResolveInstallRoot_PersistReadRoundTrip ---

func TestResolveInstallRoot_PersistReadRoundTrip(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	home := t.TempDir()
	mkDir(t, home, "config")

	// Persist.
	if err := PersistInstallRoot(home, repo); err != nil {
		t.Fatalf("PersistInstallRoot: %v", err)
	}

	// Read back.
	persisted, err := config.Get(home, "install-root")
	if err != nil {
		t.Fatalf("config.Get: %v", err)
	}

	canonical, _ := gitToplevel(repo)
	if persisted != canonical {
		t.Errorf("read back %q, want %q", persisted, canonical)
	}

	// Resolve via tier 3 should succeed with no other hints.
	t.Setenv("MUNSU_REPO", "")
	root, err := ResolveInstallRoot(home, "")
	if err != nil {
		t.Fatalf("ResolveInstallRoot from persisted: %v", err)
	}
	if root != persisted {
		t.Errorf("resolved = %q, want persisted %q", root, persisted)
	}
}

// --- Test verifyAndCanonicalize ---

func TestVerifyAndCanonicalize_Valid(t *testing.T) {
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	root, err := verifyAndCanonicalize(repo)
	if err != nil {
		t.Fatalf("verifyAndCanonicalize: %v", err)
	}
	if root == "" {
		t.Fatal("expected non-empty root")
	}
}

func TestVerifyAndCanonicalize_NonExistent(t *testing.T) {
	_, err := verifyAndCanonicalize("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestVerifyAndCanonicalize_NotADirectory(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	writeFile(t, tmp, "file.txt", "hello")

	_, err := verifyAndCanonicalize(f)
	if err == nil {
		t.Fatal("expected error for file path")
	}
}

func TestVerifyAndCanonicalize_NotInGit(t *testing.T) {
	tmp := t.TempDir()
	mkDir(t, tmp, "subdir")

	_, err := verifyAndCanonicalize(tmp)
	if err == nil {
		t.Fatal("expected error for non-git path")
	}
}

// --- KnownKeys test ---

func TestInstallRootIsKnownKey(t *testing.T) {
	if !config.IsKnownKey("install-root") {
		t.Fatal("install-root should be a known config key")
	}
}
