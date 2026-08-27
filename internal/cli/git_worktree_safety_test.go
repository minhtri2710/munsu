package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/spf13/cobra"
)

func TestSafetyCheckGitReadsRemainAvailableWithoutBinding(t *testing.T) {
	repo := initGitRepoForSafety(t, t.TempDir())
	// A worktree, not the primary checkout: inside a task run the primary
	// checkout is refused outright regardless of the command (see
	// TestSafetyCheckRefusesPrimaryCheckoutDuringTaskRun).
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, repo, "worktree", "add", "--detach", worktree)
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-read")

	for _, command := range []string{"git status --short", "git branch --show-current"} {
		block, reason := runPiSafetyForGit(t, worktree, command)
		if block || reason != "" {
			t.Fatalf("%q block=%v reason=%q", command, block, reason)
		}
	}
}

func TestSafetyCheckRejectsShellCompoundGitMutations(t *testing.T) {
	repo := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, repo, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-shell", repo, worktree)
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
	homeDir := bindSafetyWorktree(t, "ship-1", primary, worktree)
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

	// Force the Windows-literal reading on Darwin so the exact Windows-shaped
	// --git-dir reaches the binding comparison without requiring a Windows host.
	for _, path := range []string{`C:\Users\soldier\repo`, `\\server\share\repo`} {
		if got := resolveSafetyPathWithMode(worktree, path, backslashLiteral); got != path {
			t.Fatalf("resolveSafetyPathWithMode(%q) = %q, want unchanged Windows absolute path", path, got)
		}
	}
	const windowsGitDir = `C:\Users\soldier\.git\worktrees\wt`
	auth := testAuthorityFor(t, homeDir)
	agg, err := auth.Get(mustTaskIDFor(t, "ship-1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree == nil {
		t.Fatal("test task has no worktree binding")
	}
	windowsBinding := *agg.Worktree
	windowsBinding.GitDir = windowsGitDir
	for _, tc := range []struct {
		name    string
		command string
		gitDir  string
		blocked bool
	}{
		{name: "bound git-dir", command: `git --work-tree . --git-dir ` + windowsGitDir + ` add file.txt`, gitDir: windowsGitDir},
		{name: "wrong git-dir", command: `git --work-tree . --git-dir C:\Users\soldier\.git\worktrees\other add file.txt`, gitDir: `C:\Users\soldier\.git\worktrees\other`, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsedWindows, err := parseGitSafetyCommandWithMode(worktree, tc.command, backslashLiteral)
			if err != nil {
				t.Fatal(err)
			}
			if parsedWindows.gitDir != tc.gitDir {
				t.Fatalf("parsed Windows --git-dir = %q, want %q", parsedWindows.gitDir, tc.gitDir)
			}
			reason := validateCanonicalGitExplicitTargetBinding(parsedWindows.gitDir, "", &windowsBinding)
			if (reason != "") != tc.blocked {
				t.Fatalf("git-dir=%q reason=%q blocked=%v, want blocked=%v", tc.gitDir, reason, reason != "", tc.blocked)
			}
		})
	}
}

