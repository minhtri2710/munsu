package taskauthority

import (
	"errors"
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

// TestCanonicalCleanupClaimRejectsLifecycleAndAcquisitionMutations proves the
// durable cleanup claim fails closed for every non-continuation mutation while
// active: Reopen, BindWorktree, BindEndpoint, Block and BeginSpawn are all
// rejected with a typed conflict, so a concurrent actor can never land between
// a fleet revalidation and the destructive action that follows it (BEO-16/P1a).
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
	// A foreign continuation gate (wrong claim operation identity) also fails
	// closed: no other actor can reconcile or bypass the claim.
	complete := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), ClaimOperationID: "op-foreign-retire", ClaimGeneration: Generation(1), Reason: "foreign"}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-claim-foreign", complete), complete); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign CompleteCleanup = %v, want ErrConflict", err)
	}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-claim-foreign-2", complete), complete); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign CompleteCleanup (retry) = %v, want ErrConflict", err)
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

// TestCanonicalCleanupCompleteThenAbortLifecycle proves the full
// completion/abort reconciliation: CompleteCleanup marks the claim completed
// and unblocks Reopen; a fresh retirement's active claim can be aborted to
// unblock Reopen without cleanup completing; and completing an aborted claim
// or aborting a completed claim both fail closed.
func TestCanonicalCleanupCompleteThenAbortLifecycle(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	retireWithClaim(t, c, "t1", preconditionOf(1, 1), "op-life-retire")

	// Complete the claim.
	complete := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 2), ClaimOperationID: "op-life-retire", ClaimGeneration: Generation(1), Reason: "done"}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-life-complete", complete), complete); err != nil {
		t.Fatalf("CompleteCleanup: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != CleanupCompleted || agg.CleanupClaim.ReconciledAt <= 0 {
		t.Fatalf("claim not completed: %+v", agg.CleanupClaim)
	}
	// Completing again is an idempotent no-op (current revision 3).
	completeAgain := complete
	completeAgain.Precondition = preconditionOf(1, 3)
	if _, err := c.CompleteCleanup(mustOperation(t, "op-life-complete-2", completeAgain), completeAgain); err != nil {
		t.Fatalf("re-CompleteCleanup: %v", err)
	}
	// Aborting a completed claim fails closed.
	abort := CanonicalAbortCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 3), ClaimOperationID: "op-life-retire", ClaimGeneration: Generation(1), Reason: "abort"}
	if _, err := c.AbortCleanup(mustOperation(t, "op-life-abort-completed", abort), abort); !errors.Is(err, ErrConflict) {
		t.Fatalf("abort completed claim = %v, want ErrConflict", err)
	}
	// The completed claim unblocks Reopen.
	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 3), Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-life-reopen", reopen), reopen); err != nil {
		t.Fatalf("Reopen after completion: %v", err)
	}

	// A fresh generation retires and is then aborted: the abort unblocks
	// Reopen WITHOUT cleanup completing.
	if _, err := c.Retire(mustOperation(t, "op-life-retire2", retireRequest(t, c, "t1", preconditionOf(2, 1))), retireRequest(t, c, "t1", preconditionOf(2, 1))); err != nil {
		t.Fatalf("Retire gen 2: %v", err)
	}
	abort2 := CanonicalAbortCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(2, 2), ClaimOperationID: "op-life-retire2", ClaimGeneration: Generation(2), Reason: "abort"}
	if _, err := c.AbortCleanup(mustOperation(t, "op-life-abort", abort2), abort2); err != nil {
		t.Fatalf("AbortCleanup: %v", err)
	}
	agg, err = c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != CleanupAborted || agg.CleanupClaim.ReconciledAt <= 0 {
		t.Fatalf("claim not aborted: %+v", agg.CleanupClaim)
	}
	// Completing an aborted claim fails closed (correct identity, but no
	// active claim remains to complete).
	completeAborted := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(2, 3), ClaimOperationID: "op-life-retire2", ClaimGeneration: Generation(2), Reason: "x"}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-life-complete-aborted", completeAborted), completeAborted); !errors.Is(err, ErrConflict) {
		t.Fatalf("complete aborted claim = %v, want ErrConflict", err)
	}
	// The aborted claim unblocks Reopen.
	reopen2 := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(2, 3), Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-life-reopen2", reopen2), reopen2); err != nil {
		t.Fatalf("Reopen after abort: %v", err)
	}
	// Abort is TERMINAL: a teardown retry must never re-activate the aborted
	// claim, and an old cleanup retry must never attach the historical claim
	// to the reopened generation. BeginCleanup on the reopened (generation 3)
	// task with the generation-2 claim identity fails closed — no activation,
	// no overwrite.
	agg, err = c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.CleanupClaim != nil {
		t.Fatalf("reopened generation leaked a cleanup claim: %+v", agg.CleanupClaim)
	}
	begin := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(3, 1), ClaimOperationID: "op-life-retire2", ClaimGeneration: Generation(2), Reason: "old retry"}
	if _, err := c.BeginCleanup(mustOperation(t, "op-life-begin-resume", begin), begin); !errors.Is(err, ErrConflict) {
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

// TestCanonicalCleanupClaimRequiresCompleteProof proves Begin/Complete/Abort
// all fail closed when the request carries a missing or invalid claim identity
// (no continuation capability can be fabricated from thin air).
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
		"complete-stale-precondition": func() error {
			req := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1), ClaimOperationID: "op-proof-retire", ClaimGeneration: Generation(1), Reason: "x"}
			_, err := c.CompleteCleanup(mustOperation(t, "op-proof-complete-stale", req), req)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := op(); err == nil {
				t.Fatalf("%s succeeded, want fail closed", name)
			}
		})
	}
	// A stale precondition must be a typed domain.ErrStalePrecondition.
	req := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1), ClaimOperationID: "op-proof-retire", ClaimGeneration: Generation(1), Reason: "x"}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-proof-stale2", req), req); !errors.Is(err, domain.ErrStalePrecondition) {
		t.Fatalf("stale CompleteCleanup = %v, want domain.ErrStalePrecondition", err)
	}
}

