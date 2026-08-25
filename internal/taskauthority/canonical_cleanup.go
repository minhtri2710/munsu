package taskauthority

import (
	"encoding/json"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
)

// CanonicalBeginCleanupRequest asserts the durable cleanup claim for one
// retirement on the current Task Aggregate. The request is the typed intent
// for the operation digest. ClaimOperationID is the stable retirement
// Operation identity that owns the claim (taskRetireOperationID in fleet) and
// ClaimGeneration is the generation whose cleanup is claimed; together they
// are the continuation capability that lets the cleanup saga mutate a claimed
// task.
type CanonicalBeginCleanupRequest struct {
	HomeID           domain.HomeID
	TaskID           domain.TaskID
	Precondition     domain.Precondition
	ClaimOperationID string
	ClaimGeneration  Generation
	Reason           string
	// ArchiveNameOccupied records, at claim creation and before any archival
	// can run, whether the generation-bound report archive name was already
	// taken. It is the durable ownership proof: a name free when the claim was
	// created and present later was written by this claim.
	ArchiveNameOccupied bool
}

func (r CanonicalBeginCleanupRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID              string     `json:"home_id"`
		TaskID              string     `json:"task_id"`
		Generation          uint64     `json:"generation"`
		Revision            uint64     `json:"revision"`
		ClaimOperationID    string     `json:"claim_operation_id"`
		ClaimGeneration     Generation `json:"claim_generation"`
		Reason              string     `json:"reason,omitempty"`
		ArchiveNameOccupied bool       `json:"archive_name_occupied,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.ClaimOperationID, r.ClaimGeneration, r.Reason, r.ArchiveNameOccupied})
}

// BeginCleanup asserts the durable cleanup claim on the current Aggregate. It
// is the cleanup continuation that closes the window between a fleet
// revalidation (task lock held only for the read) and the external
// backend/filesystem action that follows: while the claim is active, every
// other task-scoped mutation (Reopen/BindEndpoint/BindWorktree/lifecycle/
// delivery) fails closed, so no actor can land a reopen or acquisition in that
// window.
//
// The mutation is exact-generation/phase/evidence fenced: the current
// aggregate must be retired at EXACTLY ClaimGeneration with preserved
// retirement evidence carrying the exact claim identity (or an already-active
// claim under that same identity — the crash-resume no-op). An already-active
// claim with the same owning identity is a no-op (no revision advance); a nil
// claim is set to active ONLY when the preserved retirement evidence matches
// the claim identity. A completed or aborted claim is NEVER re-activated:
// abort is terminal (a teardown retry after an explicit abort does not resume
// the cleanup) and a completed cleanup is never re-run. A claim on a newer
// (reopened) generation, a non-retired phase, a foreign stored identity, or a
// missing/unmatched evidence record all fail closed — an old teardown retry
// can never attach historical cleanup to a reopened generation.
func (c *Canonical) BeginCleanup(op domain.Operation, req CanonicalBeginCleanupRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateClaimIdentity(req.ClaimOperationID, req.ClaimGeneration); err != nil {
		return Outcome{}, err
	}
	return c.mutateTaskCleanup(op, req.TaskID, req.Precondition, cleanupGate{operationID: req.ClaimOperationID, generation: req.ClaimGeneration}, func(cur Aggregate) (Aggregate, error) {
		// The claim can only be asserted for the CURRENT generation while it
		// is retired at exactly the claimed generation. A reopened generation
		// is never claimable by an old teardown retry.
		if cur.Generation != req.ClaimGeneration {
			return Aggregate{}, conflictError(ErrConflict, "task %s is at generation %s; the cleanup claim for generation %s cannot be asserted on a newer generation", cur.TaskID, cur.Generation, req.ClaimGeneration)
		}
		if cur.Phase != PhaseRetired {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s is %s; the cleanup claim requires the retired phase", cur.TaskID, cur.Generation, cur.Phase)
		}
		claim := cur.CleanupClaim
		if claim != nil {
			// The stored claim — in ANY status — must carry the exact request
			// identity; a foreign identity is never overwritten and a foreign
			// continuation is never accepted as a no-op.
			if claim.OperationID != req.ClaimOperationID || claim.Generation != req.ClaimGeneration {
				return Aggregate{}, conflictError(ErrConflict, "task %s generation %s stores a cleanup claim of a different identity (operation %q generation %s); refusing to overwrite", cur.TaskID, cur.Generation, claim.OperationID, claim.Generation)
			}
			if claim.Status == CleanupActive {
				return cur, nil // already claimed by this cleanup under the exact identity: no-op
			}
			// Completed and aborted claims are terminal: abort is never
			// resumed and a completed cleanup is never re-run.
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s cleanup claim is already %s; abort is terminal and cleanup is not re-activated", cur.TaskID, cur.Generation, claim.Status)
		}
		// No stored claim (defensive legacy path): the preserved retirement
		// evidence must carry the exact claim identity before a claim is set.
		if cur.Retirement == nil || cur.Retirement.OperationID != req.ClaimOperationID || cur.Retirement.Generation != req.ClaimGeneration {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s has no preserved retirement evidence matching the claim identity (operation %q generation %s); refusing to claim", cur.TaskID, cur.Generation, req.ClaimOperationID, req.ClaimGeneration)
		}
		next := cur.clone()
		next.CleanupClaim = &CleanupClaim{
			OperationID: req.ClaimOperationID,
			Generation:  req.ClaimGeneration,
			Status:      CleanupActive,
			ClaimedAt:   c.now().UnixNano(),

			ArchiveNameOccupied: req.ArchiveNameOccupied,
		}
		next.Revision++
		return next, nil
	})
}

// CanonicalCompleteCleanupRequest reconciles the active cleanup claim to
// completed after all evidence-pinned releases and projection removal
// succeeded. The request is the typed intent for the operation digest.
type CanonicalCompleteCleanupRequest struct {
	HomeID           domain.HomeID
	TaskID           domain.TaskID
	Precondition     domain.Precondition
	ClaimOperationID string
	ClaimGeneration  Generation
	Reason           string
}

func (r CanonicalCompleteCleanupRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID           string     `json:"home_id"`
		TaskID           string     `json:"task_id"`
		Generation       uint64     `json:"generation"`
		Revision         uint64     `json:"revision"`
		ClaimOperationID string     `json:"claim_operation_id"`
		ClaimGeneration  Generation `json:"claim_generation"`
		Reason           string     `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.ClaimOperationID, r.ClaimGeneration, r.Reason})
}

