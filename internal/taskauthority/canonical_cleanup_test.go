package taskauthority

import (
	"fmt"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// retireWithClaim commits a retirement for t1 at the given precondition and
// asserts the durable active cleanup claim was committed atomically.
func retireWithClaim(t *testing.T, c *Canonical, taskID string, prec domain.Precondition, opID string) {
	t.Helper()
	req := retireRequest(t, c, taskID, prec)
	if _, err := c.Retire(mustOperation(t, opID, req), req); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != CleanupActive || agg.CleanupClaim.Generation != 1 || agg.CleanupClaim.OperationID != opID {
		t.Fatalf("retire did not commit the active cleanup claim: %+v", agg.CleanupClaim)
	}
}

func reconcileCleanup(t *testing.T, c *Canonical, taskID string, generation Generation, terminal CleanupStatus) {
	t.Helper()
	if err := c.ReconcileRetirementCleanup(mustTaskID(t, taskID), generation, terminal, func() error { return nil }); err != nil {
		t.Fatalf("ReconcileRetirementCleanup(%s): %v", terminal, err)
	}
}

// TestCanonicalCleanupTerminalStatesReleaseReopen proves reconciliation makes
// completed and aborted claims terminal and permits reopening after either
// outcome.
func TestCanonicalCleanupTerminalStatesReleaseReopen(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	retireWithClaim(t, c, "t1", preconditionOf(1, 1), "op-terminal-retire")

	reconcileCleanup(t, c, "t1", 1, CleanupCompleted)
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != CleanupCompleted || agg.CleanupClaim.ReconciledAt <= 0 {
		t.Fatalf("claim not completed: %+v", agg.CleanupClaim)
	}
	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, uint64(agg.Revision)), Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-terminal-reopen", reopen), reopen); err != nil {
		t.Fatalf("Reopen after completion: %v", err)
	}

	secondRetire := retireRequest(t, c, "t1", preconditionOf(2, 1))
	if _, err := c.Retire(mustOperation(t, "op-terminal-retire2", secondRetire), secondRetire); err != nil {
		t.Fatalf("Retire generation 2: %v", err)
	}
	reconcileCleanup(t, c, "t1", 2, CleanupAborted)
	agg, err = c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != CleanupAborted || agg.CleanupClaim.ReconciledAt <= 0 {
		t.Fatalf("claim not aborted: %+v", agg.CleanupClaim)
	}
	reopen = CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(2, uint64(agg.Revision)), Reason: "reopen after abort"}
	if _, err := c.Reopen(mustOperation(t, "op-terminal-reopen2", reopen), reopen); err != nil {
		t.Fatalf("Reopen after abort: %v", err)
	}
	agg, err = c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim != nil {
		t.Fatalf("reopened generation leaked a cleanup claim: %+v", agg.CleanupClaim)
	}
}

// TestCanonicalCleanupClaimRequiresCompleteProof proves BeginCleanup refuses
// missing or invalid continuation identities before touching task state.
func TestCanonicalCleanupClaimRequiresCompleteProof(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	retireWithClaim(t, c, "t1", preconditionOf(1, 1), "op-proof-retire")

	for name, op := range map[string]func() error{
		"begin-empty": func() error {
			req := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 2), ClaimOperationID: "", ClaimGeneration: Generation(1), Reason: "x"}
			_, err := c.BeginCleanup(mustOperation(t, "op-proof-begin-empty", req), req)
			return err
		},
		"begin-zero-gen": func() error {
			req := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 2), ClaimOperationID: "op-proof-retire", ClaimGeneration: 0, Reason: "x"}
			_, err := c.BeginCleanup(mustOperation(t, "op-proof-begin-gen", req), req)
			return err
		},
		"begin-missing-task": func() error {
			req := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "missing"), Precondition: preconditionOf(1, 1), ClaimOperationID: "op-proof-retire", ClaimGeneration: Generation(1), Reason: "x"}
			_, err := c.BeginCleanup(mustOperation(t, "op-proof-begin-missing", req), req)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := op(); err == nil {
				t.Fatalf("%s succeeded, want fail closed", name)
			}
		})
	}
}

