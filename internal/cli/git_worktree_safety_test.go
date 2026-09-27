package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
			parsedWindows := parseGitSafetyCommandForTest(worktree, tc.command, backslashLiteral)
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

// TestSafetyCheckArgvLevelFenceEvasionDenied pins the ADR-0005 §6 hardening:
// force/delete/rewrite must be denied regardless of flag position, bundled
// short flags, long spellings, and a global-option value that would otherwise
// shadow the verb. Each command exercises a distinct newly-closed evasion.
func TestSafetyCheckArgvLevelFenceEvasionDenied(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-evade", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-evade")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-evade")

	for _, command := range []string{
		// push: force/delete after the refspec, bundled short, +refspec, and a
		// -c value that would otherwise shadow the push verb.
		"git push origin HEAD:refs/heads/mu/ship-evade --force",
		"git push origin -fq HEAD:refs/heads/mu/ship-evade",
		"git push origin --delete mu/ship-evade",
		"git push origin +mu/ship-evade",
		"git push origin main",
		"git push notorigin mu/ship-evade",
		"git push origin mu/ship-evade mu/ship-evade",
		"git push origin",
		"git -c user.email=x push origin HEAD:refs/heads/mu/ship-evade --force",
		// branch: delete/move/copy in a trailing position and in long form.
		"git branch mu/ship-evade -D",
		"git branch --delete mu/ship-evade",
		"git branch -m mu/ship-evade renamed",
		"git branch --move mu/ship-evade renamed",
		"git branch --copy mu/ship-evade copied",
		"git branch -f mu/ship-evade",
		"git branch --force mu/ship-evade",
		"git branch -df mu/ship-evade",
		"git branch other-branch",
		"git branch other-branch mu/ship-evade",
		"git branch --unset-upstream",
		"git branch --set-upstream-to=origin/main",
	} {
		block, reason := runPiSafetyForGit(t, worktree, command)
		if !block || reason == "" {
			t.Fatalf("%q block=%v reason=%q, want deny", command, block, reason)
		}
	}
}

// TestSafetyCheckNormalPushAndBranchFormsAllowed proves the argv-level fence
// hardening did not regress the permitted normal push and task-local branch
// forms, including the allowed flags in either position.
func TestSafetyCheckNormalPushAndBranchFormsAllowed(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-ok", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-ok")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-ok")

	for _, command := range []string{
		"git push origin mu/ship-ok",
		"git push origin HEAD:refs/heads/mu/ship-ok",
		"git push -q origin mu/ship-ok",
		"git push origin mu/ship-ok -v",
		"git branch mu/ship-ok",
	} {
		block, reason := runPiSafetyForGit(t, worktree, command)
		if block {
			t.Fatalf("%q blocked: %s", command, reason)
		}
	}
}

