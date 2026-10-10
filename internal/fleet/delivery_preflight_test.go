//go:build integration

package fleet

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestPreflight_UnknownMode(t *testing.T) {
	_, err := Preflight("bogus-mode", "", taskauthority.DeliveryStep{})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
	if !strings.Contains(err.Error(), "unknown delivery mode") {
		t.Errorf("expected 'unknown delivery mode', got: %v", err)
	}
}

func TestPreflight_LocalOnlyAlwaysOK(t *testing.T) {
	result, err := Preflight("local-only", "", taskauthority.DeliveryStep{})
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

func TestPreflight_DirectPR_ForgeReadiness(t *testing.T) {
	t.Run("forge not on PATH refuses in every forge mode", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		for _, mode := range []string{"direct-PR", "no-mistakes"} {
			_, err := Preflight(mode, "", githubForgeStep)
			if err == nil || !strings.Contains(err.Error(), "configured forge is not Ready") {
				t.Fatalf("%s: expected configured-forge refusal, got: %v", mode, err)
			}
		}
	})

	t.Run("gh not authenticated refuses", func(t *testing.T) {
		installFakeGH(t, ghReply{match: "auth status", stderr: "You are not logged into any GitHub hosts.", exit: 1})
		_, err := Preflight("direct-PR", "", githubForgeStep)
		if err == nil || !strings.Contains(err.Error(), "configured forge is not Ready") {
			t.Fatalf("expected configured-forge refusal without gh auth, got: %v", err)
		}
	})

	t.Run("forge ready is feasible", func(t *testing.T) {
		installFakeGH(t)
		result, err := Preflight("direct-PR", "", githubForgeStep)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Feasible {
			t.Errorf("direct-PR should be feasible with a Ready forge, checks: %v", result.Checks)
		}
		if len(result.Checks) != 1 || result.Checks[0].Name != "forge-tool" {
			t.Fatalf("expected one forge-tool check, got: %v", result.Checks)
		}
	})
}

func TestPreflight_DirectPR_HasRemote(t *testing.T) {
	installFakeGH(t)
	repo := t.TempDir()
	initGitRepo(t, repo, "")

	// Without remote, the has-remote check should fail when repoPath is provided
	result, err := Preflight("direct-PR", repo, githubForgeStep)
	if err != nil {
		t.Fatal(err)
	}

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

	result, err = Preflight("direct-PR", repo, githubForgeStep)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range result.Checks {
		if c.Name == "has-remote" && !c.OK {
			t.Errorf("has-remote should be true after adding remote, got: %s", c.Detail)
		}
	}

	t.Run("gitlab forge checks has-remote too", func(t *testing.T) {
		old := glabRunnerFor
		glabRunnerFor = fixedGlabRunner(readyRunner())
		t.Cleanup(func() { glabRunnerFor = old })
		noRemote := t.TempDir()
		initGitRepo(t, noRemote, "")
		result, err := Preflight("direct-PR", noRemote, gitlabForgeStep)
		if err != nil {
			t.Fatal(err)
		}
		if result.Feasible {
			t.Fatal("direct-PR with a gitlab forge and no remote should be infeasible")
		}
		var hasRemoteCheck *Check
		for i, c := range result.Checks {
			if c.Name == "has-remote" {
				hasRemoteCheck = &result.Checks[i]
				break
			}
		}
		if hasRemoteCheck == nil || hasRemoteCheck.OK {
			t.Fatalf("expected a failing has-remote check, got: %v", result.Checks)
		}
	})
}

func TestPreflight_DirectPR_SkipRemoteWhenNoRepoPath(t *testing.T) {
	installFakeGH(t)
	result, err := Preflight("direct-PR", "", githubForgeStep)
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

// TestPreflightDeliveryBlocksInfeasibleDirectPR pins the runner-level refusal:
// a direct-PR repo with no remote is infeasible, and the runner blocks it
// before worktree acquisition.
func TestPreflightDeliveryBlocksInfeasibleDirectPR(t *testing.T) {
	installFakeGH(t)
	repo := t.TempDir()
	initGitRepo(t, repo, "")
	r := &Runner{effectiveMode: "direct-PR", projPath: repo, forgeStep: githubForgeStep}
	if err := r.preflightDelivery(); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("preflightDelivery = %v, want a blocked refusal without a remote", err)
	}
}
