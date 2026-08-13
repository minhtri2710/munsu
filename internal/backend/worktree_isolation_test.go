package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// isolationRepo creates a primary checkout with one commit and a nested
// subdirectory, returning the repository root.
func isolationRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitIso(t, root, "init")
	gitIso(t, root, "config", "user.email", "test@test.com")
	gitIso(t, root, "config", "user.name", "Test")
	sub := filepath.Join(root, "internal", "backend")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file.go"), []byte("package backend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIso(t, root, "add", ".")
	gitIso(t, root, "commit", "-m", "initial")
	return root
}

// isolationWorktree creates a primary checkout plus one linked worktree and
// returns the worktree path. Tests that need a genuinely isolated checkout must
// build one instead of assuming the directory the test binary runs in is a
// worktree — that holds under a soldier but not in CI, where it is a clone.
func isolationWorktree(t *testing.T) string {
	t.Helper()
	root := isolationRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitIso(t, root, "worktree", "add", "--detach", wt)
	return wt
}

func gitIso(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// TestIsIsolated_PrimaryCheckoutSubdirectory is the regression test for the
// guard bug: from a subdirectory of a primary checkout git answers --git-dir
// absolutely and --git-common-dir relatively, which made IsIsolated report a
// primary checkout as an isolated worktree. Running from the repository root
// does not reproduce it, so both positions are asserted here.
func TestIsIsolated_PrimaryCheckoutSubdirectory(t *testing.T) {
	root := isolationRepo(t)

	for _, tc := range []struct {
		name string
		path string
	}{
		{"root", root},
		{"subdirectory", filepath.Join(root, "internal", "backend")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolated, err := IsIsolated(tc.path)
			if err != nil {
				t.Fatalf("IsIsolated(%s): %v", tc.path, err)
			}
			if isolated {
				t.Errorf("IsIsolated(%s) = true, want false (primary checkout)", tc.path)
			}
		})
	}
}

// TestEnsureNotPrimary_PrimaryCheckoutRejected covers the guard itself: it must
// fail closed on a primary checkout, from the root and from a subdirectory.
func TestEnsureNotPrimary_PrimaryCheckoutRejected(t *testing.T) {
	root := isolationRepo(t)

	for _, tc := range []struct {
		name string
		path string
	}{
		{"root", root},
		{"subdirectory", filepath.Join(root, "internal", "backend")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := EnsureNotPrimary(tc.path); err == nil {
				t.Errorf("EnsureNotPrimary(%s) = nil, want error (primary checkout)", tc.path)
			}
		})
	}
}

// TestIsIsolated_RealWorktree pins the other half of the contract: a genuine
// linked worktree still reports isolated, from its root and from a
// subdirectory, so the fix cannot be "always return false".
func TestIsIsolated_RealWorktree(t *testing.T) {
	wt := isolationWorktree(t)

	for _, tc := range []struct {
		name string
		path string
	}{
		{"root", wt},
		{"subdirectory", filepath.Join(wt, "internal", "backend")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolated, err := IsIsolated(tc.path)
			if err != nil {
				t.Fatalf("IsIsolated(%s): %v", tc.path, err)
			}
			if !isolated {
				t.Errorf("IsIsolated(%s) = false, want true (linked worktree)", tc.path)
			}
			if err := EnsureNotPrimary(tc.path); err != nil {
				t.Errorf("EnsureNotPrimary(%s) = %v, want nil", tc.path, err)
			}
		})
	}
}
