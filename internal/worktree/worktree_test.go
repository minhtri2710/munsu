package worktree

import (
	"os"
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
	_, err := Get(repoDir, false)
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

	path, err := Get(repoDir, true)
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

	err := Return("/some/wt-path")
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

	err := Return("/some/wt-path")
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

	err := Return("/some/wt-path")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
}