func TestSafetyCheckGitMutationRefusesPrimaryWrongRepoStaleGenerationAndHead(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	otherPrimary := initGitRepoForSafety(t, t.TempDir())
	otherWorktree := filepath.Join(t.TempDir(), "other-wt")
	runGitForSafety(t, otherPrimary, "worktree", "add", "--detach", otherWorktree)

	homeDir := bindSafetyWorktree(t, "ship-2", primary, worktree)
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
		// A target outside any repository classifies as unrelated, which the
		// worktree whitelist refuses just as firmly as a primary checkout.
		{"non-git target", t.TempDir(), "git checkout -b mu/ship-2", "not the bound worktree"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block, reason := runPiSafetyForGit(t, tc.path, tc.command)
			if !block || !strings.Contains(reason, tc.want) {
				t.Fatalf("block=%v reason=%q want %q", block, reason, tc.want)
			}
		})
	}

	auth := testAuthorityFor(t, homeDir)
	// Stale generation: complete then reopen creates generation 2 (current,
	// no binding) while generation 1 keeps the worktree binding, so the gate
	// refuses mutations on current-truth absence of a binding.
	agg1, err := auth.Get(mustTaskIDFor(t, "ship-2"))
	if err != nil {
		t.Fatal(err)
	}
	completeReq := taskauthority.CanonicalCompleteRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskIDFor(t, "ship-2"),
		Precondition: domain.Of(uint64(agg1.Generation), uint64(agg1.Revision)),
		To:           taskauthority.PhaseDone,
		Reason:       "safety test",
	}
	if _, err := auth.Complete(mustCanonicalOp(t, "safety-complete-ship-2", completeReq), completeReq); err != nil {
		t.Fatal(err)
	}
	reopenedAgg, err := auth.Get(mustTaskIDFor(t, "ship-2"))
	if err != nil {
		t.Fatal(err)
	}
	reopenReq := taskauthority.CanonicalReopenRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskIDFor(t, "ship-2"),
		Precondition: domain.Of(uint64(reopenedAgg.Generation), uint64(reopenedAgg.Revision)),
		Reason:       "safety test",
	}
	if _, err := auth.Reopen(mustCanonicalOp(t, "safety-reopen-ship-2", reopenReq), reopenReq); err != nil {
		t.Fatal(err)
	}
	block, reason := runPiSafetyForGit(t, worktree, "git add file.txt")
	if !block || !strings.Contains(reason, "requires active worktree binding") {
		t.Fatalf("stale generation block=%v reason=%q", block, reason)
	}

	// Bind the reopened generation so the binding checks are reached, and
	// move the worktree onto the task branch so the branch check does not
	// mask them.
	agg, err := auth.Get(mustTaskIDFor(t, "ship-2"))
	if err != nil {
		t.Fatal(err)
	}
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-2")
	reopened := safetyWorktreeBinding(t, primary, worktree, "reopened-lease", "reopened-fence")
	bindReq := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskIDFor(t, "ship-2"),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding:      reopened,
		Reason:       "safety test",
	}
	if _, err := auth.BindWorktree(mustCanonicalOp(t, "safety-rebind-ship-2", bindReq), bindReq); err != nil {
		t.Fatal(err)
	}
	if block, reason := runPiSafetyForGit(t, worktree, "git add file.txt"); block {
		t.Fatalf("bound reopened generation blocked: %s", reason)
	}

	// Move the recorded head off the actual HEAD: from a detached worktree,
	// checkout of the task branch must fail closed on the recorded base HEAD
	// mismatch (the canonical binding head check).
	setSafetyWorktreeHead(t, homeDir, "ship-2", strings.Repeat("f", 40))
	runGitForSafety(t, worktree, "checkout", "--detach")
	block, reason = runPiSafetyForGit(t, worktree, "git checkout -b mu/ship-2")
	if !block || !strings.Contains(reason, "unexpected head") {
		t.Fatalf("unexpected head block=%v reason=%q", block, reason)
	}
}

func TestSafetyCheckDefaultShipAuthorityGitAllowlist(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-3", primary, worktree)
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

// TestSafetyCheckForceDeniedWithoutAuthorization proves unrestricted force,
// force-with-lease, branch deletion, rewrites, and push --delete are all
// unconditionally denied: the git authorization layer (amendment/retirement
// context tiers, force-with-lease authorization) was removed with the legacy
// delivery path, so no context can authorize them.
func TestSafetyCheckForceDeniedWithoutAuthorization(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-force", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-force")

	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-force")

	for _, command := range []string{
		"git push --force origin HEAD:refs/heads/mu/ship-force",
		"git push -f origin HEAD:refs/heads/mu/ship-force",
		"git push --force-with-lease origin HEAD:refs/heads/mu/ship-force",
		"git push --delete origin mu/ship-force",
		"git branch -d mu/ship-force",
		"git rebase main",
		"git reset --hard HEAD~1",
		"git merge main",
		"git cherry-pick HEAD",
		"git revert HEAD",
	} {
		block, reason := runPiSafetyForGit(t, worktree, command)
		if !block || reason == "" {
			t.Fatalf("%q block=%v reason=%q, want deny", command, block, reason)
		}
	}
}

func bindSafetyWorktree(t *testing.T, taskID, primary, worktree string) string {
	t.Helper()
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	createReq := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      tid,
		Owner:       "general",
		Description: "test task",
		Kind:        "ship",
		Reason:      "safety test",
	}
	if pid, err := domain.NewProjectID("repo"); err == nil {
		createReq.Project = pid
	}
	if _, err := auth.Create(mustCanonicalOp(t, "safety-create-"+taskID, createReq), createReq); err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}

	binding := safetyWorktreeBinding(t, primary, worktree, "worktree-lease", "worktree-fence")
	bindReq := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       auth.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding:      binding,
		Reason:       "safety test",
	}
	if _, err := auth.BindWorktree(mustCanonicalOp(t, "safety-bind-"+taskID, bindReq), bindReq); err != nil {
		t.Fatal(err)
	}
	return homeDir
}

// safetyWorktreeBinding computes one canonical worktree binding for the
// safety gate fixtures from the primary repository and its worktree.
func safetyWorktreeBinding(t *testing.T, primary, worktree, leaseID, fenceToken string) taskauthority.WorktreeBinding {
	t.Helper()
	repoID := gitOutputForSafety(t, primary, "rev-parse", "--git-common-dir")
	gitDir := gitOutputForSafety(t, worktree, "rev-parse", "--git-dir")
	commonDir := gitOutputForSafety(t, worktree, "rev-parse", "--git-common-dir")
	head := gitOutputForSafety(t, worktree, "rev-parse", "HEAD")
	absWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	return taskauthority.WorktreeBinding{
		RepositoryIdentity: canonicalSafetyPath(t, resolveGitPathForSafety(primary, repoID)),
		Path:               absWorktree,
		GitDir:             canonicalSafetyPath(t, resolveGitPathForSafety(worktree, gitDir)),
		CommonDir:          canonicalSafetyPath(t, resolveGitPathForSafety(worktree, commonDir)),
		Head:               head,
		LeaseID:            leaseID,
		FenceToken:         fenceToken,
		BoundAtUnix:        time.Now().Unix(),
	}
}

