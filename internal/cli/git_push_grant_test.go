package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func recordSafetyPushGrant(t *testing.T, homeDir, taskID, head string) {
	t.Helper()
	auth := testAuthorityFor(t, homeDir)
	tid := mustTaskIDFor(t, taskID)
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalRecordPushGrantRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		HeadSHA:      head,
		Words:        domain.Words{Grantor: "Human", Channel: "supervisor-relay:typed", Quote: "a"},
	}
	op, err := newCanonicalOperation("push-grant", req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RecordPushGrant(op, req); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateGitArgvSafetyRequiresExactPushGrantForBothRemotes(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-grant", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-grant")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-grant")
	runGitForSafety(t, worktree, "config", "remote.no-mistakes.url", primary)
	runGitForSafety(t, worktree, "config", "remote.no-mistakes.push", "HEAD:refs/heads/mu/ship-grant")
	head := gitOutputForSafety(t, worktree, "rev-parse", "HEAD")

	for _, tc := range []struct {
		name string
		argv []string
	}{
		{name: "origin", argv: []string{"push", "origin", "HEAD:refs/heads/mu/ship-grant"}},
		{name: "no-mistakes", argv: []string{"push", "no-mistakes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocked, reason := evaluateGitArgvSafety(worktree, tc.argv)
			if !blocked || !strings.Contains(reason, tc.name) || !strings.Contains(reason, "ship-grant") ||
				!strings.Contains(reason, head) || !strings.Contains(reason, "report needs-decision") {
				t.Fatalf("push without grant to %s = blocked %v, reason %q; want remote, task, exact head and recovery", tc.name, blocked, reason)
			}
		})
	}

	recordSafetyPushGrant(t, homeDir, "ship-grant", head)
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{name: "origin", argv: []string{"push", "origin", "HEAD:refs/heads/mu/ship-grant"}},
		{name: "no-mistakes", argv: []string{"push", "no-mistakes"}},
	} {
		if blocked, reason := evaluateGitArgvSafety(worktree, tc.argv); blocked {
			t.Fatalf("push with matching grant to %s blocked: %s", tc.name, reason)
		}
	}

}

func TestEvaluateGitArgvSafetyRequiresTaskLocalNoMistakesPushMapping(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-gate-config", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-gate-config")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-gate-config")
	argv := []string{"push", "no-mistakes"}

	blocked, reason := evaluateGitArgvSafety(worktree, argv)
	if !blocked || !strings.Contains(reason, "remote.no-mistakes.url is not configured") {
		t.Fatalf("gate push without gate remote = blocked %v, reason %q; want configured-remote refusal", blocked, reason)
	}
	runGitForSafety(t, worktree, "config", "remote.no-mistakes.url", primary)
	blocked, reason = evaluateGitArgvSafety(worktree, argv)
	if !blocked || !strings.Contains(reason, "remote.no-mistakes.push must explicitly name the task branch") {
		t.Fatalf("gate push without push mapping = blocked %v, reason %q; want explicit-mapping refusal", blocked, reason)
	}
	runGitForSafety(t, worktree, "config", "remote.no-mistakes.push", "HEAD:refs/heads/mu/other-task")
	blocked, reason = evaluateGitArgvSafety(worktree, argv)
	if !blocked || !strings.Contains(reason, "remote.no-mistakes.push must name only the current task branch") {
		t.Fatalf("gate push to another task branch = blocked %v, reason %q; want task-local mapping refusal", blocked, reason)
	}
	runGitForSafety(t, worktree, "config", "--add", "remote.no-mistakes.push", "HEAD:refs/heads/mu/ship-gate-config")
	blocked, reason = evaluateGitArgvSafety(worktree, argv)
	if !blocked || !strings.Contains(reason, "remote.no-mistakes.push must name only the current task branch") {
		t.Fatalf("gate push with multiple mappings = blocked %v, reason %q; want single-mapping refusal", blocked, reason)
	}
}

func TestEvaluateGitArgvSafetyRejectsPushGrantForDifferentHead(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := t.TempDir() + "/wt"
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-stale-grant", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-stale-grant")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-stale-grant")
	oldHead := gitOutputForSafety(t, worktree, "rev-parse", "HEAD")
	recordSafetyPushGrant(t, homeDir, "ship-stale-grant", oldHead)

	osWriteSafetyFile(t, worktree, "new.txt", "new commit\n")
	runGitForSafety(t, worktree, "add", "new.txt")
	runGitForSafety(t, worktree, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "new commit")
	newHead := gitOutputForSafety(t, worktree, "rev-parse", "HEAD")

	blocked, reason := evaluateGitArgvSafety(worktree, []string{"push", "origin", "HEAD:refs/heads/mu/ship-stale-grant"})
	if !blocked || !strings.Contains(reason, newHead) {
		t.Fatalf("push of new commit = blocked %v, reason %q; want refusal naming %s", blocked, reason, newHead)
	}
}

func TestEvaluateGitArgvSafetyRejectsPushGrantFromPriorGeneration(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-reopened-grant", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-reopened-grant")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-reopened-grant")
	head := gitOutputForSafety(t, worktree, "rev-parse", "HEAD")
	auth := testAuthorityFor(t, homeDir)
	tid := mustTaskIDFor(t, "ship-reopened-grant")
	old, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalRecordPushGrantRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(old.Generation), uint64(old.Revision)),
		HeadSHA:      head, Words: domain.Words{Grantor: "Human", Channel: "supervisor-relay:typed", Quote: "a"},
	}
	op, err := newCanonicalOperation("push-grant", req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RecordPushGrant(op, req); err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	complete := taskauthority.CanonicalCompleteRequest{
		HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		To: taskauthority.PhaseDone, Reason: "complete for new generation test",
	}
	completeOp, err := newCanonicalOperation("task-done", complete)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Complete(completeOp, complete); err != nil {
		t.Fatal(err)
	}
	agg, err = auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	reopen := taskauthority.CanonicalReopenRequest{
		HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Reason: "new generation",
	}
	reopenOp, err := newCanonicalOperation("task-reopen", reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(reopenOp, reopen); err != nil {
		t.Fatal(err)
	}

	agg, err = auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	newBinding := safetyWorktreeBinding(t, primary, worktree, "reopened-lease", "reopened-fence")
	bindReq := taskauthority.CanonicalBindWorktreeRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding:      newBinding, Reason: "bind reopened generation for grant test",
	}
	if _, err := auth.BindWorktree(mustCanonicalOp(t, "safety-rebind-reopened-grant", bindReq), bindReq); err != nil {
		t.Fatal(err)
	}
	blocked, reason := evaluateGitArgvSafety(worktree, []string{"push", "origin", "HEAD:refs/heads/mu/ship-reopened-grant"})
	if !blocked || !strings.Contains(reason, head) {
		t.Fatalf("push under reopened generation = blocked %v, reason %q; want refusal naming %s", blocked, reason, head)
	}
}

func osWriteSafetyFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
