package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

func TestSafetyCheckGitReadsRemainAvailableWithoutBinding(t *testing.T) {
	repo := initGitRepoForSafety(t, t.TempDir())
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-read")

	for _, command := range []string{"git status --short", "git branch --show-current"} {
		block, reason := runPiSafetyForGit(t, repo, command)
		if block || reason != "" {
			t.Fatalf("%q block=%v reason=%q", command, block, reason)
		}
	}
}

func TestSafetyCheckRejectsShellCompoundGitMutations(t *testing.T) {
	repo := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, repo, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-shell", "direct-PR", repo, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-shell")

	for _, command := range []string{
		"git status; git add file.txt",
		"git status && git commit -m test",
		"git status || git add file.txt",
		"git status | git add file.txt",
		"git $(printf add) file.txt",
		"git `printf add` file.txt",
		"git status\ngit add file.txt",
	} {
		block, reason := runPiSafetyForGit(t, repo, command)
		if !block || reason == "" {
			t.Fatalf("%q block=%v reason=%q, want deny", command, block, reason)
		}
	}
}

func TestSafetyCheckGitMutationRequiresExactWorktreeBindingAndAllowsAlternateTargetForms(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-1", "direct-PR", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-1")

	branch, reason := runPiSafetyForGit(t, worktree, "git checkout -b mu/ship-1")
	if branch || reason != "" {
		t.Fatalf("branch creation blocked=%v reason=%q", branch, reason)
	}
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-1")

	allowed := []string{
		"git --work-tree . --git-dir " + shellPathForSafety(t, gitDirPathForSafety(t, worktree)) + " add file.txt",
		"git -C . add file.txt",
		"git commit -m work",
		"git push origin HEAD:refs/heads/mu/ship-1",
	}
	for _, command := range allowed {
		block, reason := runPiSafetyForGit(t, worktree, command)
		if block {
			t.Fatalf("%q blocked: %s", command, reason)
		}
	}
}

func TestSafetyCheckGitMutationRefusesPrimaryWrongRepoStaleGenerationLeaseAndHead(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	otherPrimary := initGitRepoForSafety(t, t.TempDir())
	otherWorktree := filepath.Join(t.TempDir(), "other-wt")
	runGitForSafety(t, otherPrimary, "worktree", "add", "--detach", otherWorktree)

	homeDir := bindSafetyWorktree(t, "ship-2", "direct-PR", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-2")

	cases := []struct {
		name    string
		path    string
		command string
		want    string
	}{
		{"primary", primary, "git checkout -b mu/ship-2", "primary checkout"},
		{"wrong repo", otherWorktree, "git checkout -b mu/ship-2", "wrong repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block, reason := runPiSafetyForGit(t, tc.path, tc.command)
			if !block || !strings.Contains(reason, tc.want) {
				t.Fatalf("block=%v reason=%q want %q", block, reason, tc.want)
			}
		})
	}

	reloaded, _, err := home.ReadCurrentTaskAggregate(homeDir, "ship-2")
	if err != nil {
		t.Fatal(err)
	}
	originalLease := reloaded.Worktree.LeaseID
	stale := *reloaded
	newCurrent := *reloaded
	newCurrent.Generation = "2"
	newCurrent.Endpoint = nil
	newCurrent.Worktree = nil
	newCurrent.State = "queued"
	if err := home.WriteTaskAggregate(homeDir, newCurrent); err != nil {
		t.Fatal(err)
	}
	block, reason := runPiSafetyForGit(t, worktree, "git add file.txt")
	if !block || !strings.Contains(reason, "stale generation") {
		t.Fatalf("stale generation block=%v reason=%q", block, reason)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", ".task-authority", "aggregates", "ship-2", "current"), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}

	stale.Worktree.LeaseID = "recycled"
	if err := home.WriteTaskAggregate(homeDir, stale); err != nil {
		t.Fatal(err)
	}
	block, reason = runPiSafetyForGit(t, worktree, "git add file.txt")
	if !block || !strings.Contains(reason, "recycled lease") {
		t.Fatalf("recycled lease block=%v reason=%q", block, reason)
	}
	stale.Worktree.LeaseID = originalLease
	if err := home.WriteTaskAggregate(homeDir, stale); err != nil {
		t.Fatal(err)
	}
	staleReloaded, _, err := home.ReadCurrentTaskAggregate(homeDir, "ship-2")
	if err != nil {
		t.Fatal(err)
	}
	stale = *staleReloaded
	stale.Worktree.Head = strings.Repeat("f", 40)
	if err := home.WriteTaskAggregate(homeDir, stale); err != nil {
		t.Fatal(err)
	}
	block, reason = runPiSafetyForGit(t, worktree, "git checkout -b mu/ship-2")
	if !block || !strings.Contains(reason, "unexpected head") {
		t.Fatalf("unexpected head block=%v reason=%q", block, reason)
	}
}

func TestSafetyCheckDefaultShipAuthorityGitAllowlist(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-3", "direct-PR", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-3")

	denied := []string{
		"git checkout main",
		"git checkout -b not-task-local",
		"git merge main",
		"git rebase main",
		"git reset --hard HEAD~1",
		"git push origin main",
		"git push --force origin HEAD:refs/heads/mu/ship-3",
	}
	for _, command := range denied {
		block, reason := runPiSafetyForGit(t, worktree, command)
		if !block || reason == "" {
			t.Fatalf("%q block=%v reason=%q, want deny", command, block, reason)
		}
	}
}