// TestSafetyCheckReadsSubshellsAndBuiltinCd pins the hook refusing git and
// munsu commands inside an unquoted `( ... )` subshell, and a git command after
// a cd behind `builtin`, `command` or an assignment, which runs in the
// directory the cd names. A subshell's cd does not move the words after its
// `)`.
func TestSafetyCheckReadsSubshellsAndBuiltinCd(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-sub", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-sub")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-sub")

	for _, command := range []string{
		"(git push --force)",
		"(git push --force origin mu/ship-sub)",
		"(cd " + primary + " && git add f)",
		"(cd " + primary + "; git add f); git add g",
		"builtin cd " + primary + " && git add f",
		"command cd " + primary + " && git add f",
		"X=1 cd " + primary + " && git add f",
		"(munsu watch)",
		"(ls .no-mistakes)",
	} {
		if block, _ := runPiSafetyForGit(t, worktree, command); !block {
			t.Errorf("%q allowed, want refused", command)
		}
	}
	for _, command := range []string{
		"(cd " + primary + "); git add f",
		"(cd " + primary + " && git status); git push origin mu/ship-sub",
		"command -v cd " + primary + " && git add f",
	} {
		if block, reason := runPiSafetyForGit(t, worktree, command); block {
			t.Errorf("%q refused: %s", command, reason)
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
	winParsed := parseGitSafetyCommandForTest(worktree, command, backslashLiteral)
	if winParsed.gitDir != windowsGitDir {
		t.Fatalf("Windows-literal --git-dir = %q, want %q", winParsed.gitDir, windowsGitDir)
	}
	if reason := validateCanonicalGitExplicitTargetBinding(winParsed.gitDir, "", &windowsBinding); reason != "" {
		t.Fatalf("bound Windows --git-dir refused under Windows reading: %s", reason)
	}

	// (2) POSIX reading: backslash is an escape -> Windows path mangled -> still
	// refused (guard keeps refusing on POSIX).
	posixParsed := parseGitSafetyCommandForTest(worktree, command, backslashEscapes)
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

// TestResolveSafetyPathModeDetermined pins resolveSafetyPathWithMode's
// absoluteness decision to the backslash mode, not to the host's
// filepath.IsAbs, which is the core of #686. It exercises the function
// directly on the running host and confirms the two production readings are
// internally consistent: under backslashLiteral a Windows-shaped --git-dir is
// returned absolute (unchanged), and under backslashEscapes it is joined under
// the base (treated as relative). The Windows-host cell of this property is
// covered by TestResolveSafetyPathWindowsHostModeIsolation, which fails before
// the fix and passes after it on a Windows runner.
func TestResolveSafetyPathModeDetermined(t *testing.T) {
	const base = "/repo/worktree"
	type cell struct {
		name string
		path string
		mode backslashMode
		want string
	}
	cells := []cell{
		{"windows-drive-literal", `C:\Users\soldier\.git\worktrees\wt`, backslashLiteral, `C:\Users\soldier\.git\worktrees\wt`},
		{"windows-unc-literal", `\\server\share\.git\worktrees\wt`, backslashLiteral, `\\server\share\.git\worktrees\wt`},
		{"windows-drive-escape", `C:\Users\soldier\.git\worktrees\wt`, backslashEscapes, filepath.Join(base, `C:\Users\soldier\.git\worktrees\wt`)},
		{"windows-unc-escape", `\\server\share\.git\worktrees\wt`, backslashEscapes, filepath.Join(base, `\\server\share\.git\worktrees\wt`)},
		{"relative-escape", "rel/path", backslashEscapes, filepath.Join(base, "rel/path")},
		{"relative-literal", "rel/path", backslashLiteral, filepath.Join(base, "rel/path")},
	}
	for _, c := range cells {
		got := resolveSafetyPathWithMode(base, c.path, c.mode)
		if got != c.want {
			t.Fatalf("resolveSafetyPathWithMode(%q, %q, %v) = %q, want %q", base, c.path, c.mode, got, c.want)
		}
	}
}

// parseGitSafetyCommandForTest parses the first mutating git command in command
// under one backslash reading, the way evaluateGitMutationSafety reads each
// segment.
func parseGitSafetyCommandForTest(checkPath, command string, mode backslashMode) gitCommandSafety {
	for _, segment := range tokenizeSegments(mode, command) {
		if commands := segmentGitCommands(checkPath, segment, mode); len(commands) > 0 {
			return commands[0].g
		}
	}
	return gitCommandSafety{}
}

// TestSafetyCheckGitVerdictsOnSharedTokenizer pins the hook verdict for every
// command whose reading moved when the git guard switched from its own
// dequote-then-split reader to tokenizeSegments and began reading every
// string payload as shell. old is the verdict at 8765440e; want is the verdict
// now. The rows that did not move pin the substitution refusal, which still
// scans the raw command, heredoc bodies included, and the payload reading.
func TestSafetyCheckGitVerdictsOnSharedTokenizer(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-vd", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-vd")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-vd")
	if err := os.MkdirAll(filepath.Join(worktree, "my dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(primary, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	const refuse, allow = true, false
	rows := []struct {
		command string
		old     bool
		want    bool
	}{
		// A quoted path is one word: the old reader split it, read "dir" or
		// "repo" as the verb, and let the push through.
		{`git -C "my dir" push --force origin mu/ship-vd`, allow, refuse},
		{`git -C "my dir" push`, allow, refuse},
		{`git -C'/other repo' push`, allow, refuse},
		// cd resolves the whole quoted operand, so a bound push from a
		// subdirectory with a space in its name is no longer refused.
		{`cd "my dir" && git push origin mu/ship-vd`, refuse, allow},
		// Every string payload is read as shell, whatever consumes it: a
		// quoted or escaped word, a heredoc body, a here-string.
		{`echo "x && git push --force"`, refuse, refuse},
		{"cat <<EOF\ngit push --force\nEOF", refuse, refuse},
		{"git commit -F- <<'EOF'\nmsg; git reset --hard\nEOF", refuse, refuse},
		// Substitution is refused on the raw command, heredoc bodies included.
		{"git $(printf add) file.txt", refuse, refuse},
		{"git `printf add` file.txt", refuse, refuse},
		{"cat <<EOF\n$(git push --force)\nEOF", refuse, refuse},
		{"cat <<EOF\n`git push --force`\nEOF", refuse, refuse},
		{"cat <<'EOF'\n$(git push --force)\nEOF", refuse, refuse},
		// The same raw check is what refuses a substitution in a heredoc body
		// for the write channel: shellWriteTargets strips bodies before it
		// tokenizes, and both guards run on every hook call.
		{"cat <<EOF\n$(touch ../escaped)\nEOF", refuse, refuse},
		// Real segment separators still end a segment.
		{"git status\ngit push --force origin mu/ship-vd", refuse, refuse},
		{"git status && git reset --hard", refuse, refuse},
		{`git reset --hard`, refuse, refuse},
		{`git push --force`, refuse, refuse},
		{`/usr/bin/git push --force`, refuse, refuse},
		// Script text a shell runs is a payload like any other.
		{"sh <<EOF\ngit push --force\nEOF", refuse, refuse},
		{"bash <<'EOF'\ngit reset --hard\nEOF", refuse, refuse},
		{"sh <<EOF\ngit reset --hard\nEOF", refuse, refuse},
		{`bash -c "git push --force"`, refuse, refuse},
		{`sh -c 'git reset --hard'`, refuse, refuse},
		{`eval "git reset --hard"`, refuse, refuse},
		{`sh <<< "git push --force"`, refuse, refuse},
		{`bash <<< 'git reset --hard'`, refuse, refuse},
		{`zsh -c "git reset --hard"`, refuse, refuse},
		{`echo ok && bash -c "git reset --hard"`, refuse, refuse},
		{`bash -c "/usr/bin/git push --force"`, refuse, refuse},
		{`sh -c "cd /tmp && git reset --hard"`, refuse, refuse},
		{`/bin/bash -xc "git push --force"`, refuse, refuse},
		{`FOO=1 dash -c "git push --force"`, refuse, refuse},
		{"echo ok; <<EOF ksh\ngit push --force\nEOF", refuse, refuse},
		{`sudo bash -c "git push --force"`, refuse, refuse},
		{`nohup sh -c 'git reset --hard'`, refuse, refuse},
		{`timeout 5 bash -c "git push --force"`, refuse, refuse},
		{`env bash -c "git reset --hard"`, refuse, refuse},
		{`echo bash -c "git push --force"`, refuse, refuse},
		{`echo "git push --force" | sh`, refuse, refuse},
		{"cat <<EOF | sh\ngit push --force\nEOF", refuse, refuse},
		{"echo \"git push --force\" | sudo sh", refuse, refuse},
		{"echo \"git push --force\" | env sh", refuse, refuse},
		// No list of interpreters or script runners decides it.
		{`echo "git push --force" | sh /dev/stdin`, refuse, refuse},
		{`echo "git push --force" | bash /dev/fd/0`, refuse, refuse},
		{". /dev/stdin <<EOF\ngit push --force\nEOF", refuse, refuse},
		{`source /dev/stdin <<< "git push --force"`, refuse, refuse},
		{`echo "git push --force" | source /dev/stdin`, refuse, refuse},
		{`echo "git push --force" | . /dev/stdin`, refuse, refuse},
		{`fish -c "git push --force"`, refuse, refuse},
		{`echo "git push --force" | fish`, refuse, refuse},
		{`echo "git push --force" | busybox sh`, refuse, refuse},
		{`env -S "sh -c git\ push" x`, refuse, refuse},
		{`printf 'git push --force' > x.sh && sh x.sh`, refuse, refuse},
		// Payloads that read as no git mutation stay allowed.
		{`ps aux | grep bash`, allow, allow},
		{`ps aux | grep -c sh`, allow, allow},
		{`cd "my dir" && git status`, allow, allow},
		{`bash -c "git status"`, allow, allow},
		{"sh <<EOF\ngit status\nEOF", allow, allow},
		{`eval eval git status`, allow, allow},
		{"git commit -F- <<'EOF'\ndon't reset\nEOF", allow, allow},
		// A script that never appears as a literal is not read (residual).
		{`bash -c "$CMD"`, allow, allow},
		{`xargs -I{} sh -c {}`, allow, allow},
		// An unclosed quote's tail is one word, and that word is read too.
		{"echo \"it's\ngit push --force", allow, refuse},
		// A payload the guard cannot recover is refused.
		{"bash <<EOF\ngit status", allow, refuse},
		{"echo " + strings.Repeat(`\`, 32) + "x", allow, refuse},
		{"echo " + strings.Repeat(`\`, 16) + "x", allow, allow},
		// Commands that hand git its arguments stay refused.
		{`echo --force | xargs git push`, refuse, refuse},
		{`xargs git push`, refuse, refuse},
		{`env git reset --hard`, refuse, refuse},
		// ANSI-C quoting is one quoted word whose escapes are decoded, and a
		// word with an escape the guard does not decode is refused.
		{`bash -c $'git push --force'`, allow, refuse},
		{`$'git' push --force`, allow, refuse},
		{`bash -c $'git\x20push --force'`, allow, refuse},
		{`bash -c $'git\040push --force'`, allow, refuse},
		{`$'\u0067it' push --force`, allow, refuse},
		{`echo $'a\tb\n'`, allow, allow},
		// Quoted text is itself read as shell, so ANSI-C text inside it is too.
		{`echo "$'git push --force'"`, allow, refuse},
		// An empty quoted word is still a word, so it is the -C operand and
		// git runs the verb after it in the current directory.
		{`git -C $'' push --force`, refuse, refuse},
		{`git -C '' push --force`, allow, refuse},
		{`git -C "" push --force`, allow, refuse},
		{`git -C ''"" push --force`, allow, refuse},
		// An empty word in the verb position is the verb, which git rejects.
		{`git '' push --force`, refuse, allow},
		// bash reads `$"..."` as its double-quoted text.
		{`$"git" push --force`, allow, refuse},
		{`bash -c $"git push --force"`, allow, refuse},
		{`bash -c 'bash -c $"git push --force"'`, allow, refuse},
		{`bash -c $"git status"`, allow, allow},
		// The word of a parameter expansion that bash can substitute is
		// another reading of the token, and a mutation in any reading refuses.
		{`${x:-git} push --force`, allow, refuse},
		{`${x:-$'\x67it'} push --force`, allow, refuse},
		{`${x:=git} push --force`, allow, refuse},
		{`${x:+git} push --force`, allow, refuse},
		{`bash -c '${x-git} push --force'`, allow, refuse},
		{`"${x:-git push --force}"`, allow, refuse},
		{`${x:-git} status`, allow, allow},
		// The classifier finds git in any position, as for `echo git push`.
		{`echo ${x:-git push}`, allow, refuse},
		// An unquoted `${...}` is one word, and its substituted word is split.
		{`${x:-git push --force}`, allow, refuse},
		{`bash -c '${x:-git push --force}'`, allow, refuse},
		{`echo ${x:-a b}`, allow, allow},
		{`echo ${x`, allow, refuse},
		// Every token at the same alternate index is one more reading.
		{`${a:-git} ${b:-push} --force`, allow, refuse},
		// A cd operand the shell computes names a directory the guard never
		// resolves, so a mutation after it has no bound target.
		{"cd ${x:-" + primary + "} && git push origin mu/ship-vd", refuse, refuse},
		{"cd ${x:-" + worktree + "} && git push origin mu/ship-vd", refuse, refuse},
		// A substituted word is a write target in the shared checkout.
		{"cd " + primary + "/docs && cp ../README.md ${x:-b c}", refuse, refuse},
	}
	if runtime.GOOS != "windows" {
		// A POSIX backslash-newline is a line continuation that joins the
		// text around it; the Windows reading keeps the backslash literal.
		rows = append(rows, []struct {
			command string
			old     bool
			want    bool
		}{
			{"/usr/bin/git \\\npush --force", allow, refuse},
			{"git \\\nreset --hard", allow, refuse},
			{"echo hi && git \\\npush --force", allow, refuse},
			{"git \\\n-C /tmp push", allow, refuse},
			{"git pu\\\nsh --force", allow, refuse},
			{"bash -c \"git pu\\\nsh --force\"", refuse, refuse},
			// A single-quoted continuation is literal in the word, but the
			// word is read as shell again, where it joins.
			{"echo 'git \\\npush --force'", allow, refuse},
			// `$$` is the PID, so the `'` after it opens an ordinary single
			// quote that the first `\'` closes; bash then runs git.
			{`$$'\' ; gi\t push --force #\''`, refuse, refuse},
			{`echo $$'\' ; gi\t push --force #\''`, refuse, refuse},
		}...)
		// bash ends a comment at the newline and runs `gi\t` as git, but the
		// tokenizer reads the `$'` in the comment as ANSI-C quoting and
		// decodes `\t` to a TAB that splits the verb. The raw span, read as
		// shell again, is what bash runs.
		const commented = "echo hi # $'\ngi\\t push --force #'"
		bashC := func(script string) string {
			return "bash -c '" + strings.ReplaceAll(script, "'", `'\''`) + "'"
		}
		rows = append(rows, []struct {
			command string
			old     bool
			want    bool
		}{
			{commented, refuse, refuse},
			{bashC(commented), refuse, refuse},
			{bashC(bashC(commented)), refuse, refuse},
			{bashC(bashC(bashC(commented))), refuse, refuse},
			{"x=1 # $'\ngi\\t push --force #'", refuse, refuse},
			{"bash <<'EOF'\n" + commented + "\nEOF", refuse, refuse},
			// The raw reading reaches ANSI-C text inside double quotes too,
			// where bash decodes nothing and runs no git (ruling (e)).
			{`echo "$'gi\\t push --force'"`, allow, refuse},
		}...)
	}
	for _, tc := range rows {
		block, reason := runPiSafetyForGit(t, worktree, tc.command)
		if block != tc.want {
			t.Errorf("%q block=%v reason=%q, want block=%v (was %v at 8765440e)", tc.command, block, reason, tc.want, tc.old)
		}
	}
}

// TestMunsuCommandRulesReadParameterExpansionWords pins that the munsu watch
// and no-mistakes rules read a parameter expansion's substituted words, and
// never re-read an expansion that reads as itself, so a benign one does not
// exhaust the depth bound.
func TestMunsuCommandRulesReadParameterExpansionWords(t *testing.T) {
	for _, tc := range []struct {
		command string
		watch   bool
		names   bool
	}{
		{`echo ${x:-a b}`, false, false},
		{`${x:-munsu watch}`, true, false},
		{`ls ${x:-.no-mistakes/x}`, false, true},
		// Every candidate at each position the rule reads is read.
		{`${a:-munsu} ${b:-watch}`, true, false},
		{`${a:-${c:-munsu}} ${b:-watch}`, true, false},
		{`munsu ${a:---home} ${b:-/h} watch`, true, false},
		{`${a:-munsu} ${b:-watch} run`, false, false},
		// A word that may be removed leaves the next word in its place.
		{`${b:+x} munsu watch`, true, false},
		{`munsu $b watch`, true, false},
		{`munsu watch "$b"`, true, false},
	} {
		got, tooDeep := munsuInvocations(tc.command, 0)
		watch := slices.ContainsFunc(got, func(invocation munsuInvocation) bool {
			return invocation.subcommand == "watch" && !watchInvocationAllowed(invocation.following)
		})
		if tooDeep || watch != tc.watch {
			t.Errorf("munsuInvocations(%q) = %q tooDeep=%v, want a bare watch=%v", tc.command, got, tooDeep, tc.watch)
		}
		if names, tooDeep := namesNoMistakesDir(tc.command, 0); tooDeep || names != tc.names {
			t.Errorf("namesNoMistakesDir(%q) = %v tooDeep=%v, want %v", tc.command, names, tooDeep, tc.names)
		}
	}
}

// TestMunsuInvocationsDepthBound pins the payload depth the munsu watch check
// reads through: the git guard's bound, past which the hook refuses rather
// than reading on or stopping short.
func TestMunsuInvocationsDepthBound(t *testing.T) {
	bashC := func(script string) string {
		return "bash -c '" + strings.ReplaceAll(script, "'", `'\''`) + "'"
	}
	inside, inRun := "munsu watch", "munsu watch run"
	for range maxShellPayloadDepth {
		inside, inRun = bashC(inside), bashC(inRun)
	}
	if got, tooDeep := munsuInvocations(inside, 0); tooDeep || len(got) != 1 || got[0].subcommand != "watch" || len(got[0].following) != 0 {
		t.Errorf("munsuInvocations at depth %d = %+v tooDeep=%v, want a bare watch", maxShellPayloadDepth, got, tooDeep)
	}
	if _, tooDeep := munsuInvocations(bashC(inside), 0); !tooDeep {
		t.Errorf("munsuInvocations past depth %d: tooDeep=false, want true", maxShellPayloadDepth)
	}
	names := "ls .no-mistakes"
	for range maxShellPayloadDepth {
		names = bashC(names)
	}
	if got, tooDeep := namesNoMistakesDir(names, 0); !got || tooDeep {
		t.Errorf("namesNoMistakesDir at depth %d = %v tooDeep=%v, want true", maxShellPayloadDepth, got, tooDeep)
	}
	if got, tooDeep := namesNoMistakesDir(bashC(names), 0); got || !tooDeep {
		t.Errorf("namesNoMistakesDir past depth %d = %v tooDeep=%v, want tooDeep", maxShellPayloadDepth, got, tooDeep)
	}
	checkPath := t.TempDir()
	if block, reason := runPiSafetyForGit(t, checkPath, inside); !block || !strings.Contains(reason, "Bare 'munsu watch'") {
		t.Errorf("hook at depth %d: block=%v reason=%q, want the bare watch refusal", maxShellPayloadDepth, block, reason)
	}
	const tooDeep = "shell payload nesting is too deep; munsu command rules cannot be checked"
	if block, reason := runPiSafetyForGit(t, checkPath, bashC(inRun)); !block || reason != tooDeep {
		t.Errorf("hook past depth %d: block=%v reason=%q, want %q", maxShellPayloadDepth, block, reason, tooDeep)
	}
}

// TestSafetyCheckReadsFunctionSubstitutionExpansionsAndCandidates pins the
// hook verdicts review-28489e659251 moved. Every refused row was allowed at
// 28489e65:
//   - bash 5.3 runs `${ cmd; }` and `${| cmd; }` as command substitution;
//   - a subscripted or indirect parameter still substitutes its word, and a
//     parameter expansion the tokenizer cannot parse is refused;
//   - `$"..."` in a double-quoted default word is its double-quoted text;
//   - every candidate word at each position the verb classifier reads is
//     read, nested defaults included.
//
// The allowed rows run no git and write nothing in bash 3.2 or 5.3.
// TestSafetyCheckReadsLongCommandInLinearTime pins that the hook's walk over
// a segment grows linearly: a git add of 16000 words took 32.9s and gigabytes
// when every position copied the words after it, and takes milliseconds and
// under 100 MiB without.
func TestSafetyCheckReadsLongCommandInLinearTime(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-fs", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-fs")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-fs")
	var words strings.Builder
	for i := range 16000 {
		fmt.Fprintf(&words, " f%d", i)
	}
	command := "git add" + words.String()
	allocated, elapsed := measureAllocation(func() {
		if block, reason := runPiSafetyForGit(t, worktree, command); block {
			t.Errorf("git add of 16000 words: block=true reason=%q, want allow", reason)
		}
	})
	if elapsed > 5*time.Second || allocated > 512<<20 {
		t.Errorf("git add of 16000 words took %v and %d MiB, want under 5s and 512 MiB", elapsed, allocated>>20)
	}
	// The write guard's enumeration of a write segment is linear too. Its
	// per-target classification is not what this pins.
	segment := tokenizeSegments(backslashEscapes, "rm"+words.String())[0]
	allocated, elapsed = measureAllocation(func() {
		if readings, ok := writeReadings(segment); !ok || len(readings) != 1 || len(readings[0]) != len(segment) {
			t.Errorf("writeReadings of rm with 16000 words = %d readings ok=%v, want one", len(readings), ok)
		}
	})
	if elapsed > 5*time.Second || allocated > 512<<20 {
		t.Errorf("writeReadings of rm with 16000 words took %v and %d MiB, want under 5s and 512 MiB", elapsed, allocated>>20)
	}
	// So is the dedupe of its targets: sixteen times the targets cost about
	// sixteen times as much, where a list scan per target cost about 256.
	// Comparing two sizes under the same load keeps -race and a busy host
	// from deciding the verdict.
	targetsCost := func(n int) time.Duration {
		command := "rm " + strings.Join(strings.Fields(words.String())[:n], " ")
		best := time.Duration(math.MaxInt64)
		for range 3 {
			start := time.Now()
			if targets, ambiguous := shellWriteTargets(worktree, command); len(targets) != n || ambiguous {
				t.Errorf("shellWriteTargets of rm with %d words = %d targets ambiguous=%v, want %d", n, len(targets), ambiguous, n)
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	if small, large := targetsCost(1000), targetsCost(16000); large > 64*small {
		t.Errorf("shellWriteTargets of rm with 16000 words took %v, %.0f times 1000 words (%v), want under 64", large, float64(large)/float64(small), small)
	}
	// The git guard reads each distinct word that reads as more than itself
	// once: sixteen times the quoted words cost about fourteen times as much,
	// where a list scan per word cost about 75.
	echoCost := func(n int) time.Duration {
		var command strings.Builder
		command.WriteString("echo")
		for i := range n {
			fmt.Fprintf(&command, " 'a %d'", i)
		}
		best := time.Duration(math.MaxInt64)
		for range 3 {
			start := time.Now()
			if block, reason := runPiSafetyForGit(t, worktree, command.String()); block {
				t.Errorf("echo of %d quoted words: block=true reason=%q, want allow", n, reason)
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	if small, large := echoCost(4000), echoCost(64000); large > 32*small {
		t.Errorf("echo of 64000 quoted words took %v, %.0f times 4000 words (%v), want under 32", large, float64(large)/float64(small), small)
	}
}

// TestSafetyCheckReadsNestedSubshellsInLinearTime pins F-E of
// review-cf6153cd273e: a subshell shares the directory stack pushd built
// instead of copying it, so n pushes under n nested subshells cost linear
// time. The budget is eight times the cost of a quarter.
func TestSafetyCheckReadsNestedSubshellsInLinearTime(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-stack", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-stack")
	cost := func(n int) time.Duration {
		command := strings.Repeat("pushd "+worktree+"; ", n) + strings.Repeat("(", n) + "git status" + strings.Repeat(")", n)
		best := time.Duration(math.MaxInt64)
		for range 3 {
			start := time.Now()
			if block, reason := runPiSafetyForGit(t, worktree, command); block {
				t.Fatalf("%d pushes under %d subshells: block=true reason=%q, want allow", n, n, reason)
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	if small, large := cost(4000), cost(16000); large > 8*small {
		t.Errorf("16000 pushes and subshells took %v, %.1f times 4000 (%v), want under 8", large, float64(large)/float64(small), small)
	}
}

// measureAllocation returns the bytes run allocates and how long it takes.
func measureAllocation(run func()) (uint64, time.Duration) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	run()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc, elapsed
}

func TestSafetyCheckReadsFunctionSubstitutionExpansionsAndCandidates(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-fs", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-fs")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-fs")
	docs := filepath.Join(primary, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}

	const refuse, allow = true, false
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{`${ git push --force; }`, refuse},
		{`${| git push --force; }`, refuse},
		{`bash -c '${ git push --force; }'`, refuse},
		{`echo ${ git push --force;}`, refuse},
		{`x=${ git push --force; }`, refuse},
		{"${\tgit push --force; }", refuse},
		{`${ munsu watch; }`, refuse},
		{`${ ls .no-mistakes; }`, refuse},
		{"cd " + docs + " && ${ echo hi > f; }", refuse},
		{`${x[0]:-git} push --force`, refuse},
		{`${x[1]-git} push --force`, refuse},
		{`${x[@]:-git push --force}`, refuse},
		{`${!x:-git} push --force`, refuse},
		{`${!x-git} push --force`, refuse},
		{`echo ${x&}`, refuse},
		{`echo ${x["0"]}`, refuse},
		{`echo ${x[0}`, refuse},
		{`echo ${x[a]b}`, refuse},
		// A subscript is any bracket-balanced text with no quote and no `}`.
		{`echo ${x[$i]}`, allow},
		{`echo "${arr[$i]}"`, allow},
		{`echo ${x[i+1]}`, allow},
		{`echo ${arr[-1]}`, allow},
		{`echo ${x[a[0]]}`, allow},
		{`${x[i+1]:-git} push --force`, refuse},
		{`echo ${x[0]}`, allow},
		{`echo ${!x}`, allow},
		{`echo ${#x}`, allow},
		{`echo ${x@Q}`, allow},
		{`echo ${x[@]}`, allow},
		{`echo ${!prefix*}`, allow},
		{`"${x:-$"git"}" push --force`, refuse},
		{`"${x:-$"g"it}" push --force`, refuse},
		{`${a:-${c:-git}} ${b:-push} --force`, refuse},
		{`${a:-git} ${b:-${c:-push}} --force`, refuse},
		{`${a:-git} ${b:-push} --force`, refuse},
		{`${a:-munsu} ${b:-watch}`, refuse},
		{"cd " + docs + " && ${a:-rm} ${b:-f}", refuse},
		{"cd " + docs + " && cp ${a:--t} ${b:-.} ../README.md", refuse},
		{"cd " + docs + " && echo ${x:-a > f}", allow},
		{"cd " + docs + " && echo ${x#a > f}", allow},
		{"cd " + docs + " && echo ${x#a; touch f #}", allow},
		{"cd " + docs + " && ${x:-echo hi > f}", allow},
		// An unquoted word made only of expansions may be removed.
		{`${a:-git} ${b:+x} push --force`, refuse},
		{`git ${b:+x} push --force`, refuse},
		{`git $b push --force`, refuse},
		{`git ${b} push --force`, refuse},
		{`${a:-git} ${b-} push --force`, refuse},
		{`bash -c '${a:-git} ${b:+x} push --force'`, refuse},
		{`bash -c 'git $b push --force'`, refuse},
		{`${b:+x} munsu watch`, refuse},
		{`munsu $b watch`, refuse},
		{"cd " + docs + " && cp -t $b f g", refuse},
		{"cd " + docs + " && cp ../README.md $b .", refuse},
		// A quoted `@` form is removed when it has no elements; any other
		// quoted expansion is an empty word.
		{`git "${b[@]:+x}" push --force`, refuse},
		{`${a:-git} "${b[@]:+x}" push --force`, refuse},
		{`git "${@:+x}" push --force`, refuse},
		{`git "${b[@]}" push --force`, refuse},
		{`git "$@" push --force`, refuse},
		{`git "${!pre@}" push --force`, refuse},
		{`git "$@""" push --force`, refuse},
		{`git "$@"$b push --force`, refuse},
		{`bash -c 'git "$@" push --force'`, refuse},
		{`bash -c 'git "${b[@]:+x}" push --force'`, refuse},
		{`bash -c 'git "${b[@]}" push --force'`, refuse},
		{`munsu "$@" watch`, refuse},
		{"cd " + docs + ` && cp -t "$@" f g`, refuse},
		{`git ${b:+"x"} push --force`, refuse},
		// `$"..."` is a double quote, and `$''` and `$""` are empty quoted
		// parts.
		{`git $"$@" push --force`, refuse},
		{`git $"${e[@]}" push --force`, refuse},
		{`git $"${@:+x}" push --force`, refuse},
		{`git ""$"$@" push --force`, refuse},
		{`git $"$@"$'' push --force`, refuse},
		{`munsu $"$@" watch`, refuse},
		{`bash -c 'git $"$@" push --force'`, refuse},
		{`bash -c "git \$\"\$@\" push --force"`, refuse},
		{"git -C $\"$@\" " + primary + " push origin mu/ship-fs", refuse},
		{"cd " + docs + ` && cp -t $"$@" f g`, refuse},
		{"cd " + docs + ` && $"$@" rm f`, refuse},
		// A line continuation inside the quotes is removed before the `@`
		// form is read.
		{"git \"$@\\\n\" push --force", refuse},
		{"git \"\\\n$@\" push --force", refuse},
		{"git \"$\\\n@\" push --force", refuse},
		{"git $\\\n@ push --force", refuse},
		{"munsu \"$@\\\n\" watch", refuse},
		{"git -C \"$@\\\n\" " + primary + " push origin mu/ship-fs", refuse},
		{"cd " + docs + " && cp -t \"$@\\\n\" f g", refuse},
		{"bash -c 'git \"$@\\\n\" push --force'", refuse},
		{"git \"${e[@]:+x}\\\n\" push --force", refuse},
		{"git \"\\\n${e[@]:+x}\" push --force", refuse},
		{"munsu \"${e[@]:+x}\\\n\" watch", refuse},
		{"cd " + docs + " && cp -t \"${e[@]:+x}\\\n\" f g", refuse},
		{"git $\"$@\\\n\" push --force", refuse},
		{"git $\"\\\n$@\" push --force", refuse},
		{"munsu $\"$@\\\n\" watch", refuse},
		{"cd " + docs + " && cp -t $\"$@\\\n\" f g", refuse},
		{"git \"\\$@\" push --force", allow},
		// A line continuation from a `$` to the end of its expansion makes the
		// word undecodable; one outside any expansion keeps its verdict.
		{"$\\\n{x:-git} push --force", refuse},
		{"${x:\\\n-git} push --force", refuse},
		{"${x:-g\\\nit} push --force", refuse},
		{"\"$\\\n{x:-git}\" push --force", refuse},
		{"$\\\n{\\\nx\\\n:-git} push --force", refuse},
		{"$\\\nx push --force", refuse},
		{"munsu $\\\n{x:-watch}", refuse},
		{"git \\\nstatus", allow},
		{"# c \\\ngit status", allow},
		{`git $"$b" push`, allow},
		{`git "$b" push`, allow},
		{`git "$*" push --force`, allow},
		{`git "${b[@]:-}" push --force`, allow},
		{`git "${e[*]}" push --force`, allow},
		{`git "${!p*}" push --force`, allow},
		// In a command that names IFS, an unquoted group's word is undecodable.
		{`IFS=:; ${x:-git:push:--force}`, refuse},
		{`bash -c 'IFS=:; ${x:-git:push:--force}'`, refuse},
		{`IFS=: read a b`, allow},
		{`IFS=: read a b; echo "${x:-a:b}" $x`, allow},
		// A name built from a variable's value is class (c).
		{`n=I; declare ${n}FS=:; ${x:-git:push:--force}`, allow},
		// Only a segment that can write is enumerated; one past the bound
		// refuses.
		{`echo $a $b $c $d $e $f $g $h $i`, allow},
		{`echo ${a:-1} ${b:-2} ${c:-3} ${d:-4} ${e:-5} ${f:-6}`, allow},
		{"cd " + docs + " && echo $1 $2 $3 $4 $5 $6 $7 $8 $9", allow},
		{`rm $a $b $c $d $e $f $g $h $i`, refuse},
		{"cd " + docs + " && rm $1 $2 $3 $4 $5 $6 $7 $8 $9", refuse},
		// Fail-closed over-refusal: the removed reading is taken even where
		// the word cannot be empty.
		{`git ${x:-status} push --force`, refuse},
	} {
		if block, reason := runPiSafetyForGit(t, worktree, tc.command); block != tc.want {
			t.Errorf("%q block=%v reason=%q, want block=%v", tc.command, block, reason, tc.want)
		}
	}
	// Function substitution is refused as command substitution, before the
	// tokenizer reads the group as an undecodable parameter expansion.
	const substitution = "compound shell command with command substitution is not allowed for git mutation"
	for _, command := range []string{"${ git status; }", "${| git status; }", "${\tgit status; }", "${\ngit status; }",
		"echo $\\\n(git push --force)", "cat <\\\n(git push --force)", "echo >\\\n(git push --force)",
		"$\\\n{ git status; }", "bash -c 'echo $\\\n(git push --force)'"} {
		if block, reason := runPiSafetyForGit(t, worktree, command); !block || reason != substitution {
			t.Errorf("%q: block=%v reason=%q, want %q", command, block, reason, substitution)
		}
	}
	// The IFS rule refuses the word as undecodable, not as a push. Any
	// decoded word of the command at any depth that names IFS sets it for
	// every depth: eval and source run their payload in the same shell.
	const undecodable = "shell word cannot be decoded; git mutation cannot be checked"
	for _, command := range []string{
		`IFS=:; ${x:-git:push:--force}`,
		`IFS=:; echo ${x:-status}`,
		`IFS=:; eval '${x:-git:push:--force}'`,
		`IFS=:; eval "\${x:-git:push:--force}"`,
		`IFS=: eval '${x:-git:push:--force}'`,
		"IFS=:; . /dev/stdin <<'EOF'\n${x:-git:push:--force}\nEOF",
		`declare IF""S=:; ${x:-git:push:--force}`,
		`export IF\S=:; ${x:-git:push:--force}`,
		`read IF''S <<< :; ${x:-git:push:--force}`,
		`printf -v IF""S :; ${x:-git:push:--force}`,
		`declare $'\x49FS'=:; ${x:-git:push:--force}`,
	} {
		if block, reason := runPiSafetyForGit(t, worktree, command); !block || reason != undecodable {
			t.Errorf("%q: block=%v reason=%q, want %q", command, block, reason, undecodable)
		}
	}
	// A value word with more than one candidate leaves the target unknown.
	const unknownTarget = "git mutation target cannot be determined"
	if block, reason := runPiSafetyForGit(t, worktree, "git -C ${x:-.} push origin mu/ship-fs"); !block || reason != unknownTarget {
		t.Errorf("git -C ${x:-.} push: block=%v reason=%q, want %q", block, reason, unknownTarget)
	}
}

// TestSafetyCheckReadsCaseStackAndCdOptions pins the git guard's side of
// review-cdc998a32be1: a case pattern's `)` does not return from a subshell,
// pushd, popd and `cd -` move the cwd (an unknown one refuses), cd options
// and redirections are read past, and a function body is read in its own
// scope.
func TestSafetyCheckReadsCaseStackAndCdOptions(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-case", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-case")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-case")

	simpleBodyCases := []struct {
		name     string
		commands []string
	}{
		{"top-level simple body", []string{`f() cd ` + primary + `; f; git add f`}},
		{"plain non-identifier function name", []string{`foo-bar() { cd ` + primary + `; }; foo-bar; git add f`}},
		{"non-compound reserved-word opener", []string{
			`f() function g { cd ` + primary + `; }; f; g; git add f`,
			`f() coproc cd ` + primary + `; f; git add f`,
		}},
		{"named-shell payload", []string{`zsh -c 'f() cd ` + primary + `; f; git add f'`}},
		{"quoted function name", []string{
			`'f'() cd ` + primary + `; f; git add f`,
			`"f"() cd ` + primary + `; f; git add f`,
			`'f'() { cd ` + primary + `; }; f; git add f`,
			`'f'() { :; }; git add f`,
			`zsh -c "'f'() cd ` + primary + `; f; git add f"`,
		}},
		{"quoted function name after a command prefix", []string{
			`! 'f'() cd ` + primary + `; f; git add f`,
			`! 'f'() { cd ` + primary + `; }; f; git add f`,
			`time 'f'() cd ` + primary + `; f; git add f`,
			`time 'f'() { cd ` + primary + `; }; f; git add f`,
			`time -p 'f'() cd ` + primary + `; f; git add f`,
			`time -p 'f'() { cd ` + primary + `; }; f; git add f`,
			`if true; then 'f'() cd ` + primary + `; fi; f; git add f`,
			`if true; then 'f'() { cd ` + primary + `; }; fi; f; git add f`,
			`if 'f'() cd ` + primary + `; then :; fi; f; git add f`,
			`if 'f'() { cd ` + primary + `; }; then :; fi; f; git add f`,
			`while 'f'() cd ` + primary + `; do break; done; f; git add f`,
			`while 'f'() { cd ` + primary + `; }; do break; done; f; git add f`,
			`until 'f'() cd ` + primary + `; do break; done; f; git add f`,
			`until 'f'() { cd ` + primary + `; }; do break; done; f; git add f`,
			`if false; then :; else 'f'() cd ` + primary + `; fi; f; git add f`,
			`if false; then :; else 'f'() { cd ` + primary + `; }; fi; f; git add f`,
			`if false; then :; elif true; then 'f'() cd ` + primary + `; fi; f; git add f`,
			`if false; then :; elif true; then 'f'() { cd ` + primary + `; }; fi; f; git add f`,
			`for x in 1; do 'f'() cd ` + primary + `; done; f; git add f`,
			`for x in 1; do 'f'() { cd ` + primary + `; }; done; f; git add f`,
			`{ 'f'() cd ` + primary + `; }; f; git add f`,
			`{ 'f'() { cd ` + primary + `; }; }; f; git add f`,
		}},
	}
	for _, tc := range simpleBodyCases {
		for _, command := range tc.commands {
			block, reason := runPiSafetyForGit(t, worktree, command)
			wantReason := "simple-command function body"
			if tc.name == "quoted function name" || tc.name == "quoted function name after a command prefix" {
				wantReason = "quoted function name"
			}
			if tc.name == "plain non-identifier function name" {
				if !block {
					t.Errorf("%s: %q: block=%v reason=%q, want refusal", tc.name, command, block, reason)
				}
				continue
			}
			if !block || !strings.Contains(reason, wantReason) {
				t.Errorf("%s: %q: block=%v reason=%q, want %s refusal", tc.name, command, block, reason, wantReason)
			}
		}
	}
	for _, quotedParenCommand := range []string{
		`'f()' cd ` + primary + `; f; git add f`,
		`function 'f()' { cd ` + primary + `; }; f; git add f`,
		`function f\(\) { cd ` + primary + `; }; f; git add f`,
	} {
		if block, reason := runPiSafetyForGit(t, worktree, quotedParenCommand); block {
			t.Errorf("%q: block=%v reason=%q, want quoted-paren form allowed", quotedParenCommand, block, reason)
		}
	}
	for _, command := range []string{
		`coproc 'f'() cd ` + primary + `; f; git add f`,
		`coproc 'f'() { cd ` + primary + `; }; f; git add f`,
	} {
		if block, reason := runPiSafetyForGit(t, worktree, command); block {
			t.Errorf("%q refused: %s; want coproc exclusion", command, reason)
		}
	}
	for _, command := range []string{
		"f() # c1\n# c2\n{ cd " + primary + "; }; f; git add f",
		"f() # c1\n# c2\nif cd " + primary + "; then :; fi; f; git add f",
		"f() # c1\n# c2\nwhile cd " + primary + "; do break; done; f; git add f",
	} {
		if block, reason := runPiSafetyForGit(t, worktree, command); !block {
			t.Errorf("%q: block=%v reason=%q, want git mutation refusal", command, block, reason)
		}
	}
	commentSubshell := "f() # c\n( cd " + primary + " ); f; git add f"
	if block, reason := runPiSafetyForGit(t, worktree, commentSubshell); block {
		t.Errorf("%q: block=%v reason=%q, want comment-transparent subshell body allowed", commentSubshell, block, reason)
	}
	for _, command := range []string{
		"f ()\ncd " + primary + "; f; git add f",
		"f() \n  cd " + primary + "; f; git add f",
		"function f()\ncd " + primary + "; f; git add f",
	} {
		block, reason := runPiSafetyForGit(t, worktree, command)
		if !block || !strings.Contains(reason, "simple-command function body") {
			t.Errorf("%q: block=%v reason=%q, want simple-command function body refusal", command, block, reason)
		}
	}
	for _, command := range []string{
		"(true; cd " + primary + "; case $x in a) :;; esac; git add f)",
		"(true; cd " + primary + "; case $x in a) :;; esac; git push origin mu/ship-case)",
		"cd " + primary + "; (case $x in a) :;; esac; cd " + worktree + "); git add f",
		"case a in a) git status",
		"pushd " + primary + " && git add f",
		"pushd " + primary + "; pushd " + worktree + "; popd; git add f",
		"popd; git add f",
		"pushd; git add f",
		"cd " + primary + "; cd " + worktree + "; cd -; git add f",
		"cd -- " + primary + " && git add f",
		"builtin cd -- " + primary + " && git add f",
		"2>/dev/null cd " + primary + "; git add f",
		"f() { cd " + primary + "; git add f; }",
		"coproc { cd " + primary + "; git add f; }",
		"f() { cd " + primary + "; }; f; git add f",
		"f() { eval 'cd " + primary + "'; }; f; git add f",
		"eval 'f() { cd " + primary + "; }'; f; git add f",
		"f() if cd " + primary + "; then :; fi; f; git add f",
		"cd; git add f",
		"HOME=" + primary + " cd && git add f",
		"cd ~; git add f",
		"git 2>/dev/null push --force",
		"git 2>&1 -C " + primary + " add f",
		"git 2> /dev/null -C " + primary + " add f",
		"git &>/dev/null -C " + primary + " add f",
		"git push 2>/dev/null --force origin main",
	} {
		if block, _ := runPiSafetyForGit(t, worktree, command); !block {
			t.Errorf("%q allowed, want refused", command)
		}
	}
	for _, command := range []string{
		"(case $x in a) cd " + primary + ";; esac); git add f",
		"pushd " + primary + "; popd; git add f",
		"pushd -n " + primary + "; git add f",
		"popd; git status",
		"coproc cd " + primary + "; git add f",
		"f() { cd " + primary + "; }; git add f",
		`git commit -m "case sensitivity fix"`,
		"f() ( cd " + primary + "; ); f; git add f",
		"(f() { cd " + primary + "; }; f); git add f",
		"f() if cd " + primary + "; then :; fi; git add f",
		"git 2>/dev/null add f",
		"git add f 2>&1 | cat",
	} {
		if block, reason := runPiSafetyForGit(t, worktree, command); block {
			t.Errorf("%q refused: %s", command, reason)
		}
	}
}

// TestSafetyCheckReadsRedirectPrefixesExpansionsAndLateFunctions pins the git
// verdicts of R1, R2, R4 and R6 of review-80b12d4f0a2e and of its round 2: a
// spaced fd, and any `{name}` word, is a refspec, a `)` inside a `${...}`
// group or a comment closes no subshell, a `<` word ends at a `)`, a subscripted assignment still lets
// cd move the shell, and a function defined after the body that calls it
// moves the directory.
func TestSafetyCheckReadsRedirectPrefixesExpansionsAndLateFunctions(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-rv", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-rv")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-rv")

	for _, command := range []string{
		"git push origin mu/ship-rv 9 >/dev/null",
		"git push origin mu/ship-rv {x} >/dev/null",
		"git push origin mu/ship-rv 12 &>/dev/null",
		`git push origin mu/ship-rv "9">/dev/null`,
		`git push origin mu/ship-rv "9">&2`,
		"git push origin mu/ship-rv 9 >&2",
		"git push origin mu/ship-rv {a[1]}>/dev/null",
		"(cd " + primary + "; : ${x:-)}; git push origin mu/ship-rv)",
		"(cd " + primary + "; : ${x:-)}; git add f)",
		"(cd " + primary + `; : "${x:-")"}"; git add f)`,
		"(cd " + primary + "; case y in y) ${x:-esac} ;; x) :;; esac; git add f)",
		"a[1]=x cd " + primary + "; git add f",
		"a[1 2]=x cd " + primary + "; git add f",
		"g() { f; }; f() { cd " + primary + "; }; g; git add f",
		"g() { g2; }; g2() { f; }; f() { cd " + primary + "; }; g; git add f",
		// bash 3.2 passes `{x}` to git as an argument.
		"git push origin mu/ship-rv {x}>/dev/null",
		"(cd " + primary + "; : # )\ngit add f)",
		"f() { cd " + primary + "; }; ${x:-f}; git add f",
		"eval cd " + primary + " && git add f",
		"f() { cd " + primary + "; }; \\f; git add f",
		"f() { cd " + primary + "; }; unset -f f; f; git add f",
		"f() { cd " + primary + "; }; f() { :; }; f; git add f",
		"f() { g() { cd " + primary + "; }; }; g; git add f",
		"(f() { cd " + primary + "; }); f; git add f",
		"g() { cd " + primary + "; }; f() { g() { :; }; }; g; git add f",
		"g() { cd " + primary + "; }; g() { :; } & g; git add f",
		"g() { f; }; f() { :; }; g; f() { cd " + primary + "; }; g; git add f",
		"f() { cd " + primary + "; }; : | f; git add f",
	} {
		if block, _ := runPiSafetyForGit(t, worktree, command); !block {
			t.Errorf("%q allowed, want refused", command)
		}
	}
	n := maxWriteReadings + 144
	var overBudget strings.Builder
	for i := 0; i <= n; i++ {
		if i == 0 {
			fmt.Fprintf(&overBudget, "f%d() { :; }; ", i)
		} else {
			fmt.Fprintf(&overBudget, "f%d() { f%d; }; ", i, i-1)
		}
	}
	fmt.Fprintf(&overBudget, "f%d; git add f", n)
	if block, reason := runPiSafetyForGit(t, worktree, overBudget.String()); !block || !strings.Contains(reason, "cannot be determined") {
		t.Errorf("over-budget inert function chain = block %v, reason %q; want refusal for an unknown directory", block, reason)
	}

	for _, command := range []string{
		"git push origin mu/ship-rv 12>/dev/null",
		"git push origin mu/ship-rv 9>/dev/null",
		"git push origin mu/ship-rv 2>&1",
		"(cd " + primary + "; : # (\n); git add f",
		"(cd " + primary + "; cat <x); git add f",
		"(cd " + primary + "; : ${x:-)}); git add f",
		"g() { g; }; g; git add f",
		"f() { g() { cd " + primary + "; }; }; f; git add f",
	} {
		if block, reason := runPiSafetyForGit(t, worktree, command); block {
			t.Errorf("%q refused: %s", command, reason)
		}
	}
	// At the hook the write guard refuses this too, so the git guard's own
	// bound is asserted at its entry point.
	budget := "eval" + strings.Repeat(" ${x:-a}", 9) + "; git add f"
	if block, reason := evaluateGitMutationSafety(worktree, budget); !block || !strings.Contains(reason, "too many readings") {
		t.Errorf("evaluateGitMutationSafety(%q) = %v %q, want refused for too many readings", budget, block, reason)
	}
}