// TestCanonicalBeginCleanupExactFencing proves BeginCleanup accepts only a
// current aggregate retired at exactly ClaimGeneration with the preserved
// retirement evidence matching the claim identity: it fails closed on a newer
// (reopened) generation, on a non-retired phase, on a foreign stored claim
// identity, and on a missing/mismatched evidence record — so an old teardown
// retry can never attach historical cleanup to a reopened generation or
// overwrite a foreign claim (BEO-16/P1a).
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

	// A foreign claim identity can never overwrite the stored active claim.
	foreign := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), ClaimOperationID: "op-foreign", ClaimGeneration: Generation(1), Reason: "x"}
	if _, err := c.BeginCleanup(mustOperation(t, "op-fence-foreign", foreign), foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign BeginCleanup on active claim = %v, want ErrConflict", err)
	}

	// Reconcile the claim to completed, then reopen: the reopened generation
	// (2) is retired fresh and carries its own claim; a historical
	// BeginCleanup for generation 1 must fail closed on the newer generation.
	complete := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 4), ClaimOperationID: "op-fence-retire", ClaimGeneration: Generation(1), Reason: "done"}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-fence-complete", complete), complete); err != nil {
		t.Fatal(err)
	}
	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 5), Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-fence-reopen", reopen), reopen); err != nil {
		t.Fatal(err)
	}
	histBegin := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(2, 1), ClaimOperationID: "op-fence-retire", ClaimGeneration: Generation(1), Reason: "old retry"}
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
	// A historical CompleteCleanup on the reopened generation also fails
	// closed (its gate identity does not match the current state).
	histComplete := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(2, 1), ClaimOperationID: "op-fence-retire", ClaimGeneration: Generation(1), Reason: "x"}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-fence-hist-complete", histComplete), histComplete); !errors.Is(err, ErrConflict) {
		t.Fatalf("historical CompleteCleanup on reopened generation = %v, want ErrConflict", err)
	}

	// A queued (non-retired) task can never be claimed.
	c2, _, _ := newTestCanonical(t)
	mustCreate(t, c2, "q")
	queuedBegin := CanonicalBeginCleanupRequest{HomeID: c2.HomeID(), TaskID: mustTaskID(t, "q"), Precondition: preconditionOf(1, 1), ClaimOperationID: "op-q-retire", ClaimGeneration: Generation(1), Reason: "x"}
	if _, err := c2.BeginCleanup(mustOperation(t, "op-fence-queued", queuedBegin), queuedBegin); !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginCleanup on queued task = %v, want ErrConflict", err)
	}

	// A bindingless (meta-only) retired generation with an ACTIVE claim under
	// the exact identity no-ops on BeginCleanup — its crash-resume retry works
	// even though it preserved no resource evidence (the claim itself is the
	// authority; the evidence identity check guards only the from-scratch
	// claim-set path).
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
	noEvidence := CanonicalBeginCleanupRequest{HomeID: c3.HomeID(), TaskID: mustTaskID(t, "legacy"), Precondition: preconditionOf(1, 2), ClaimOperationID: "op-legacy-retire", ClaimGeneration: Generation(1), Reason: "resume"}
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

