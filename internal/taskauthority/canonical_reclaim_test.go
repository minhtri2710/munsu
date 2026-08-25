package taskauthority

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestReclaimReleasedTaskArtifactsOwnershipAndFence(t *testing.T) {
	c, h, _ := newTestCanonical(t)
	id := mustTaskID(t, "fence-task")
	called := false
	reclaimed, err := c.ReclaimReleasedTaskArtifacts(id, func() error {
		called = true
		_, lockErr := h.Lock(taskScope(id.Value()))
		if !errors.Is(lockErr, home.ErrLockTimeout) {
			t.Fatalf("nested task lock error = %v, want ErrLockTimeout", lockErr)
		}
		return nil
	})
	if err != nil || !reclaimed || !called {
		t.Fatalf("unknown reclaim = %v, %v, called=%v", reclaimed, err, called)
	}
}

func TestWriteTaskDataArtifactUsesTaskFence(t *testing.T) {
	c, h, _ := newTestCanonical(t)
	id := mustTaskID(t, "write-fence")
	mustCreate(t, c, id.Value())
	called := false
	if err := c.WriteTaskDataArtifact(id, func() error {
		called = true
		_, err := h.Lock(taskScope(id.Value()))
		if !errors.Is(err, home.ErrLockTimeout) {
			t.Fatalf("lock error = %v", err)
		}
		reclaimedCalled := false
		if reclaimed, reclaimErr := c.ReclaimReleasedTaskArtifacts(id, func() error { reclaimedCalled = true; return nil }); reclaimed || !errors.Is(reclaimErr, home.ErrLockTimeout) || reclaimedCalled {
			t.Fatalf("nested reclaim = %v, %v, called=%v", reclaimed, reclaimErr, reclaimedCalled)
		}
		return nil
	}); err != nil || !called {
		t.Fatalf("write = %v, called=%v", err, called)
	}
	if err := c.WriteTaskDataArtifact(mustTaskID(t, "missing-write"), func() error { return nil }); err != nil {
		t.Fatalf("unknown task write = %v", err)
	}
}

func TestArchiveRetiredReportRequiresExactActiveClaim(t *testing.T) {
	c, h, _ := newTestCanonical(t)
	id := "archive-task"
	mustCreate(t, c, id)
	retireWithClaim(t, c, id, preconditionOf(1, 1), "op-archive-retire")
	called := false
	if err := c.ReconcileRetirementCleanup(mustTaskID(t, id), 1, CleanupCompleted, func(bool) error { return nil }, func(bool) error {
		called = true
		_, err := h.Lock(taskScope(id))
		if !errors.Is(err, home.ErrLockTimeout) {
			t.Fatalf("lock error = %v", err)
		}
		return nil
	}); err != nil || !called {
		t.Fatalf("active archive = %v, called=%v", err, called)
	}
	called = false
	if err := c.ReconcileRetirementCleanup(mustTaskID(t, id), 2, CleanupCompleted, func(bool) error { return nil }, func(bool) error { called = true; return nil }); err == nil || called {
		t.Fatalf("wrong-generation archive = %v, called=%v", err, called)
	}
	for _, status := range []CleanupStatus{CleanupCompleted, CleanupAborted} {
		t.Run(string(status), func(t *testing.T) {
			id := "archive-" + string(status)
			mustCreate(t, c, id)
			retireWithClaim(t, c, id, preconditionOf(1, 1), "op-"+id)
			agg, _ := c.Get(mustTaskID(t, id))
			if status == CleanupCompleted {
				req := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, id), Precondition: preconditionOf(uint64(agg.Generation), uint64(agg.Revision)), ClaimOperationID: "op-" + id, ClaimGeneration: 1, Reason: "done"}
				if _, err := c.CompleteCleanup(mustOperation(t, "complete-"+id, req), req); err != nil {
					t.Fatal(err)
				}
			} else {
				req := CanonicalAbortCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, id), Precondition: preconditionOf(uint64(agg.Generation), uint64(agg.Revision)), ClaimOperationID: "op-" + id, ClaimGeneration: 1, Reason: "abort"}
				if _, err := c.AbortCleanup(mustOperation(t, "abort-"+id, req), req); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			if err := c.ReconcileRetirementCleanup(mustTaskID(t, id), 1, CleanupCompleted, func(bool) error { return nil }, func(bool) error { called = true; return nil }); err == nil || called {
				t.Fatalf("archive = %v, called=%v", err, called)
			}
		})
	}
}

