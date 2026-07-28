package fleet

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPreflight_UnknownMode(t *testing.T) {
	_, err := Preflight("bogus-mode", "")
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
	if !strings.Contains(err.Error(), "unknown delivery mode") {
		t.Errorf("expected 'unknown delivery mode', got: %v", err)
	}
}

func TestPreflight_LocalOnlyAlwaysOK(t *testing.T) {
	result, err := Preflight("local-only", "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Feasible {
		t.Error("local-only should always be feasible")
	}
	if len(result.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(result.Checks))
	}
	if !result.Checks[0].OK {
		t.Errorf("git-configured check should be OK, got: %s", result.Checks[0].Detail)
	}
}

func TestPreflight_NoMistakes_BinaryCheck(t *testing.T) {
	t.Run("no binary on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		result, err := Preflight("no-mistakes", "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Feasible {
			t.Error("no-mistakes should not be feasible without binary on PATH")
		}
		if len(result.Checks) != 1 {
			t.Fatalf("expected 1 check, got %d", len(result.Checks))
		}
		if result.Checks[0].OK {
			t.Error("no-mistakes-binary should fail")
		}
		if !strings.Contains(result.Checks[0].Detail, "no-mistakes not on PATH") {
			t.Errorf("expected PATH guidance, got: %s", result.Checks[0].Detail)
		}
	})

	t.Run("binary on PATH", func(t *testing.T) {
		if _, err := exec.LookPath("no-mistakes"); err != nil {
			t.Skip("no-mistakes not on PATH, skipping positive test")
		}
		result, err := Preflight("no-mistakes", "")
		if err != nil {
			t.Fatal(err)
		}
		if !result.Feasible {
			t.Error("no-mistakes should be feasible when binary is on PATH")
		}
	})
}

func TestPreflight_DirectPR_GhAuth(t *testing.T) {
	t.Run("gh not available", func(t *testing.T) {
		// Use a PATH where gh is definitely not found
		t.Setenv("PATH", t.TempDir())
		result, err := Preflight("direct-PR", "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Feasible {
			t.Error("direct-PR should not be feasible without gh auth")
		}
		if len(result.Checks) == 0 {
			t.Fatal("expected at least one check")
		}
		if result.Checks[0].OK {
			t.Error("gh-auth check should fail")
		}
	})

	t.Run("gh auth active", func(t *testing.T) {
		cmd := exec.Command("gh", "auth", "status")
		if err := cmd.Run(); err != nil {
			t.Skip("gh not authenticated, skipping positive test")
		}
		result, err := Preflight("direct-PR", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Checks) == 0 {
			t.Fatal("expected at least one check")
		}
		if !result.Checks[0].OK {
			t.Errorf("gh-auth check should pass when authenticated, got: %s", result.Checks[0].Detail)
		}
	})
}

func TestPreflight_DirectPR_HasRemote(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "")

	// Without remote, the has-remote check should fail when repoPath is provided
	result, err := Preflight("direct-PR", repo)
	if err != nil {
		t.Fatal(err)
	}

	// gh auth might fail (expected in test env) but has-remote should be one of the checks
	var hasRemoteCheck *Check
	for i, c := range result.Checks {
		if c.Name == "has-remote" {
			hasRemoteCheck = &result.Checks[i]
			break
		}
	}
	if hasRemoteCheck == nil {
		t.Fatal("expected 'has-remote' check when repoPath is provided")
	}
	if hasRemoteCheck.OK {
		t.Error("has-remote should be false for repo without remotes")
	}

	// Now add a remote and verify it passes
	cmd := exec.Command("git", "remote", "add", "origin", "https://github.com/test/test.git")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %s", out)
	}

	result, err = Preflight("direct-PR", repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range result.Checks {
		if c.Name == "has-remote" && !c.OK {
			t.Errorf("has-remote should be true after adding remote, got: %s", c.Detail)
		}
	}
}

func TestPreflight_DirectPR_SkipRemoteWhenNoRepoPath(t *testing.T) {
	// Without repoPath, only gh auth should be checked
	// Use a clean PATH to ensure gh auth fails (expected), but verify no has-remote check
	t.Setenv("PATH", t.TempDir())
	result, err := Preflight("direct-PR", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range result.Checks {
		if c.Name == "has-remote" {
			t.Fatal("has-remote check should not run when repoPath is empty")
		}
	}
	if len(result.Checks) != 1 {
		t.Fatalf("expected 1 check without repoPath, got %d: %v", len(result.Checks), result.Checks)
	}
}