// TestCanonicalCleanupReconciliationIdentityFenced proves every reconciliation
// idempotency path requires the EXACT stored claim identity: a foreign
// CompleteCleanup/AbortCleanup/BeginCleanup on a completed or aborted claim is
// rejected (never a successful no-op), and the same-identity no-ops succeed.
func TestCanonicalCleanupReconciliationIdentityFenced(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	retireWithClaim(t, c, "t1", preconditionOf(1, 1), "op-id-retire")

	// Complete under the exact identity, then attempt foreign continuations.
	complete := CanonicalCompleteCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 2), ClaimOperationID: "op-id-retire", ClaimGeneration: Generation(1), Reason: "done"}
	if _, err := c.CompleteCleanup(mustOperation(t, "op-id-complete", complete), complete); err != nil {
		t.Fatal(err)
	}
	foreignComplete := complete
	foreignComplete.ClaimOperationID = "op-foreign"
	foreignComplete.Precondition = preconditionOf(1, 3)
	if _, err := c.CompleteCleanup(mustOperation(t, "op-id-foreign-complete", foreignComplete), foreignComplete); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign CompleteCleanup on completed claim = %v, want ErrConflict", err)
	}
	foreignAbort := CanonicalAbortCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 3), ClaimOperationID: "op-foreign", ClaimGeneration: Generation(1), Reason: "x"}
	if _, err := c.AbortCleanup(mustOperation(t, "op-id-foreign-abort", foreignAbort), foreignAbort); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign AbortCleanup on completed claim = %v, want ErrConflict", err)
	}
	foreignBegin := CanonicalBeginCleanupRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 3), ClaimOperationID: "op-foreign", ClaimGeneration: Generation(1), Reason: "x"}
	if _, err := c.BeginCleanup(mustOperation(t, "op-id-foreign-begin", foreignBegin), foreignBegin); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign BeginCleanup on completed claim = %v, want ErrConflict", err)
	}
	// The same-identity CompleteCleanup idempotent no-op still succeeds.
	completeAgain := complete
	completeAgain.Precondition = preconditionOf(1, 3)
	if _, err := c.CompleteCleanup(mustOperation(t, "op-id-complete-again", completeAgain), completeAgain); err != nil {
		t.Fatalf("same-identity re-complete = %v, want idempotent no-op", err)
	}

	// Same for the aborted path on a fresh claim.
	c2, _, _ := newTestCanonical(t)
	mustCreate(t, c2, "t2")
	retireWithClaim(t, c2, "t2", preconditionOf(1, 1), "op-id-retire2")
	abort := CanonicalAbortCleanupRequest{HomeID: c2.HomeID(), TaskID: mustTaskID(t, "t2"), Precondition: preconditionOf(1, 2), ClaimOperationID: "op-id-retire2", ClaimGeneration: Generation(1), Reason: "abort"}
	if _, err := c2.AbortCleanup(mustOperation(t, "op-id-abort", abort), abort); err != nil {
		t.Fatal(err)
	}
	foreignAbort2 := abort
	foreignAbort2.ClaimOperationID = "op-foreign"
	foreignAbort2.Precondition = preconditionOf(1, 3)
	if _, err := c2.AbortCleanup(mustOperation(t, "op-id-foreign-abort2", foreignAbort2), foreignAbort2); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign AbortCleanup on aborted claim = %v, want ErrConflict", err)
	}
	foreignComplete2 := CanonicalCompleteCleanupRequest{HomeID: c2.HomeID(), TaskID: mustTaskID(t, "t2"), Precondition: preconditionOf(1, 3), ClaimOperationID: "op-foreign", ClaimGeneration: Generation(1), Reason: "x"}
	if _, err := c2.CompleteCleanup(mustOperation(t, "op-id-foreign-complete2", foreignComplete2), foreignComplete2); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign CompleteCleanup on aborted claim = %v, want ErrConflict", err)
	}
	// The same-identity AbortCleanup idempotent no-op still succeeds.
	abortAgain := abort
	abortAgain.Precondition = preconditionOf(1, 3)
	if _, err := c2.AbortCleanup(mustOperation(t, "op-id-abort-again", abortAgain), abortAgain); err != nil {
		t.Fatalf("same-identity re-abort = %v, want idempotent no-op", err)
	}
}
