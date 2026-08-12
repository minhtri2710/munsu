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
}

func (r CanonicalBeginCleanupRequest) DigestBytes() ([]byte, error) {
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

// BeginCleanup asserts the durable cleanup claim on the current Aggregate,
// idempotently. It is the cleanup continuation that closes the window between
// a fleet revalidation (task lock held only for the read) and the external
// backend/filesystem action that follows: while the claim is active, every
// other task-scoped mutation (Reopen/BindEndpoint/BindWorktree/lifecycle)
// fails closed, so no actor can land a reopen or acquisition in that window.
//
// The mutation is exact-generation fenced and naturally idempotent: an
// already-active claim with the same owning identity is a no-op (no revision
// advance); a nil or aborted claim is set to active under the same identity,
// so a teardown retry after a crash or an explicit abort re-asserts the claim
// and resumes cleanup. A completed claim is left untouched (cleanup already
// finished). A foreign gate (a different claim identity) fails closed.
func (c *Canonical) BeginCleanup(op domain.Operation, req CanonicalBeginCleanupRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateClaimIdentity(req.ClaimOperationID, req.ClaimGeneration); err != nil {
		return Outcome{}, err
	}
	return c.mutateTaskCleanup(op, req.TaskID, req.Precondition, cleanupGate{operationID: req.ClaimOperationID, generation: req.ClaimGeneration}, func(cur Aggregate) (Aggregate, error) {
		if cur.CleanupClaim != nil && cur.CleanupClaim.OperationID == req.ClaimOperationID && cur.CleanupClaim.Generation == req.ClaimGeneration && cur.CleanupClaim.Status == CleanupActive {
			return cur, nil // already claimed by this cleanup: no-op
		}
		next := cur.clone()
		next.CleanupClaim = &CleanupClaim{
			OperationID: req.ClaimOperationID,
			Generation:  req.ClaimGeneration,
			Status:      CleanupActive,
			ClaimedAt:   c.now().UnixNano(),
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
// task for reopen. It requires the exact continuation gate of the active
// claim; a nil/aborted/foreign claim fails closed (nothing is released).
// Completing an already-completed claim is a no-op.
func (c *Canonical) CompleteCleanup(op domain.Operation, req CanonicalCompleteCleanupRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateClaimIdentity(req.ClaimOperationID, req.ClaimGeneration); err != nil {
		return Outcome{}, err
	}
	return c.mutateTaskCleanup(op, req.TaskID, req.Precondition, cleanupGate{operationID: req.ClaimOperationID, generation: req.ClaimGeneration}, func(cur Aggregate) (Aggregate, error) {
		claim := cur.CleanupClaim
		if claim == nil || claim.Status == CleanupAborted {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s has no active cleanup claim to complete (status %v)", cur.TaskID, cur.Generation, cleanupStatusLabel(claim))
		}
		if claim.Status == CleanupCompleted {
			return cur, nil // idempotent: already reconciled
		}
		next := cur.clone()
		next.CleanupClaim = &CleanupClaim{
			OperationID:  claim.OperationID,
			Generation:   claim.Generation,
			Status:       CleanupCompleted,
			ClaimedAt:    claim.ClaimedAt,
			ReconciledAt: c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}

// CanonicalAbortCleanupRequest releases the active cleanup claim WITHOUT
// completing cleanup (operator escape hatch for a stuck claim): the task
// becomes reopenable, the retired generation's preserved evidence remains as
// a historical record, and a later teardown retry re-activates the claim and
// resumes cleanup. The request is the typed intent for the operation digest.
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
// reopen without cleanup completing. Aborting an already-aborted claim is a
// no-op; aborting a completed claim fails closed (cleanup already finished).
func (c *Canonical) AbortCleanup(op domain.Operation, req CanonicalAbortCleanupRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateClaimIdentity(req.ClaimOperationID, req.ClaimGeneration); err != nil {
		return Outcome{}, err
	}
	return c.mutateTaskCleanup(op, req.TaskID, req.Precondition, cleanupGate{operationID: req.ClaimOperationID, generation: req.ClaimGeneration}, func(cur Aggregate) (Aggregate, error) {
		claim := cur.CleanupClaim
		if claim == nil || claim.Status == CleanupCompleted {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s has no active cleanup claim to abort (status %v)", cur.TaskID, cur.Generation, cleanupStatusLabel(claim))
		}
		if claim.Status == CleanupAborted {
			return cur, nil // idempotent: already aborted
		}
		next := cur.clone()
		next.CleanupClaim = &CleanupClaim{
			OperationID:  claim.OperationID,
			Generation:   claim.Generation,
			Status:       CleanupAborted,
			ClaimedAt:    claim.ClaimedAt,
			ReconciledAt: c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}

// cleanupStatusLabel renders the claim status for diagnostics (nil claim
// included).
func cleanupStatusLabel(claim *CleanupClaim) string {
	if claim == nil {
		return "absent"
	}
	return string(claim.Status)
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
