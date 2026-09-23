//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// retireWithPendingCleanup builds a failed teardown: the task is retired and
// its cleanup claim stays active, with a .meta carrying the gen-1 session.
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
	writeRetireMeta(t, homeDir, taskID, "@1", wtDir)
	if err := home.UpdateMeta(homeDir, taskID, func(meta map[string]string) error {
		meta["herdr_session"] = "sess"
		meta["herdr_pane_id"] = "pane"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	failing := &recordingTeardown{alive: true, disposeErr: errors.New("window busy")}
	if _, err := RetireTask(Options{HomeDir: homeDir, ID: taskID, Force: true}, failing, fakeRetirementJournals{}, auth); err == nil {
		t.Fatal("expected pending cleanup")
	}
	return homeDir, auth
}

// TestAbortReopenThenSpawnPassesBacklogAuthority proves a proven-absent abort
// clears the dead session projection, so failed teardown -> cleanup-abort ->
// task reopen -> spawn is not refused as a duplicate live session.
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

	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"window", "backend", "herdr_session", "herdr_pane_id"} {
		if v, ok := meta[k]; ok {
			t.Errorf("abort kept session key %s=%q", k, v)
		}
	}
	if meta["kind"] != "ship" || meta["worktree"] == "" {
		t.Errorf("abort dropped non-session keys: %v", meta)
	}
	r := &Runner{args: Args{ID: taskID, Authority: auth}, homeDir: homeDir}
	if err := r.checkBacklogAuthority(); err != nil {
		t.Fatalf("spawn of the reopened task refused: %v", err)
	}
}

// TestAbortWithLiveEndpointLeavesMetaUntouched proves an abort that cannot
// prove the endpoint absent touches no session key.
func TestAbortWithLiveEndpointLeavesMetaUntouched(t *testing.T) {
	taskID := "abort-live-meta"
	homeDir, auth := retireWithPendingCleanup(t, taskID)
	before, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := AbortRetirementCleanup(auth, homeDir, &recordingTeardown{alive: true}, mustTaskID(t, taskID), taskauthority.Generation(1)); err == nil {
		t.Fatal("abort with a live endpoint must refuse")
	}
	after, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("refused abort changed meta: before=%v after=%v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("refused abort changed %s: %q -> %q", k, v, after[k])
		}
	}
}

// TestAbortFailsWhenSessionMetaCannotBeCleared proves the abort does not
// release the claim when the dead session projection cannot be removed: the
// claim stays active so the abort can be retried.
func TestAbortFailsWhenSessionMetaCannotBeCleared(t *testing.T) {
	taskID := "abort-meta-unwritable"
	homeDir, auth := retireWithPendingCleanup(t, taskID)
	metaPath, err := home.MetaFilePath(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(metaPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(metaPath, "occupied"), 0755); err != nil {
		t.Fatal(err)
	}
	err = AbortRetirementCleanup(auth, homeDir, fakeTeardown{}, mustTaskID(t, taskID), taskauthority.Generation(1))
	if err == nil || !strings.Contains(err.Error(), "clearing session keys from task meta") {
		t.Fatalf("abort = %v, want session meta clear failure", err)
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != taskauthority.CleanupActive {
		t.Fatalf("failed abort released the claim: %+v", agg.CleanupClaim)
	}
}
