//go:build integration

package fleet

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
)

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

// TestCheckDefaultBranchStale also plants FETCH_HEAD as a link to a sentinel:
// the unfenced fetch runs in a soldier's worktree, whose git dir the seat writes.
func TestCheckDefaultBranchStale(t *testing.T) {
	tests := []struct {
		name     string
		arrange  func(t *testing.T, tmp string) string // returns the worktree to check
		wantWarn string
	}{
		{"no remote", func(t *testing.T, tmp string) string {
			initGitRepo(t, tmp, "")
			return tmp
		}, ""},
		{"linked worktree behind origin", func(t *testing.T, tmp string) string {
			primary, remote, other := filepath.Join(tmp, "primary"), filepath.Join(tmp, "remote.git"), filepath.Join(tmp, "other")
			if err := os.MkdirAll(primary, 0o755); err != nil {
				t.Fatal(err)
			}
			setupGitRepo(t, primary, remote)
			gitTestRun(t, tmp, "clone", "-q", remote, other)
			gitTestRun(t, other, "-c", "user.email=t@t", "-c", "user.name=T", "commit", "-q", "--allow-empty", "-m", "ahead")
			gitTestRun(t, other, "push", "-q", "origin", "main")
			wt := filepath.Join(tmp, "wt")
			gitTestRun(t, primary, "worktree", "add", "-q", "-b", "feature/x", wt)
			return wt
		}, `default branch "main" is 1 commits behind origin/main`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			wt := tt.arrange(t, tmp)
			sentinel := filepath.Join(tmp, "sentinel")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			fetchHead := filepath.Join(gitTestRun(t, wt, "rev-parse", "--absolute-git-dir"), "FETCH_HEAD")
			if err := os.Symlink(sentinel, fetchHead); err != nil {
				t.Fatal(err)
			}

			warn, err := checkDefaultBranchStale(wt, "main")
			if err != nil {
				t.Fatalf("checkDefaultBranchStale: %v", err)
			}
			if !strings.Contains(warn, tt.wantWarn) || (tt.wantWarn == "") != (warn == "") {
				t.Errorf("warning = %q, want %q", warn, tt.wantWarn)
			}
			if b, err := os.ReadFile(sentinel); err != nil || string(b) != "keep" {
				t.Errorf("sentinel = %q, %v; the fetch wrote through FETCH_HEAD", b, err)
			}
			if target, err := os.Readlink(fetchHead); err != nil || target != sentinel {
				t.Errorf("FETCH_HEAD = %q, %v; want the planted link untouched", target, err)
			}
		})
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
	defaultBranch, err := backend.DefaultBranch(tmp)
	if err != nil {
		t.Fatalf("gitDefaultBranch: %v", err)
	}

	summary, err := gitDiffSummary(tmp, defaultBranch.Ref, "feature/test-branch")
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

// TestGitDiffSummary_BinaryNumstatCountsZero pins that a binary file, which
// git numstat reports as "-\t-", counts as a changed file with zero
// insertions and deletions.
func TestGitDiffSummary_BinaryNumstatCountsZero(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp, "")
	gitEnv := gitEnvForDir(tmp)
	defaultBranch, err := backend.DefaultBranch(tmp)
	if err != nil {
		t.Fatalf("gitDefaultBranch: %v", err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = tmp
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("checkout", "-b", "feature/binary")
	if err := os.WriteFile(filepath.Join(tmp, "blob.bin"), []byte{0, 1, 2, 0, 3}, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "text.txt"), []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "blob.bin", "text.txt")
	run("commit", "-m", "binary and text")

	summary, err := gitDiffSummary(tmp, defaultBranch.Ref, "feature/binary")
	if err != nil {
		t.Fatalf("gitDiffSummary: %v", err)
	}
	for _, want := range []string{"| Files changed | 2 |", "| Insertions | 1 |", "| Deletions | 0 |"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q:\n%s", want, summary)
		}
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

// TestReviewDiffDefaultBase runs ReviewDiff end to end, with no stored
// identity and origin/HEAD unset, over the states that decide which ref the
// diff is taken against: the local default branch when there is one, else
// origin/<default> (never a bare name no local branch carries). A malformed
// origin/HEAD is refused.
func TestReviewDiffDefaultBase(t *testing.T) {
	tests := []struct {
		name    string
		local   string // initial local branch
		origin  string // branch pushed to origin
		feature bool   // branch off local into feature/x; else local is the feature
		head    string // planted origin/HEAD target, if any
		want    string // Base shown, or the refusal when wantErr
		wantErr bool
	}{
		{name: "local master with origin/main diffs against master", local: "master", origin: "main", feature: true, want: "- **Base:** `master`"},
		{name: "default known only as origin/main diffs against origin/main", local: "feature/x", origin: "main", want: "- **Base:** `origin/main`"},
		{name: "malformed origin/HEAD is refused", local: "main", origin: "main", feature: true, head: "refs/remotes/upstream/main", want: "cannot detect default branch", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
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
			run("init", "-b", tt.local)
			run("config", "user.email", "test@test.com")
			run("config", "user.name", "Test")
			if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# test"), 0644); err != nil {
				t.Fatal(err)
			}
			run("add", "README.md")
			run("commit", "-m", "initial")
			run("init", "--bare", remote)
			run("remote", "add", "origin", remote)
			run("push", "origin", "HEAD:refs/heads/"+tt.origin)
			if tt.head != "" {
				run("symbolic-ref", "refs/remotes/origin/HEAD", tt.head)
			}
			if tt.feature {
				run("checkout", "-b", "feature/x")
			}
			if err := os.WriteFile(filepath.Join(repo, "change.txt"), []byte("change\n"), 0644); err != nil {
				t.Fatal(err)
			}
			run("add", "change.txt")
			run("commit", "-m", "change")

			auth, homeDir := newFleetCanonical(t)
			mustFleetCreate(t, auth, "t1")
			seedWorktreeEvidence(t, auth, "t1", repo, "lease-wt", "fence-wt")
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
			if tt.wantErr {
				if diffErr == nil || !strings.Contains(diffErr.Error(), tt.want) {
					t.Fatalf("ReviewDiff = %v, want refusal %q", diffErr, tt.want)
				}
				return
			}
			if diffErr != nil {
				t.Fatalf("ReviewDiff: %v", diffErr)
			}
			if !strings.Contains(string(out), tt.want) || !strings.Contains(string(out), "change.txt") {
				t.Fatalf("review-diff output = %s, want %q and change.txt", out, tt.want)
			}
		})
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
func gitEnvForDir(dir string) []string { return append(os.Environ(), "GIT_CEILING_DIRECTORIES="+dir) }