func bindSafetyWorktree(t *testing.T, taskID, mode, primary, worktree string) string {
	t.Helper()
	homeDir := t.TempDir()
	agg, err := home.CreateTaskAggregate(homeDir, taskID, "general", "test task", "ship", "repo")
	if err != nil {
		t.Fatal(err)
	}
	agg.State = "working"
	agg.Endpoint = &home.TaskEndpointBinding{TaskGeneration: agg.Generation, Backend: "tmux", Handle: "pane", LeaseID: "endpoint-lease", FenceToken: "endpoint-fence", BoundAtUnix: time.Now().Unix()}
	if mode != "" {
		agg.StateDetail = mode
	}

	repoID := gitOutputForSafety(t, primary, "rev-parse", "--git-common-dir")
	gitDir := gitOutputForSafety(t, worktree, "rev-parse", "--git-dir")
	commonDir := gitOutputForSafety(t, worktree, "rev-parse", "--git-common-dir")
	head := gitOutputForSafety(t, worktree, "rev-parse", "HEAD")
	absWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	binding := home.TaskWorktreeBinding{
		RepositoryIdentity: canonicalSafetyPath(t, resolveGitPathForSafety(primary, repoID)),
		Path:               absWorktree,
		GitDir:             canonicalSafetyPath(t, resolveGitPathForSafety(worktree, gitDir)),
		CommonDir:          canonicalSafetyPath(t, resolveGitPathForSafety(worktree, commonDir)),
		Head:               head,
		LeaseID:            "worktree-lease",
		FenceToken:         "worktree-fence",
		BoundAtUnix:        time.Now().Unix(),
	}
	if err := home.BindTaskWorktree(homeDir, agg.TaskID, agg.Generation, binding); err != nil {
		t.Fatal(err)
	}
	agg, _, err = home.ReadCurrentTaskAggregate(homeDir, agg.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	agg.State = "working"
	agg.Endpoint = &home.TaskEndpointBinding{TaskGeneration: agg.Generation, Backend: "tmux", Handle: "pane", LeaseID: "endpoint-lease", FenceToken: "endpoint-fence", BoundAtUnix: time.Now().Unix()}
	if mode != "" {
		agg.StateDetail = mode
	}
	if err := home.WriteTaskAggregate(homeDir, *agg); err != nil {
		t.Fatal(err)
	}
	return homeDir
}

func runPiSafetyForGit(t *testing.T, checkPath, command string) (bool, string) {
	t.Helper()
	cmd := &cobra.Command{Use: "safety-check"}
	configureContractCommand(cmd)
	cmd.SetErr(io.Discard)
	stdout, _ := captureBoth(func() {
		if err := runSafetyCheck(cmd, checkPath, command, ""); err != nil {
			t.Fatalf("runSafetyCheck: %v", err)
		}
	})
	return parseSafetyBlock(t, stdout)
}

func parseSafetyBlock(t *testing.T, stdout string) (bool, string) {
	t.Helper()
	idx := strings.Index(stdout, "block: ")
	if idx < 0 {
		t.Fatalf("safety output missing block field:\n%s", stdout)
	}
	line := stdout[idx:]
	block := strings.HasPrefix(line, "block: true")
	reason := ""
	if rIdx := strings.Index(stdout, "reason: "); rIdx >= 0 {
		reasonLine := stdout[rIdx+len("reason: "):]
		if end := strings.IndexByte(reasonLine, '\n'); end >= 0 {
			reasonLine = reasonLine[:end]
		}
		reason = strings.TrimSpace(reasonLine)
	}
	return block, reason
}

func initGitRepoForSafety(t *testing.T, dir string) string {
	t.Helper()
	runGitForSafety(t, dir, "init", "-b", "main")
	runGitForSafety(t, dir, "config", "user.email", "test@example.com")
	runGitForSafety(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitForSafety(t, dir, "add", "README.md")
	runGitForSafety(t, dir, "commit", "-m", "initial")
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func runGitForSafety(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test User", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test User", "GIT_COMMITTER_EMAIL=test@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func gitOutputForSafety(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(string(out))
}

func canonicalSafetyPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(resolved)
}

func shellPathForSafety(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(abs, "'", "'\\''")
}

func gitDirPathForSafety(t *testing.T, worktree string) string {
	t.Helper()
	raw := gitOutputForSafety(t, worktree, "rev-parse", "--git-dir")
	return resolveGitPathForSafety(worktree, raw)
}

func resolveGitPathForSafety(base, raw string) string {
	if filepath.IsAbs(raw) {
		return raw
	}
	return filepath.Join(base, raw)
}
