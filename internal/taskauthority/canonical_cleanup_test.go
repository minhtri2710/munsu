package taskauthority

import (
	"errors"
	"strings"
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

// TestCanonicalCleanupClaimRejectsLifecycleAndAcquisitionMutations proves the
// durable cleanup claim fails closed for every non-continuation mutation while
// active: Reopen, BindWorktree, BindEndpoint and Block are rejected with a
// typed conflict. A foreign BeginCleanup continuation is also rejected by the
// cleanup fence before it can mutate the active claim.
func TestCanonicalCleanupClaimRejectsLifecycleAndAcquisitionMutations(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	wt := bindWorktreeRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BindWorktree(mustOperation(t, "op-claim-wt", wt), wt); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	ep := bindEndpointRequest(c, "t1", preconditionOf(1, 2))
	if _, err := c.BindEndpoint(mustOperation(t, "op-claim-ep", ep), ep); err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}
	retireWithClaim(t, c, "t1", preconditionOf(1, 3), "op-claim-retire")

	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-claim-reopen", reopen), reopen); !errors.Is(err, ErrConflict) {
		t.Fatalf("Reopen with active claim = %v, want ErrConflict", err)
	}
	wt2 := bindWorktreeRequest(c, "t1", preconditionOf(1, 4))
	if _, err := c.BindWorktree(mustOperation(t, "op-claim-bindwt", wt2), wt2); !errors.Is(err, ErrConflict) {
		t.Fatalf("BindWorktree with active claim = %v, want ErrConflict", err)
	}
	ep2 := bindEndpointRequest(c, "t1", preconditionOf(1, 4))
	if _, err := c.BindEndpoint(mustOperation(t, "op-claim-bindep", ep2), ep2); !errors.Is(err, ErrConflict) {
		t.Fatalf("BindEndpoint with active claim = %v, want ErrConflict", err)
	}
	block := CanonicalBlockRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), Reason: "block"}
	if _, err := c.Block(mustOperation(t, "op-claim-block", block), block); !errors.Is(err, ErrConflict) {
		t.Fatalf("Block with active claim = %v, want ErrConflict", err)
	}
	foreign := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), ClaimOperationID: "op-foreign-retire", ClaimGeneration: 1, Reason: "foreign"}
	if _, err := c.BeginCleanup(mustOperation(t, "op-claim-foreign", foreign), foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign BeginCleanup = %v, want ErrConflict", err)
	}
}

