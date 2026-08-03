package taskauthority

// This file owns the scout → ship kind promotion as a named semantic
// operation (Task 7.8). Promote flips a terminal scout Generation to ship
// kind: the kind is an authoritative TaskDefinition field, so the change
// commits inside the Aggregate with the Expected Generation fence
// revalidated in one Store transaction, exactly one Revision advance, one
// typed promotion audit event, and the durable idempotency receipt. The
// operation itself never touches the filesystem; the .meta kind projection
// is reconciled by the caller strictly after the receipt (ADR-0007 §7).

// PromoteRequest is the immutable request payload of one generation-bound
// promotion. It carries the exact Task Generation fence, the stable Task
// Operation identity, the actor, and the reason.
type PromoteRequest struct {
	OperationID string
	Actor       Actor
	TaskID      string
	// ExpectedGeneration is the incarnation fence: promotion only applies to
	// the exact current Generation the calling flow verified.
	ExpectedGeneration Generation
	Reason             string
}

// Promote is the named semantic operation that promotes a terminal scout
// Task Generation to ship kind. In one Store transaction it revalidates the
// Expected Generation fence, enforces the promotion preconditions (the
// current Definition.Kind must be scout and the phase must be done or
// resolved — a live or retired task never promotes), advances the Revision
// by exactly one, commits one typed promotion audit event, and persists the
// durable idempotency receipt. Same-op replay is idempotent and returns the
// original receipt without a second audit or revision; reusing the Operation
// ID with a different intent is a non-retryable conflict; a stale or missing
// task fails closed.
func (a *Authority) Promote(req PromoteRequest) (Result, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return Result{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string `json:"task_id"`
		ExpectedGeneration uint64 `json:"expected_generation"`
		Reason             string `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.Reason})
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
		if cur.Definition.Kind != "scout" {
			return preconditionError("task %s generation %s has kind %q, only scout tasks promote to ship", req.TaskID, cur.Generation, cur.Definition.Kind)
		}
		if cur.Phase != PhaseDone && cur.Phase != PhaseResolved {
			return preconditionError("task %s generation %s is %s, promotion requires a done or resolved scout", req.TaskID, cur.Generation, cur.Phase)
		}
		updated := cur.clone()
		updated.Definition.Kind = "ship"
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditPromote,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			After:       cur.Phase,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return Result{}, err
	}
	return resultFromReceipt(receipt)
}
