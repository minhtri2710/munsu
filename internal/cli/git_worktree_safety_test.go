package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
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
}

func TestSafetyCheckGitMutationRefusesPrimaryWrongRepoStaleGenerationLeaseAndHead(t *testing.T) {
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
	// refuses mutations with "stale generation" (Task 8.2 re-expression of
	// the v1 aggregate rewrite).
	if _, err := auth.Complete(taskauthority.CompleteRequest{
		OperationID:        "safety-complete-ship-2",
		Actor:              taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID:             "ship-2",
		ExpectedGeneration: 1,
		To:                 taskauthority.PhaseDone,
		Reason:             "safety test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(taskauthority.ReopenRequest{
		OperationID:        "safety-reopen-ship-2",
		Actor:              taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID:             "ship-2",
		ExpectedGeneration: 1,
		Reason:             "safety test",
	}); err != nil {
		t.Fatal(err)
	}
	block, reason := runPiSafetyForGit(t, worktree, "git add file.txt")
	if !block || !strings.Contains(reason, "stale generation") {
		t.Fatalf("stale generation block=%v reason=%q", block, reason)
	}

	// Bind the reopened generation so the binding and lease checks are
	// reached, and move the worktree onto the task branch so the branch
	// check does not mask them.
	agg, err := auth.Get("ship-2")
	if err != nil {
		t.Fatal(err)
	}
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-2")
	recycled := safetyWorktreeBinding(t, primary, worktree, "recycled-lease", "recycled-fence")
	if _, err := auth.BindWorktree(taskauthority.BindWorktreeRequest{
		OperationID:        "safety-rebind-ship-2",
		Actor:              taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID:             "ship-2",
		ExpectedGeneration: agg.Generation,
		Binding:            recycled,
		Reason:             "safety test",
	}); err != nil {
		t.Fatal(err)
	}
	if block, reason := runPiSafetyForGit(t, worktree, "git add file.txt"); block {
		t.Fatalf("bound reopened generation blocked: %s", reason)
	}

	// Recycled lease: tamper the reopened generation's lease marker so the
	// lease read no longer matches the binding (the v2 aggregate is
	// authoritative; the marker is the home-compatible lease artifact).
	writeSafetyLeaseMarker(t, homeDir, "ship-2", agg.Generation.String(), home.TaskWorktreeBinding{
		TaskGeneration: agg.Generation.String(),
		LeaseID:        recycled.LeaseID,
		FenceToken:     "stale-fence",
	})
	block, reason = runPiSafetyForGit(t, worktree, "git add file.txt")
	if !block || !strings.Contains(reason, "recycled lease") {
		t.Fatalf("recycled lease block=%v reason=%q", block, reason)
	}

	// Restore the marker, then move the recorded head off the actual HEAD:
	// from a detached worktree, checkout of the task branch must fail closed
	// on the recorded base HEAD mismatch (the v2 binding head check).
	writeSafetyLeaseMarker(t, homeDir, "ship-2", agg.Generation.String(), home.TaskWorktreeBinding{
		TaskGeneration: agg.Generation.String(),
		LeaseID:        recycled.LeaseID,
		FenceToken:     recycled.FenceToken,
	})
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

// TestSafetyCheckForceWithLeaseDeniedWithoutAuth verifies that --force-with-lease
// is denied when no git mutation authorization exists.
func TestSafetyCheckForceWithLeaseDeniedWithoutAuth(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-fwl-deny", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-fwl-deny")

	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-fwl-deny")

	block, reason := runPiSafetyForGit(t, worktree, "git push --force-with-lease origin HEAD:refs/heads/mu/ship-fwl-deny")
	if !block || reason == "" {
		t.Fatalf("force-with-lease without auth block=%v reason=%q, want deny", block, reason)
	}
	if !strings.Contains(reason, "not authorized") {
		t.Errorf("expected 'not authorized' in reason, got: %q", reason)
	}
}

// TestSafetyCheckUnrestrictedForceAlwaysDenied verifies that --force and -f
// are always denied regardless of authorization.
func TestSafetyCheckUnrestrictedForceAlwaysDenied(t *testing.T) {
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
	} {
		block, reason := runPiSafetyForGit(t, worktree, command)
		if !block || reason == "" {
			t.Fatalf("%q block=%v reason=%q, want deny", command, block, reason)
		}
		if !strings.Contains(reason, "unrestricted force push") {
			t.Errorf("expected 'unrestricted force push' in reason, got: %q", reason)
		}
	}
}

