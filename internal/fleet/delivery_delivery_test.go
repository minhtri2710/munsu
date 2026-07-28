package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

func TestHasRemote(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "")

	if hasRemote(tmp) {
		t.Error("expected no remotes for fresh repo")
	}

	// Add a remote
	cmd := exec.Command("git", "remote", "add", "origin", "https://github.com/test/test.git")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %s", out)
	}

	if !hasRemote(tmp) {
		t.Error("expected remote to be detected")
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

func TestMergeLocal_NoWorktree(t *testing.T) {
	err := MergeLocal(t.TempDir(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestNoMistakesStatus_NoBinary(t *testing.T) {
	// If no-mistakes isn't on PATH, this should return an error
	_, err := NoMistakesStatus("test-branch")
	if err != nil {
		// Expected — no-mistakes not available in test env
		if !strings.Contains(err.Error(), "no-mistakes") {
			t.Errorf("expected no-mistakes error, got: %v", err)
		}
	}
}

func TestNoMistakesRun_NoBinary(t *testing.T) {
	err := NoMistakesRun("test", nil)
	if err != nil {
		if !strings.Contains(err.Error(), "no-mistakes") {
			t.Errorf("expected no-mistakes error, got: %v", err)
		}
	}
}

func TestNoMistakesRespond_Empty(t *testing.T) {
	err := NoMistakesRespond(nil)
	if err == nil {
		t.Fatal("expected error for empty findings")
	}
}

func TestNoMistakesRespond_NoBinary(t *testing.T) {
	err := NoMistakesRespond([]string{"finding-1"})
	if err != nil {
		if !strings.Contains(err.Error(), "no-mistakes") {
			t.Errorf("expected no-mistakes error, got: %v", err)
		}
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
func gitEnvForDir(dir string) []string {
	return append(os.Environ(),
		"GIT_CEILING_DIRECTORIES="+dir,
	)
}