// CompleteCleanup marks the active cleanup claim completed, releasing the
// task for reopen. It requires the exact continuation identity of the stored
// claim in EVERY path: a nil/foreign claim fails closed, completing an
// already-completed claim is a no-op ONLY under the exact stored identity, and
// completing an aborted claim fails closed (abort is terminal).
func (c *Canonical) CompleteCleanup(op domain.Operation, req CanonicalCompleteCleanupRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateClaimIdentity(req.ClaimOperationID, req.ClaimGeneration); err != nil {
		return Outcome{}, err
	}
	return c.mutateTaskCleanup(op, req.TaskID, req.Precondition, cleanupGate{operationID: req.ClaimOperationID, generation: req.ClaimGeneration}, func(cur Aggregate) (Aggregate, error) {
		claim := cur.CleanupClaim
		if claim == nil {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s has no cleanup claim to complete", cur.TaskID, cur.Generation)
		}
		// Every path is identity-fenced: a foreign identity is never accepted
		// as a no-op and never reconciles the stored claim.
		if claim.OperationID != req.ClaimOperationID || claim.Generation != req.ClaimGeneration {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s stores a cleanup claim of a different identity (operation %q generation %s); refusing to complete", cur.TaskID, cur.Generation, claim.OperationID, claim.Generation)
		}
		if claim.Status == CleanupCompleted {
			return cur, nil // idempotent under the exact identity: already reconciled
		}
		if claim.Status == CleanupAborted {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s cleanup claim is aborted; abort is terminal and cleanup cannot be completed", cur.TaskID, cur.Generation)
		}
		next := cur.clone()
		next.CleanupClaim = &CleanupClaim{
			OperationID:         claim.OperationID,
			Generation:          claim.Generation,
			Status:              CleanupCompleted,
			ClaimedAt:           claim.ClaimedAt,
			ReconciledAt:        c.now().UnixNano(),
			ArchiveNameOccupied: claim.ArchiveNameOccupied,
		}
		next.Revision++
		return next, nil
	})
}

