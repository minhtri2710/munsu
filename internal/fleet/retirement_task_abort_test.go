//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// retireWithPendingCleanup builds a failed teardown: the task is retired and
// its cleanup claim stays active.
func retireWithPendingCleanup(t *testing.T, taskID string) (string, *taskauthority.Canonical) {
	t.Helper()
	homeDir := t.TempDir()
	auth := canonicalMergeTestAuth(t, homeDir, taskID)
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatal(err)
	}
	seedWorktreeEvidence(t, auth, taskID, wtDir, "lease-wt-1", "fence-wt-1")
	seedEndpointEvidence(t, auth, taskID, "@1", "lease-ep-1", "fence-ep-1")
	failing := &recordingTeardown{alive: true, disposeErr: errors.New("window busy")}
	if _, err := RetireTask(Options{HomeDir: homeDir, ID: taskID, Force: true}, failing, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	return homeDir, auth
}

// TestAbortReopenThenSpawnPassesBacklogAuthority proves failed teardown ->
// cleanup-abort -> task reopen -> spawn is not refused as a duplicate live
// session.
func TestAbortReopenThenSpawnPassesBacklogAuthority(t *testing.T) {
	taskID := "abort-reopen-spawn"
	homeDir, auth := retireWithPendingCleanup(t, taskID)

	abortCleanupFor(t, auth, homeDir, taskID, taskauthority.Generation(1))
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	reopen := taskauthority.CanonicalReopenRequest{HomeID: auth.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Reason: "reopen"}
	op, err := domain.NewOperation(mustOpID(t, "op-reopen-abort-spawn"), reopen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Reopen(op, reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	r := &Runner{args: Args{ID: taskID, Authority: auth}, homeDir: homeDir}
	if err := r.checkBacklogAuthority(); err != nil {
		t.Fatalf("spawn of the reopened task refused: %v", err)
	}
}
