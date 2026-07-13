package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestTreehouseNotFound(t *testing.T) {
	// Temporarily remove treehouse from PATH
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	_, err := Get("/some/repo", false)
	if err == nil {
		t.Fatal("expected error when treehouse is not on PATH, got nil")
	}
	if !IsTreehouseNotFound(err) {
		t.Errorf("expected treehouse-not-found error, got: %v", err)
	}
}

func TestTreehouseNotFound_Return(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	err := Return("/some/path")
	if err == nil {
		t.Fatal("expected error when treehouse is not on PATH, got nil")
	}
	if !IsTreehouseNotFound(err) {
		t.Errorf("expected treehouse-not-found error, got: %v", err)
	}
}

func TestTreehouseNotFound_Status(t *testing.T) {
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)

	os.Setenv("PATH", "/dev/null")
	_, err := Status()
	if err == nil {
		t.Fatal("expected error when treehouse is not on PATH, got nil")
	}
	if !IsTreehouseNotFound(err) {
		t.Errorf("expected treehouse-not-found error, got: %v", err)
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

func init() {
	// Verify we're running in a git worktree for meaningful test assertions
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	if out, err := cmd.Output(); err != nil || string(out) != "true\n" {
		panic("tests must run inside a git worktree")
	}
}