func TestReconcileCompletedCleanupReportsSupersededGeneration(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	id := "completed-superseded"
	mustCreate(t, c, id)
	retireWithClaim(t, c, id, preconditionOf(1, 1), "op-superseded-retire")
	agg, err := c.Get(mustTaskID(t, id))
	if err != nil {
		t.Fatal(err)
	}
	complete := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, id), Precondition: preconditionOf(uint64(agg.Generation), uint64(agg.Revision)), ClaimOperationID: "op-superseded-retire", ClaimGeneration: 1, Reason: "done"}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-superseded-complete", complete), complete); err != nil {
		t.Fatal(err)
	}
	agg, err = c.Get(mustTaskID(t, id))
	if err != nil {
		t.Fatal(err)
	}
	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, id), Precondition: preconditionOf(uint64(agg.Generation), uint64(agg.Revision)), Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-superseded-reopen", reopen), reopen); err != nil {
		t.Fatal(err)
	}
	called := false
	result, err := c.ReconcileCompletedCleanup(mustTaskID(t, id), 1, func() error { called = true; return nil })
	if err != nil || result != CompletedCleanupSuperseded || called {
		t.Fatalf("result=%q err=%v called=%v", result, err, called)
	}
}

func TestReconcileRetirementCleanupPersistsArchiveAttempt(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	id := "archive-attempt"
	mustCreate(t, c, id)
	retireWithClaim(t, c, id, preconditionOf(1, 1), "op-archive-attempt")
	failed := true
	if err := c.ReconcileRetirementCleanup(mustTaskID(t, id), 1, CleanupCompleted, func(bool) error { return nil }, func(attempted bool) error {
		if !attempted {
			t.Fatal("work did not observe committed archive attempt")
		}
		if failed {
			failed = false
			return errors.New("archive failed")
		}
		return nil
	}); err == nil {
		t.Fatal("expected archival failure")
	}
	agg, err := c.Get(mustTaskID(t, id))
	if err != nil || agg.CleanupClaim == nil || !agg.CleanupClaim.ReportArchiveAttempted {
		t.Fatalf("claim = %+v, %v", agg.CleanupClaim, err)
	}
	if err := c.ReconcileRetirementCleanup(mustTaskID(t, id), 1, CleanupCompleted, func(bool) error { return nil }, func(attempted bool) error {
		if !attempted {
			t.Fatal("retry lost archive attempt")
		}
		return nil
	}); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestReconcileCompletedCleanupPostCommitCallbackIsFenced(t *testing.T) {
	c, h, _ := newTestCanonical(t)
	id := "post-commit-fence"
	mustCreate(t, c, id)
	retireWithClaim(t, c, id, preconditionOf(1, 1), "op-post-commit-retire")
	if err := c.ReconcileRetirementCleanup(mustTaskID(t, id), 1, CleanupCompleted, func(bool) error { return nil }, func(bool) error { return nil }, func() error {
		_, err := h.Lock(taskScope(id))
		if !errors.Is(err, home.ErrLockTimeout) {
			t.Fatalf("lock error = %v", err)
		}
		return errors.New("projection cleanup failed")
	}); err == nil {
		t.Fatal("expected post-commit callback error")
	}
	agg, err := c.Get(mustTaskID(t, id))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != CleanupCompleted {
		t.Fatalf("claim = %+v, want completed", agg.CleanupClaim)
	}
}

func retireWithoutClaim(t *testing.T, c *Canonical, taskID string) {
	t.Helper()
	req := retireRequest(t, c, taskID, preconditionOf(1, 1))
	if _, err := c.Retire(mustOperation(t, "op-retire-no-claim-"+taskID, req), req); err != nil {
		t.Fatal(err)
	}
}

func TestReclaimReleasedTaskArtifactsAllowsSupersededTask(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	id := "superseded-reclaim"
	mustCreate(t, c, id)
	mustReserveTransfer(t, c, id, preconditionOf(1, 1), "dest-home")
	commit := commitTransferRequest(t, c, id, preconditionOf(1, 2), "res-"+id, "dest-home")
	if _, err := c.CommitTransfer(mustOperation(t, "op-commit-"+id, commit), commit); err != nil {
		t.Fatal(err)
	}
	called := false
	ok, err := c.ReclaimReleasedTaskArtifacts(mustTaskID(t, id), func() error { called = true; return nil })
	if err != nil || !ok || !called {
		t.Fatalf("reclaim superseded = %v, %v, called=%v", ok, err, called)
	}
}

func TestReclaimReleasedTaskArtifactsUnknownBriefIsKept(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	id := "unknown-brief"
	if err := os.MkdirAll(filepath.Join(c.h.Root(), "data", id), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.h.Root(), "data", id, "brief.md"), []byte("brief"), 0644); err != nil {
		t.Fatal(err)
	}
	called := false
	got, err := c.ReclaimReleasedTaskArtifactsByID(id, func() error { called = true; return nil })
	if err != nil || got || called {
		t.Fatalf("reclaim = %v, err=%v, called=%v", got, err, called)
	}
}