// TestCanonicalBeginCleanupIdempotentNoOp proves BeginCleanup is a natural
// no-op when the claim is already active under the same identity (the normal
// crash-resume retry): the aggregate revision does not advance.
func TestCanonicalBeginCleanupIdempotentNoOp(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	retireWithClaim(t, c, "t1", preconditionOf(1, 1), "op-begin-retire")

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	rev := agg.Revision
	begin := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, uint64(rev)), ClaimOperationID: "op-begin-retire", ClaimGeneration: Generation(1), Reason: "resume"}
	if _, err := c.BeginCleanup(mustOperation(t, "op-begin-again", begin), begin); err != nil {
		t.Fatalf("BeginCleanup: %v", err)
	}
	agg, err = c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != rev {
		t.Fatalf("BeginCleanup no-op advanced revision %d -> %d", rev, agg.Revision)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != CleanupActive {
		t.Fatalf("claim lost: %+v", agg.CleanupClaim)
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
	oldBegin := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(3, 1), ClaimOperationID: "op-terminal-retire2", ClaimGeneration: 2, Reason: "old retry"}
	if _, err := c.BeginCleanup(mustOperation(t, "op-terminal-begin-resume", oldBegin), oldBegin); !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginCleanup of historical claim on reopened generation = %v, want ErrConflict", err)
	}
	agg, err = c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim != nil {
		t.Fatalf("historical claim activated on reopened generation: %+v", agg.CleanupClaim)
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

// TestCanonicalBeginCleanupExactFencing proves BeginCleanup accepts only a
// current aggregate retired at exactly ClaimGeneration with matching
// retirement evidence, and never attaches an old claim to a reopened task.
func TestCanonicalBeginCleanupExactFencing(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	wt := bindWorktreeRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BindWorktree(mustOperation(t, "op-fence-wt", wt), wt); err != nil {
		t.Fatal(err)
	}
	ep := bindEndpointRequest(c, "t1", preconditionOf(1, 2))
	if _, err := c.BindEndpoint(mustOperation(t, "op-fence-ep", ep), ep); err != nil {
		t.Fatal(err)
	}
	retireWithClaim(t, c, "t1", preconditionOf(1, 3), "op-fence-retire")

	foreign := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), ClaimOperationID: "op-foreign", ClaimGeneration: 1, Reason: "x"}
	if _, err := c.BeginCleanup(mustOperation(t, "op-fence-foreign", foreign), foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign BeginCleanup on active claim = %v, want ErrConflict", err)
	}

	reconcileCleanup(t, c, "t1", 1, CleanupCompleted)
	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 5), Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-fence-reopen", reopen), reopen); err != nil {
		t.Fatal(err)
	}
	histBegin := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(2, 1), ClaimOperationID: "op-fence-retire", ClaimGeneration: 1, Reason: "old retry"}
	if _, err := c.BeginCleanup(mustOperation(t, "op-fence-hist", histBegin), histBegin); !errors.Is(err, ErrConflict) {
		t.Fatalf("historical BeginCleanup on reopened generation = %v, want ErrConflict", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim != nil {
		t.Fatalf("historical claim activated on reopened generation: %+v", agg.CleanupClaim)
	}

	c2, _, _ := newTestCanonical(t)
	mustCreate(t, c2, "q")
	queuedBegin := CanonicalBeginCleanupRequest{HomeID: c2.HomeID(), TaskID: mustTaskID(t, "q"), Precondition: preconditionOf(1, 1), ClaimOperationID: "op-q-retire", ClaimGeneration: 1, Reason: "x"}
	if _, err := c2.BeginCleanup(mustOperation(t, "op-fence-queued", queuedBegin), queuedBegin); !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginCleanup on queued task = %v, want ErrConflict", err)
	}

	c3, _, _ := newTestCanonical(t)
	mustCreate(t, c3, "legacy")
	legacyRetire := retireRequest(t, c3, "legacy", preconditionOf(1, 1))
	if _, err := c3.Retire(mustOperation(t, "op-legacy-retire", legacyRetire), legacyRetire); err != nil {
		t.Fatal(err)
	}
	legacyAgg, err := c3.Get(mustTaskID(t, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	if legacyAgg.Retirement != nil {
		t.Fatalf("bindingless retire preserved evidence: %+v", legacyAgg.Retirement)
	}
	noEvidence := CanonicalBeginCleanupRequest{HomeID: c3.HomeID(), TaskID: mustTaskID(t, "legacy"), Precondition: preconditionOf(1, 2), ClaimOperationID: "op-legacy-retire", ClaimGeneration: 1, Reason: "resume"}
	if _, err := c3.BeginCleanup(mustOperation(t, "op-fence-no-evidence", noEvidence), noEvidence); err != nil {
		t.Fatalf("meta-only crash-resume BeginCleanup = %v, want idempotent no-op", err)
	}
	legacyAgg, err = c3.Get(mustTaskID(t, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	if legacyAgg.CleanupClaim == nil || legacyAgg.CleanupClaim.Status != CleanupActive {
		t.Fatalf("meta-only claim not retained: %+v", legacyAgg.CleanupClaim)
	}
}

// TestCanonicalBeginCleanupIdentityFencedAfterReconciliation proves the
// cleanup fence rejects foreign continuation identity on a reconciled claim.
func TestCanonicalBeginCleanupIdentityFencedAfterReconciliation(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	retireWithClaim(t, c, "t1", preconditionOf(1, 1), "op-id-retire")
	reconcileCleanup(t, c, "t1", 1, CleanupCompleted)

	foreign := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 3), ClaimOperationID: "op-foreign", ClaimGeneration: 1, Reason: "x"}
	if _, err := c.BeginCleanup(mustOperation(t, "op-id-foreign-begin", foreign), foreign); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "cleanup claim fence mismatch") {
		t.Fatalf("foreign BeginCleanup on completed claim = %v, want cleanup claim fence mismatch", err)
	}
}
