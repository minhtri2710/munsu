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

// BeginCleanup asserts the durable cleanup claim on the current Aggregate. It
// is the cleanup continuation that closes the window between a fleet
// revalidation (task lock held only for the read) and the external
// backend/filesystem action that follows: while the claim is active, every
// other task-scoped mutation (Reopen/BindEndpoint/BindWorktree/lifecycle/
// delivery) fails closed, so no actor can land a reopen or acquisition in that
// window.
//
// The mutation is exact-generation/phase fenced: the current aggregate must be
// retired at EXACTLY ClaimGeneration, and checkCleanupFence has already required
// a stored claim to carry the exact request identity. An already-active claim
// is a no-op (the crash-resume case, no revision advance). A completed or
// aborted claim is NEVER re-activated: abort is terminal (a teardown retry
// after an explicit abort does not resume the cleanup) and a completed cleanup
// is never re-run. A claim on a newer (reopened) generation, a non-retired
// phase, or a foreign stored identity all fail closed — an old teardown retry
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
		// Retire commits the cleanup claim atomically with the transition to
		// retired, so a retired generation carries one; Begin only continues an
		// active claim. checkCleanupFence has already required a stored claim,
		// in ANY status, to carry the exact request identity, but it passes a
		// missing claim, which a hand-edited document can leave.
		claim := cur.CleanupClaim
		if claim == nil {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s has no cleanup claim; refusing to claim", cur.TaskID, cur.Generation)
		}
		if claim.Status == CleanupActive {
			return cur, nil // already claimed by this cleanup under the exact identity: no-op
		}
		// Completed and aborted claims are terminal: abort is never resumed and
		// a completed cleanup is never re-run.
		return Aggregate{}, conflictError(ErrConflict, "task %s generation %s cleanup claim is already %s; abort is terminal and cleanup is not re-activated", cur.TaskID, cur.Generation, claim.Status)
	})
}

// CanonicalBeginArtifactDisposalRequest records on the active cleanup claim
// that worktree launch-artifact disposal has begun. The request is the typed
// intent for the operation digest.
type CanonicalBeginArtifactDisposalRequest struct {
	HomeID           domain.HomeID
	TaskID           domain.TaskID
	Precondition     domain.Precondition
	ClaimOperationID string
	ClaimGeneration  Generation
	Reason           string
}

func (r CanonicalBeginArtifactDisposalRequest) DigestBytes() ([]byte, error) {
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

// BeginArtifactDisposal marks the active cleanup claim as disposing the
// retired worktree's launch artifacts. Fleet commits it before removing any
// artifact, so a resume that finds the manifest already removed can tell an
// interrupted disposal from a manifest that was never there. An already-marked
// claim is a no-op; a claim that is not active fails closed.
func (c *Canonical) BeginArtifactDisposal(op domain.Operation, req CanonicalBeginArtifactDisposalRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateClaimIdentity(req.ClaimOperationID, req.ClaimGeneration); err != nil {
		return Outcome{}, err
	}
	return c.mutateTaskCleanup(op, req.TaskID, req.Precondition, cleanupGate{operationID: req.ClaimOperationID, generation: req.ClaimGeneration}, func(cur Aggregate) (Aggregate, error) {
		if cur.CleanupClaim == nil || cur.CleanupClaim.Status != CleanupActive {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s cleanup claim is not active; artifact disposal requires the active claim", cur.TaskID, cur.Generation)
		}
		if cur.CleanupClaim.ArtifactsDisposing {
			return cur, nil
		}
		next := cur.clone()
		next.CleanupClaim.ArtifactsDisposing = true
		next.Revision++
		return next, nil
	})
}

// CanonicalCompleteCleanupRequest describes reconciliation of an active cleanup
// claim to completed after evidence-pinned releases and projection removal
// succeed. The request is the typed intent for the operation digest.
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

// CanonicalAbortCleanupRequest describes reconciliation of an active cleanup
// claim to aborted. The request is the typed intent for the operation digest.
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