// setSafetyWorktreeHead rewrites the current canonical aggregate's worktree
// binding head by editing the committed task document directly, mirroring how
// the pre-canonical fixture rewrote the aggregate document. The canonical
// read path observes the tampered head on the next Get.
func setSafetyWorktreeHead(t *testing.T, homeDir, taskID, head string) {
	t.Helper()
	currentPath := filepath.Join(homeDir, "state", "task-authority", "tasks", taskID, "current.json")
	data, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		HomeRevision uint64                  `json:"home_revision"`
		Aggregate    taskauthority.Aggregate `json:"aggregate"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Aggregate.Worktree == nil {
		t.Fatalf("task %s has no worktree binding", taskID)
	}
	w := *doc.Aggregate.Worktree
	w.Head = head
	doc.Aggregate.Worktree = &w
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, out, 0600); err != nil {
		t.Fatal(err)
	}
}

func runPiSafetyForGit(t *testing.T, checkPath, command string) (bool, string) {
	t.Helper()
	cmd := &cobra.Command{Use: "safety-check"}
	configureContractCommand(cmd)
	cmd.SetErr(io.Discard)
	stdout, _ := captureBoth(func() {
		if err := runSafetyCheck(cmd, checkPath, command, "", ""); err != nil {
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

// TestSafetyCheckGitWindowsBackslashReadingIsolatedFromPosix pins the two
// required behaviors from the guard fix:
//
//  1. On Windows-shaped input (backslashLiteral reading) a valid Windows path in
//     --git-dir must read literally, match the bound worktree, and be allowed
//     (the false-positive refusal the fix removes).
//  2. On POSIX (backslashEscapes reading) the guard must keep treating a lone
//     backslash as a shell escape: the same Windows path is mangled and still
//     fails the binding comparison (no weakening of POSIX refusals).
func TestSafetyCheckGitWindowsBackslashReadingIsolatedFromPosix(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-win", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-win")

	// A Windows-machine worktree binding whose --git-dir uses backslashes.
	const windowsGitDir = `C:\Users\soldier\.git\worktrees\wt`
	auth := testAuthorityFor(t, homeDir)
	agg, err := auth.Get(mustTaskIDFor(t, "ship-win"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree == nil {
		t.Fatal("test task has no worktree binding")
	}
	windowsBinding := *agg.Worktree
	windowsBinding.GitDir = windowsGitDir

	const command = `git --work-tree . --git-dir ` + windowsGitDir + ` add file.txt`

	// (1) Windows reading: backslashes literal -> bound path matches -> allowed.
	winParsed, err := parseGitSafetyCommandWithMode(worktree, command, backslashLiteral)
	if err != nil {
		t.Fatal(err)
	}
	if winParsed.gitDir != windowsGitDir {
		t.Fatalf("Windows-literal --git-dir = %q, want %q", winParsed.gitDir, windowsGitDir)
	}
	if reason := validateCanonicalGitExplicitTargetBinding(winParsed.gitDir, "", &windowsBinding); reason != "" {
		t.Fatalf("bound Windows --git-dir refused under Windows reading: %s", reason)
	}

	// (2) POSIX reading: backslash is an escape -> Windows path mangled -> still
	// refused (guard keeps refusing on POSIX).
	posixParsed, err := parseGitSafetyCommandWithMode(worktree, command, backslashEscapes)
	if err != nil {
		t.Fatal(err)
	}
	if posixParsed.gitDir == windowsGitDir {
		t.Fatalf("POSIX reading kept backslashes literal: %q; POSIX must escape them", posixParsed.gitDir)
	}
	if reason := validateCanonicalGitExplicitTargetBinding(posixParsed.gitDir, "", &windowsBinding); reason == "" {
		t.Fatalf("mangled Windows --git-dir %q unexpectedly matched binding under POSIX reading", posixParsed.gitDir)
	}

	// resolveSafetyPathWithMode must only short-circuit to the absolute Windows
	// path under the Windows (literal) reading; under POSIX it must join to the
	// base (treat the backslash path as relative), not weaken POSIX refusals.
	if got := resolveSafetyPathWithMode(worktree, windowsGitDir, backslashLiteral); got != windowsGitDir {
		t.Fatalf("Windows reading resolveSafetyPathWithMode(%q) = %q, want unchanged Windows absolute path", windowsGitDir, got)
	}
	posixResolved := resolveSafetyPathWithMode(worktree, windowsGitDir, backslashEscapes)
	if posixResolved == windowsGitDir {
		t.Fatalf("POSIX reading treat Windows path %q as absolute; must join under base", windowsGitDir)
	}
	if !strings.HasPrefix(posixResolved, worktree) {
		t.Fatalf("POSIX resolved path %q not joined under base %q", posixResolved, worktree)
	}
}