// TestSafetyCheckBranchDeletionDeniedWithoutContext verifies that branch
// deletion is denied without retirement context.
func TestSafetyCheckBranchDeletionDeniedWithoutContext(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-bdel", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-bdel")

	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-bdel")

	// git branch -d should be denied without retirement context
	block, reason := runPiSafetyForGit(t, worktree, "git branch -d mu/ship-bdel")
	if !block || reason == "" {
		t.Fatalf("branch -d without context block=%v reason=%q, want deny", block, reason)
	}
	if !strings.Contains(reason, "cleanup authority") {
		t.Errorf("expected 'cleanup authority' in reason, got: %q", reason)
	}
}

// TestSafetyCheckRewriteDeniedWithoutContext verifies that rewrite operations
// (rebase, reset, merge) are denied without amendment context.
func TestSafetyCheckRewriteDeniedWithoutContext(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-rewrite", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-rewrite")

	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-rewrite")

	for _, command := range []string{
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
		if !strings.Contains(reason, "rewrite operations") {
			t.Errorf("expected 'rewrite operations' in reason, got: %q", reason)
		}
	}
}

// TestSafetyCheckPushDeleteDeniedWithoutContext verifies that push --delete
// is denied without retirement context.
func TestSafetyCheckPushDeleteDeniedWithoutContext(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-pdel", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-pdel")

	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-pdel")

	// git push --delete should be denied without retirement context
	block, reason := runPiSafetyForGit(t, worktree, "git push --delete origin mu/ship-pdel")
	if !block || reason == "" {
		t.Fatalf("push --delete without context block=%v reason=%q, want deny", block, reason)
	}
	if !strings.Contains(reason, "cleanup authority") {
		t.Errorf("expected 'cleanup authority' in reason, got: %q", reason)
	}
}

// TestSafetyCheckForceWithLeaseAuthorizedWithContext verifies that force-with-lease
// is allowed when authorized via git mutation authorization.
func TestSafetyCheckForceWithLeaseAuthorizedWithContext(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-fwl-auth", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-fwl-auth")

	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-fwl-auth")

	// Seed the .meta projections of the authoritative git authorization
	// records (Task 7.4): the safety read path reads these projections, which
	// production reconciles after each Authority commit.
	seedGitAuthMeta(t, homeDir, "ship-fwl-auth", "rewrite", "amendment", map[string]any{
		"operation": "force-with-lease",
		"expected_state": map[string]string{
			"ref":     "refs/heads/mu/ship-fwl-auth",
			"old_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"new_sha": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
		"authorizer": "amendment",
		"context":    "amendment",
	})

	// Force-with-lease should be allowed
	block, reason := runPiSafetyForGit(t, worktree, "git push --force-with-lease origin HEAD:refs/heads/mu/ship-fwl-auth")
	if block {
		t.Fatalf("force-with-lease with auth blocked=%v reason=%q, want allow", block, reason)
	}
}

// TestSafetyCheckBranchDeletionAllowedWithRetirementContext verifies that branch
// deletion is allowed with retirement context and cleanup tier.
func TestSafetyCheckBranchDeletionAllowedWithRetirementContext(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-bdel-ok", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-bdel-ok")

	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-bdel-ok")

	// Seed the .meta projections of the authoritative git authorization
	// records (Task 7.4).
	seedGitAuthMeta(t, homeDir, "ship-bdel-ok", "cleanup", "retirement", nil)

	// Branch deletion should be allowed in retirement context
	block, reason := runPiSafetyForGit(t, worktree, "git branch -d mu/ship-bdel-ok")
	if block {
		t.Fatalf("branch -d in retirement blocked=%v reason=%q, want allow", block, reason)
	}
}

// TestSafetyCheckRewriteAllowedWithAmendmentContext verifies that rewrite
// operations are allowed with amendment context and rewrite tier.
func TestSafetyCheckRewriteAllowedWithAmendmentContext(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-rewrite-ok", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-rewrite-ok")

	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-rewrite-ok")

	// Seed the .meta projections of the authoritative git authorization
	// records (Task 7.4).
	seedGitAuthMeta(t, homeDir, "ship-rewrite-ok", "rewrite", "amendment", nil)

	// Rebase should be allowed in amendment context
	block, reason := runPiSafetyForGit(t, worktree, "git rebase main")
	if block {
		t.Fatalf("rebase in amendment blocked=%v reason=%q, want allow", block, reason)
	}
}

