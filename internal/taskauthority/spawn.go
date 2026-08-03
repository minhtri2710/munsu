package taskauthority

import (
	"strings"
)

// ConfirmSpawnRequest confirms one queued task for live work by binding its
// runtime endpoint and transitioning queued → working in one operation.
type ConfirmSpawnRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Binding            EndpointBinding
	Reason             string
}

// ConfirmSpawn is the named semantic operation that persists the Endpoint
// Binding and the queued → working transition atomically after harness
// readiness succeeds. Inside one Store transaction it revalidates the
// Expected Generation fence, owner presence, the generation-bound worktree
// binding (ConfirmSpawn is only valid after BindWorktree), the queued phase,
// the absence of an existing endpoint binding, and applicable durable
// Dispatch Holds for the spawn action before committing the binding, the
// transition, the Revision advance, the typed audit event, and the durable
// idempotency receipt. Repeating the same Operation ID with the same intent
// replays the original receipt; a duplicate Operation ID with a changed
// intent is a typed non-retryable conflict; an already-bound endpoint or a
// non-queued phase fails closed with a typed conflict. A failed transaction
// leaves the task queued with no endpoint binding.
func (a *Authority) ConfirmSpawn(req ConfirmSpawnRequest) (Result, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return Result{}, err
	}
	if err := validateEndpointBinding(req.Binding); err != nil {
		return Result{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string          `json:"task_id"`
		ExpectedGeneration uint64          `json:"expected_generation"`
		Binding            EndpointBinding `json:"binding"`
		Reason             string          `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.Binding, req.Reason})
	if err != nil {
		return Result{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		if strings.TrimSpace(cur.Definition.Owner) == "" {
			return conflictError(ErrPrecondition, "task %s generation %s is not ready to spawn: %s", req.TaskID, cur.Generation, ReadinessMissingOwner)
		}
		if cur.Worktree == nil {
			return conflictError(ErrConflict, "task %s generation %s has no worktree binding; confirm spawn requires a bound worktree", req.TaskID, cur.Generation)
		}
		if cur.Phase != PhaseQueued {
			return conflictError(ErrConflict, "task %s generation %s is %s, confirm spawn requires queued", req.TaskID, cur.Generation, cur.Phase)
		}
		if cur.Endpoint != nil {
			return conflictError(ErrConflict, "task %s generation %s already has an endpoint binding", req.TaskID, cur.Generation)
		}
		if err := checkHolds(tx, DispatchActionSpawn, cur); err != nil {
			return err
		}
		updated := cur.clone()
		updated.Endpoint = &req.Binding
		updated.Phase = PhaseWorking
		updated.PhaseDetail = req.Reason
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(a.audit(op, cur.TaskID, cur.Generation, req.Reason, cur.Phase, PhaseWorking))
	})
	if err != nil {
		return Result{}, err
	}
	return resultFromReceipt(receipt)
}