// The cleanup claim rules: what BeginCleanup requires of the retired
// generation, and what the claim fence refuses while the claim is active or
// after it is reconciled.
func TestCleanupClaimRefusals(t *testing.T) {
	claimOp := "op-claim-retire"
	currentRev := func(t *testing.T, c *Canonical) uint64 {
		agg, err := c.Get(mustTaskID(t, "t1"))
		if err != nil {
			t.Fatal(err)
		}
		return uint64(agg.Revision)
	}
	beginCleanup := func(t *testing.T, c *Canonical, gen, rev uint64, claimOpID string, claimGen Generation) error {
		req := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(gen, rev), ClaimOperationID: claimOpID, ClaimGeneration: claimGen, Reason: "cleanup"}
		_, err := c.BeginCleanup(mustOperation(t, fmt.Sprintf("op-begin-cleanup-%d-%d-%s", gen, rev, claimOpID), req), req)
		return err
	}
	retired := func(t *testing.T, c *Canonical) {
		mustCreate(t, c, "t1")
		retireWithClaim(t, c, "t1", preconditionOf(1, 1), claimOp)
	}
	reopen := func(t *testing.T, c *Canonical) error {
		return stepReopen(t, c, "t1", currentRev(t, c))
	}

	runRefusalRows(t, []refusalRow{
		{name: "begin cleanup refuses a newer generation", class: ErrConflict, want: "cannot be asserted on a newer generation", run: func(t *testing.T, c *Canonical) error {
			retired(t, c)
			reconcileCleanup(t, c, "t1", 1, CleanupCompleted)
			mustStep(t, reopen(t, c))
			return beginCleanup(t, c, 2, 1, claimOp, 1)
		}},
		{name: "begin cleanup refuses a task that is not retired", class: ErrConflict, want: "the cleanup claim requires the retired phase", run: func(t *testing.T, c *Canonical) error {
			mustCreate(t, c, "t1")
			return beginCleanup(t, c, 1, 1, claimOp, 1)
		}},
		{name: "begin cleanup refuses a retired task with no claim", class: ErrConflict, want: "has no cleanup claim; refusing to claim", run: func(t *testing.T, c *Canonical) error {
			retired(t, c)
			rewriteTaskDocForTest(t, c, "t1", func(agg Aggregate) Aggregate { agg.CleanupClaim = nil; return agg })
			return beginCleanup(t, c, 1, currentRev(t, c), claimOp, 1)
		}},
		{name: "begin cleanup refuses an aborted claim", class: ErrConflict, want: "abort is terminal and cleanup is not re-activated", run: func(t *testing.T, c *Canonical) error {
			retired(t, c)
			reconcileCleanup(t, c, "t1", 1, CleanupAborted)
			return beginCleanup(t, c, 1, currentRev(t, c), claimOp, 1)
		}},
		{name: "begin cleanup allows the owning claim and leaves the revision alone", run: func(t *testing.T, c *Canonical) error {
			retired(t, c)
			rev := currentRev(t, c)
			if err := beginCleanup(t, c, 1, rev, claimOp, 1); err != nil {
				return err
			}
			if got := currentRev(t, c); got != rev {
				t.Fatalf("the no-op advanced the revision %d -> %d", rev, got)
			}
			return nil
		}},

		{name: "an active claim refuses an ordinary mutation", class: ErrConflict, want: "has an active cleanup claim", run: func(t *testing.T, c *Canonical) error {
			retired(t, c)
			return reopen(t, c)
		}},
		{name: "an active claim refuses a foreign continuation", class: ErrConflict, want: "the mutation is not a continuation of the active cleanup claim", run: func(t *testing.T, c *Canonical) error {
			retired(t, c)
			return beginCleanup(t, c, 1, currentRev(t, c), "op-foreign-retire", 1)
		}},
		{name: "a reconciled claim refuses a foreign continuation", class: ErrConflict, want: "the continuation identity does not match the stored claim", run: func(t *testing.T, c *Canonical) error {
			retired(t, c)
			reconcileCleanup(t, c, "t1", 1, CleanupCompleted)
			return beginCleanup(t, c, 1, currentRev(t, c), "op-foreign-retire", 1)
		}},
		{name: "a reconciled claim allows an ordinary mutation", run: func(t *testing.T, c *Canonical) error {
			retired(t, c)
			reconcileCleanup(t, c, "t1", 1, CleanupCompleted)
			return reopen(t, c)
		}},
	})
}
