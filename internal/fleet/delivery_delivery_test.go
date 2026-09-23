//go:build integration

package fleet

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestStrconvParseInt(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"42", 42},
		{"0", 0},
		{"100", 100},
		{"abc", 0},
		{"42extra", 42},
		{"", 0},
	}

	for _, tt := range tests {
		got, _ := strconvParseInt(tt.input)
		if got != tt.want {
			t.Errorf("strconvParseInt(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestMinInt(t *testing.T) {
	if got := minInt(5, 10); got != 5 {
		t.Errorf("minInt(5, 10) = %d, want 5", got)
	}
	if got := minInt(10, 5); got != 5 {
		t.Errorf("minInt(10, 5) = %d, want 5", got)
	}
	if got := minInt(5, 5); got != 5 {
		t.Errorf("minInt(5, 5) = %d, want 5", got)
	}
}

func TestGitBranch(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "")

	branch, err := gitBranch(tmp)
	if err != nil {
		t.Fatalf("gitBranch: %v", err)
	}
	if branch == "" {
		t.Fatal("expected non-empty branch name")
	}
}

func TestGitDefaultBranch(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "")

	branch, err := gitDefaultBranch(tmp)
	if err != nil {
		t.Fatalf("gitDefaultBranch: %v", err)
	}
	if branch != "main" && branch != "master" {
		t.Errorf("expected main or master, got %q", branch)
	}
}

func TestGitDefaultBranch_NoRemote(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "")

	branch, err := gitDefaultBranch(tmp)
	if err != nil {
		t.Fatalf("gitDefaultBranch without remote: %v", err)
	}
	if branch == "" {
		t.Fatal("expected a branch name even without remote")
	}
}

func TestCheckDefaultBranchStale_NoRemote(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "")

	warn, err := checkDefaultBranchStale(tmp, "main")
	if err != nil {
		t.Fatalf("checkDefaultBranchStale: %v", err)
	}
	if warn != "" {
		t.Errorf("expected no warning without remote, got: %s", warn)
	}
}

func TestGitDiffSummary_NoDiff(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "")

	branch, err := gitBranch(tmp)
	if err != nil {
		t.Fatalf("gitBranch: %v", err)
	}

	summary, err := gitDiffSummary(tmp, branch, branch)
	if err != nil {
		t.Fatalf("gitDiffSummary: %v", err)
	}
	if !strings.Contains(summary, "No differences") {
		t.Errorf("expected 'No differences' in summary for same branch, got: %s", summary)
	}
}

func TestGitDiffSummary_WithDiff(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "")

	gitEnv := gitEnvForDir(tmp)

	// Create a branch and add a commit
	cmd := exec.Command("git", "checkout", "-b", "feature/test-branch")
	cmd.Dir = tmp
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %s", out)
	}

	os.WriteFile(filepath.Join(tmp, "test.txt"), []byte("hello world\n"), 0644)
	cmd = exec.Command("git", "add", "test.txt")
	cmd.Dir = tmp
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "add test.txt")
	cmd.Dir = tmp
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}

	// Get the default branch name
	defaultBranch, err := gitDefaultBranch(tmp)
	if err != nil {
		t.Fatalf("gitDefaultBranch: %v", err)
	}

	summary, err := gitDiffSummary(tmp, defaultBranch, "feature/test-branch")
	if err != nil {
		t.Fatalf("gitDiffSummary: %v", err)
	}
	if !strings.Contains(summary, "test.txt") {
		t.Errorf("expected summary to mention test.txt, got: %s", summary)
	}
	if !strings.Contains(summary, "Insertions") {
		t.Errorf("expected summary to mention Insertions, got: %s", summary)
	}
}

// TestGitDiffSummary_MergeBaseFailureSurfaces proves a base with no merge
// base against the branch fails instead of silently diffing the raw base.
func TestGitDiffSummary_MergeBaseFailureSurfaces(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "")
	branch, err := gitBranch(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitDiffSummary(tmp, "refs/does/not/exist", branch); err == nil || !strings.Contains(err.Error(), "git merge-base") {
		t.Fatalf("gitDiffSummary err = %v, want merge-base failure", err)
	}
}

// TestReviewDiffUsesStoredIdentityBaseOnOrigin proves review-diff diffs
// against origin/<BaseRef> from the stored delivery identity, not an
// unfetched provider merge ref.
func TestReviewDiffUsesStoredIdentityBaseOnOrigin(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo, "")
	remote := filepath.Join(t.TempDir(), "remote.git")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = gitEnvForDir(repo)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "--bare", remote)
	run("checkout", "-B", "trunk")
	run("remote", "add", "origin", remote)
	run("push", "origin", "trunk")
	run("fetch", "origin")
	run("checkout", "-b", "feature/review")
	if err := os.WriteFile(filepath.Join(repo, "change.txt"), []byte("change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "change.txt")
	run("commit", "-m", "change")

	_, homeDir := newFleetCanonical(t)
	if err := home.WriteMeta(homeDir, "t1", map[string]string{
		"project": "project", "worktree": repo,
		"pr_provider": "github", "pr_owner": "minhtri2710", "pr_repo": "munsu",
		"pr_number": "42", "pr_url": "https://github.com/minhtri2710/munsu/pull/42",
		"pr_base_ref": "trunk", "pr_head_ref": "feature/review", "pr_head_sha": deliveryTestHead,
		"pr_timestamp": "2026-08-05T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	diffErr := ReviewDiff(homeDir, "t1")
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	if diffErr != nil {
		t.Fatalf("ReviewDiff: %v", diffErr)
	}
	if !strings.Contains(string(out), "- **Base:** `origin/trunk`") || !strings.Contains(string(out), "change.txt") {
		t.Fatalf("review-diff output = %s, want a diff of change.txt against origin/trunk", out)
	}
}

// initGitRepo initializes a git repo in the given directory.
func initGitRepo(t *testing.T, dir, remoteDir string) {
	t.Helper()

	gitEnv := gitEnvForDir(dir)

	// Init
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}

	// Configure
	for _, cfg := range []string{"user.email test@test.com", "user.name Test"} {
		parts := strings.Split(cfg, " ")
		c := exec.Command("git", append([]string{"config"}, parts...)...)
		c.Dir = dir
		c.Env = gitEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %s", cfg, out)
		}
	}

	// Initial commit
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# test"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = dir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = dir
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}

	// Set up remote if remoteDir provided
	if remoteDir != "" {
		cmd = exec.Command("git", "init", "--bare", remoteDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init --bare: %s", out)
		}

		// Detect the default branch name
		branchOut, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
		if err != nil {
			t.Fatalf("detecting branch: %v", err)
		}
		defaultBranch := strings.TrimSpace(string(branchOut))

		cmd = exec.Command("git", "remote", "add", "origin", remoteDir)
		cmd.Dir = dir
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git remote add: %s", out)
		}
		cmd = exec.Command("git", "push", "-u", "origin", defaultBranch)
		cmd.Dir = dir
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git push: %s", out)
		}
	}
}

// gitEnvForDir returns the current environment with GIT_CEILING_DIRECTORIES set
// to prevent git from looking at parent directories.
