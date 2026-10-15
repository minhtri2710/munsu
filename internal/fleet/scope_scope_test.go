//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

// --- ClassifyIdentity tests ---

func TestClassifyIdentity_PrimaryCheckout(t *testing.T) {
	repo := initRepo(t, t.TempDir())

	identity, gitDir, commonDir, err := ClassifyIdentity(repo)
	if err != nil {
		t.Fatalf("ClassifyIdentity(%q) = _, _, _, %v", repo, err)
	}
	if identity != Primary {
		t.Errorf("ClassifyIdentity(%q) = %v, want Primary", repo, identity)
	}
	if gitDir != commonDir {
		t.Errorf("gitDir (%s) should equal commonDir (%s) for primary checkout", gitDir, commonDir)
	}
}

func TestClassifyIdentity_UnrelatedPath(t *testing.T) {
	tmp := t.TempDir()
	// No git repo here

	identity, gitDir, commonDir, err := ClassifyIdentity(tmp)
	if err != nil {
		t.Fatalf("ClassifyIdentity(%q) = _, _, _, %v", tmp, err)
	}
	if identity != Unrelated {
		t.Errorf("ClassifyIdentity(%q) = %v, want Unrelated", tmp, identity)
	}
	if gitDir != "" || commonDir != "" {
		t.Errorf("expected empty gitDir/commonDir for unrelated, got %q / %q", gitDir, commonDir)
	}
}

func TestClassifyIdentity_Worktree(t *testing.T) {
	// Create a primary repo
	primaryDir := initRepo(t, t.TempDir())

	// Create a worktree
	wtDir := filepath.Join(t.TempDir(), "worktree")
	scopeRunCmd(t, primaryDir, "git", "worktree", "add", wtDir)

	identity, gitDir, commonDir, err := ClassifyIdentity(wtDir)
	if err != nil {
		t.Fatalf("ClassifyIdentity(%q) = _, _, _, %v", wtDir, err)
	}
	if identity != Worktree {
		t.Errorf("ClassifyIdentity(%q) = %v, want Worktree", wtDir, identity)
	}
	if gitDir == commonDir {
		t.Errorf("gitDir (%s) should NOT equal commonDir (%s) for worktree", gitDir, commonDir)
	}
}

func TestClassifyIdentity_DeletedPathFailsClosed(t *testing.T) {
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist")
	identity, _, _, err := ClassifyIdentity(nonexistent)
	if err == nil {
		t.Fatal("expected deleted path classification error")
	}
	if identity != Unknown {
		t.Errorf("errored classification = %v, want Unknown (never a verdict it did not reach)", identity)
	}
}

// TestClassify_ErrorIdentityIsUnknown pins the zero value of Identity. It used
// to be Primary, so a Classify that failed before assigning anything reported
// "primary" for a path it could not classify at all — wrong evidence today,
// and a loaded trap for any future `if identity == Primary { block }` written
// without an Err check.
func TestClassify_ErrorIdentityIsUnknown(t *testing.T) {
	res := Classify(filepath.Join(t.TempDir(), "does-not-exist"))
	if res.Err == nil {
		t.Fatal("expected classification error for a deleted path")
	}
	if res.Identity != Unknown {
		t.Errorf("Identity = %v (%s), want Unknown", res.Identity, res.Identity)
	}
}

func TestClassifyIdentity_SymlinkedRepo(t *testing.T) {
	realDir := initRepo(t, t.TempDir())

	// Create a symlink to the real repo
	symDir := filepath.Join(t.TempDir(), "symlink-repo")
	if err := os.Symlink(realDir, symDir); err != nil {
		t.Fatal(err)
	}

	identity, gitDir, commonDir, err := ClassifyIdentity(symDir)
	if err != nil {
		t.Fatalf("ClassifyIdentity via symlink = _, _, _, %v", err)
	}
	// The symlinked path should resolve to the same identity as the real dir
	if identity != Primary {
		t.Errorf("ClassifyIdentity via symlink = %v, want Primary", identity)
	}
	if gitDir != commonDir {
		t.Errorf("gitDir (%s) should equal commonDir (%s) for primary via symlink", gitDir, commonDir)
	}
}

// --- Full classification tests ---

func TestClassify_Worktree(t *testing.T) {
	primaryDir := initRepo(t, t.TempDir())
	wtDir := filepath.Join(t.TempDir(), "wt")
	scopeRunCmd(t, primaryDir, "git", "worktree", "add", wtDir)

	res := Classify(wtDir)

	if res.Identity != Worktree {
		t.Errorf("Classify(%q).Identity = %v, want Worktree", wtDir, res.Identity)
	}
}

func TestClassify_Unrelated(t *testing.T) {
	tmp := t.TempDir()
	// Not a git repo

	res := Classify(tmp)
	if res.Err != nil {
		t.Fatalf("Classify(%q).Err = %v", tmp, res.Err)
	}
	if res.Identity != Unrelated {
		t.Errorf("Classify(%q).Identity = %v, want Unrelated", tmp, res.Identity)
	}
}

func TestClassifyIdentity_GitUnavailableFailsClosed(t *testing.T) {
	repo := initRepo(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	if _, _, _, err := ClassifyIdentity(repo); err == nil {
		t.Fatal("expected git unavailable error")
	}
}

// --- Malformed marker tests ---