func TestReclaimReleasedTaskArtifactsLifecycleStates(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "working")
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Canonical, string)
		want  bool
	}{
		{"not retired", func(*testing.T, *Canonical, string) {}, false},
		{"active", func(t *testing.T, c *Canonical, id string) {
			retireWithClaim(t, c, id, preconditionOf(1, 1), "op-active-retire")
		}, false},
		{"nil claim", func(t *testing.T, c *Canonical, id string) {
			retireWithoutClaim(t, c, id)
			rewriteTaskDocForTest(t, c, id, func(agg Aggregate) Aggregate { agg.CleanupClaim = nil; return agg })
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := strings.ReplaceAll(tc.name, " ", "-") + "-task"
			mustCreate(t, c, id)
			tc.setup(t, c, id)
			called := false
			got, err := c.ReclaimReleasedTaskArtifacts(mustTaskID(t, id), func() error { called = true; return nil })
			if err != nil || got != tc.want || called != tc.want {
				t.Fatalf("reclaim = %v, err=%v, called=%v, want=%v", got, err, called, tc.want)
			}
		})
	}
}

func TestReclaimReleasedTaskArtifactsTerminalAndCallbackFailure(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	for _, status := range []CleanupStatus{CleanupCompleted, CleanupAborted} {
		id := "terminal-" + string(status)
		mustCreate(t, c, id)
		retireWithClaim(t, c, id, preconditionOf(1, 1), "op-retire-"+id)
		agg, _ := c.Get(mustTaskID(t, id))
		req := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, id), Precondition: preconditionOf(uint64(agg.Generation), uint64(agg.Revision)), ClaimOperationID: "op-retire-" + id, ClaimGeneration: 1, Reason: "complete"}
		if status == CleanupAborted {
			abort := CanonicalAbortCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, id), Precondition: req.Precondition, ClaimOperationID: req.ClaimOperationID, ClaimGeneration: 1, Reason: "abort"}
			if _, err := c.AbortCleanup(mustOperation(t, "op-abort-"+id, abort), abort); err != nil {
				t.Fatal(err)
			}
		} else if _, err := c.CompleteCleanup(mustOperation(t, "op-complete-"+id, req), req); err != nil {
			t.Fatal(err)
		}
		called := false
		got, err := c.ReclaimReleasedTaskArtifacts(mustTaskID(t, id), func() error { called = true; return nil })
		if !got || err != nil || !called {
			t.Fatalf("status %s successful reclaim = %v, %v, called=%v", status, got, err, called)
		}
	}

	mustCreate(t, c, "callback-error")
	retireWithClaim(t, c, "callback-error", preconditionOf(1, 1), "op-callback-error")
	agg, _ := c.Get(mustTaskID(t, "callback-error"))
	complete := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "callback-error"), Precondition: preconditionOf(uint64(agg.Generation), uint64(agg.Revision)), ClaimOperationID: "op-callback-error", ClaimGeneration: 1, Reason: "complete"}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-complete-callback-error", complete), complete); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReclaimReleasedTaskArtifacts(mustTaskID(t, "callback-error"), func() error { return errors.New("reclaim failed") })
	if got || err == nil {
		t.Fatalf("callback failure reclaim = %v, %v", got, err)
	}
}