// TestSafetyCheckPushDeleteAllowedWithRetirementContext verifies that push --delete
// is allowed with retirement context and cleanup tier.
func TestSafetyCheckPushDeleteAllowedWithRetirementContext(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-pdel-ok", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-pdel-ok")

	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-pdel-ok")

	// Seed the .meta projections of the authoritative git authorization
	// records (Task 7.4).
	seedGitAuthMeta(t, homeDir, "ship-pdel-ok", "cleanup", "retirement", nil)

	// Push --delete should be allowed in retirement context
	block, reason := runPiSafetyForGit(t, worktree, "git push --delete origin mu/ship-pdel-ok")
	if block {
		t.Fatalf("push --delete in retirement blocked=%v reason=%q, want allow", block, reason)
	}
}

// seedGitAuthMeta seeds the .meta projections of the authoritative git
// authorization records that production reconciles after each Authority
// commit (Task 7.4). The git mutation safety read path consumes these
// projections; mutationAuth, when non-nil, is the JSON of the
// git_mutation_authorization record.
func seedGitAuthMeta(t *testing.T, homeDir, taskID, tier, context string, mutationAuth map[string]any) {
	t.Helper()
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = make(map[string]string)
	}
	meta["git_capability_tier"] = tier
	meta["git_auth_context"] = context
	if mutationAuth != nil {
		data, err := json.Marshal(mutationAuth)
		if err != nil {
			t.Fatal(err)
		}
		meta["git_mutation_authorization"] = string(data)
	}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatal(err)
	}
}

func bindSafetyWorktree(t *testing.T, taskID, primary, worktree string) string {
	t.Helper()
	homeDir := t.TempDir()
	auth := testAuthorityFor(t, homeDir)
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "safety-create-" + taskID,
		Actor:       taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID:      taskID,
		Owner:       "general",
		Description: "test task",
		Kind:        "ship",
		Project:     "repo",
		Reason:      "safety test",
	}); err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}

	binding := safetyWorktreeBinding(t, primary, worktree, "worktree-lease", "worktree-fence")
	if _, err := auth.BindWorktree(taskauthority.BindWorktreeRequest{
		OperationID:        "safety-bind-" + taskID,
		Actor:              taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID:             taskID,
		ExpectedGeneration: agg.Generation,
		Binding:            binding,
		Reason:             "safety test",
	}); err != nil {
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
// binding head through one raw Store transaction, mirroring how the v1 test
// rewrote the aggregate document.
func setSafetyWorktreeHead(t *testing.T, homeDir, taskID, head string) {
	t.Helper()
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	op := taskauthority.Operation{
		ID:     "safety-set-head-" + taskID,
		Digest: strings.Repeat("a", 64),
		Actor:  taskauthority.Actor{ID: "general", Rank: "general"},
	}
	if _, err := store.Update(op, func(tx *taskauthority.Tx) error {
		cur, ok := tx.Current(taskID)
		if !ok {
			return fmt.Errorf("task %s not found", taskID)
		}
		if cur.Worktree == nil {
			return fmt.Errorf("task %s has no worktree binding", taskID)
		}
		updated := cur
		updated.Revision++
		updated.Worktree = &taskauthority.WorktreeBinding{
			RepositoryIdentity: cur.Worktree.RepositoryIdentity,
			Path:               cur.Worktree.Path,
			GitDir:             cur.Worktree.GitDir,
			CommonDir:          cur.Worktree.CommonDir,
			Head:               head,
			LeaseID:            cur.Worktree.LeaseID,
			FenceToken:         cur.Worktree.FenceToken,
			BoundAtUnix:        cur.Worktree.BoundAtUnix,
		}
		return tx.PutAggregate(updated)
	}); err != nil {
		t.Fatal(err)
	}
}

// writeSafetyLeaseMarker writes one worktree lease marker in the versioned
// v2 namespace using the home-compatible bare format, mirroring what the
// task-authority BindWorktree transaction commits.
func writeSafetyLeaseMarker(t *testing.T, homeDir, taskID, generation string, binding home.TaskWorktreeBinding) {
	t.Helper()
	rel := filepath.Join(homeDir, "state", ".task-authority", "v1", "worktree-leases", taskID, generation, binding.LeaseID+".json")
	if err := os.MkdirAll(filepath.Dir(rel), 0700); err != nil {
		t.Fatal(err)
	}
	marker := struct {
		TaskID         string `json:"task_id"`
		TaskGeneration string `json:"task_generation"`
		LeaseID        string `json:"lease_id"`
		FenceToken     string `json:"fence_token"`
	}{
		TaskID:         taskID,
		TaskGeneration: generation,
		LeaseID:        binding.LeaseID,
		FenceToken:     binding.FenceToken,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rel, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
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
