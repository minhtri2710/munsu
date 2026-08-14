package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildTaskWorktreeBinding is the only producer of taskauthority.WorktreeBinding,
// and its identity != Worktree refusal is the sole guard standing between a
// Soldier launch and the primary checkout (ADR-0009). These tests assert both
// directions of that guard: it refuses everything that is not a linked
// worktree, and it still admits a real one.

func bindingRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bindingGit(t, dir, "init")
	bindingGit(t, dir, "config", "user.email", "test@test.com")
	bindingGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bindingGit(t, dir, "add", ".")
	bindingGit(t, dir, "commit", "-m", "initial")
	return dir
}

func bindingGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// TestBuildTaskWorktreeBinding_RefusesNonWorktree pins the refusing direction.
// A primary checkout classifies as Primary and a plain directory as Unrelated;
// the whitelist admits neither, so both must fail closed with no binding.
func TestBuildTaskWorktreeBinding_RefusesNonWorktree(t *testing.T) {
	primary := bindingRepo(t)

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"primary checkout", primary, "worktree binding target is primary, not worktree"},
		{"primary subdirectory", filepath.Join(primary, "sub"), "worktree binding target is primary, not worktree"},
		{"non-git directory", t.TempDir(), "worktree binding target is unrelated, not worktree"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "primary subdirectory" {
				if err := os.MkdirAll(tc.path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			binding, err := buildTaskWorktreeBinding(primary, tc.path, "lease-1", "fence-1")
			if err == nil {
				t.Fatalf("buildTaskWorktreeBinding(%s) = %+v, want refusal", tc.path, binding)
			}
			if !strings.Contains(err.Error(), "not worktree") {
				t.Errorf("error = %q, want it to contain %q", err.Error(), "not worktree")
			}
			if err.Error() != tc.want {
				t.Errorf("error = %q, want %q", err.Error(), tc.want)
			}
			if binding.Path != "" || binding.GitDir != "" || binding.CommonDir != "" {
				t.Errorf("binding = %+v, want zero value on refusal", binding)
			}
		})
	}
}

// TestBuildTaskWorktreeBinding_AdmitsLinkedWorktree pins the admitting
// direction, so the guard cannot be satisfied by refusing everything. GitDir
// and CommonDir must differ — that inequality is what makes the path a linked
// worktree rather than the primary checkout.
func TestBuildTaskWorktreeBinding_AdmitsLinkedWorktree(t *testing.T) {
	primary := bindingRepo(t)
	worktree := filepath.Join(t.TempDir(), "wt")
	bindingGit(t, primary, "worktree", "add", "--detach", worktree)

	binding, err := buildTaskWorktreeBinding(primary, worktree, "lease-1", "fence-1")
	if err != nil {
		t.Fatalf("buildTaskWorktreeBinding(%s) = %v, want a binding", worktree, err)
	}
	if binding.GitDir == "" || binding.CommonDir == "" {
		t.Fatalf("binding git dirs empty: %+v", binding)
	}
	if binding.GitDir == binding.CommonDir {
		t.Errorf("GitDir == CommonDir (%s); a linked worktree must have them differ", binding.GitDir)
	}
	canonicalWorktree, err := canonicalExistingPath(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Path != canonicalWorktree {
		t.Errorf("Path = %q, want %q", binding.Path, canonicalWorktree)
	}
	if binding.Head == "" {
		t.Error("Head is empty, want the worktree HEAD")
	}
	if binding.LeaseID != "lease-1" || binding.FenceToken != "fence-1" {
		t.Errorf("lease/fence = %q/%q, want lease-1/fence-1", binding.LeaseID, binding.FenceToken)
	}
}