// CanonicalAbortCleanupRequest releases the active cleanup claim WITHOUT
// completing cleanup (operator escape hatch for a stuck claim): the task
// becomes reopenable and the retired generation's preserved evidence remains
// as a historical record. Abort is TERMINAL: a later teardown retry does not
// re-activate the claim, and the aborted cleanup is never resumed against a
// reopened generation. The request is the typed intent for the operation
// digest.
type CanonicalAbortCleanupRequest struct {
	HomeID           domain.HomeID
	TaskID           domain.TaskID
	Precondition     domain.Precondition
	ClaimOperationID string
	ClaimGeneration  Generation
	Reason           string
}

func (r CanonicalAbortCleanupRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID           string     `json:"home_id"`
		TaskID           string     `json:"task_id"`
		Generation       uint64     `json:"generation"`
		Revision         uint64     `json:"revision"`
		ClaimOperationID string     `json:"claim_operation_id"`
		ClaimGeneration  Generation `json:"claim_generation"`
		Reason           string     `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.ClaimOperationID, r.ClaimGeneration, r.Reason})
}

// AbortCleanup marks the active cleanup claim aborted, releasing the task for
// reopen without cleanup completing. Abort is terminal: aborting an
// already-aborted claim is a no-op ONLY under the exact stored identity;
// aborting a completed claim or any foreign identity fails closed.
func (c *Canonical) AbortCleanup(op domain.Operation, req CanonicalAbortCleanupRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateClaimIdentity(req.ClaimOperationID, req.ClaimGeneration); err != nil {
		return Outcome{}, err
	}
	return c.mutateTaskCleanup(op, req.TaskID, req.Precondition, cleanupGate{operationID: req.ClaimOperationID, generation: req.ClaimGeneration}, func(cur Aggregate) (Aggregate, error) {
		claim := cur.CleanupClaim
		if claim == nil {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s has no cleanup claim to abort", cur.TaskID, cur.Generation)
		}
		// Every path is identity-fenced: a foreign identity is never accepted
		// as a no-op and never reconciles the stored claim.
		if claim.OperationID != req.ClaimOperationID || claim.Generation != req.ClaimGeneration {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s stores a cleanup claim of a different identity (operation %q generation %s); refusing to abort", cur.TaskID, cur.Generation, claim.OperationID, claim.Generation)
		}
		if claim.Status == CleanupAborted {
			return cur, nil // idempotent under the exact identity: already aborted
		}
		if claim.Status == CleanupCompleted {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s cleanup claim is completed; a completed cleanup cannot be aborted", cur.TaskID, cur.Generation)
		}
		next := cur.clone()
		next.CleanupClaim = &CleanupClaim{
			OperationID:         claim.OperationID,
			Generation:          claim.Generation,
			Status:              CleanupAborted,
			ClaimedAt:           claim.ClaimedAt,
			ReconciledAt:        c.now().UnixNano(),
			ArchiveNameOccupied: claim.ArchiveNameOccupied,
		}
		next.Revision++
		return next, nil
	})
}

// validateClaimIdentity checks the continuation gate shape: a safe owning
// retirement Operation ID and a valid cleaned generation.
func validateClaimIdentity(operationID string, generation Generation) error {
	if strings.TrimSpace(operationID) == "" || strings.ContainsAny(operationID, `/\\`) {
		return validationError("cleanup continuation requires a safe claim operation id")
	}
	if err := generation.Validate(); err != nil {
		return err
	}
	return nil
}
