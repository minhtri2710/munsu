package taskauthority

import (
	"errors"
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

func TestArchiveRetiredReportRequiresExactActiveClaim(t *testing.T) {
	c, h, _ := newTestCanonical(t)
	id := "archive-task"
	mustCreate(t, c, id)
	retireWithClaim(t, c, id, preconditionOf(1, 1), "op-archive-retire")
	called := false
	if err := c.ArchiveRetiredReport(mustTaskID(t, id), 1, func() error {
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
	if err := c.ArchiveRetiredReport(mustTaskID(t, id), 2, func() error { called = true; return nil }); err == nil || called {
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
			if err := c.ArchiveRetiredReport(mustTaskID(t, id), 1, func() error { called = true; return nil }); err == nil || called {
				t.Fatalf("archive = %v, called=%v", err, called)
			}
		})
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
			mustCreate(t, c, id)
		}, false},
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
