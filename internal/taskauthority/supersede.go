package taskauthority

// SupersedeRequest retries a terminal Task Generation as a fresh queued
// Generation.
type SupersedeRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	Reason             string
}

// Supersede is the named semantic operation that retries a terminal task as
// a new queued Generation at Revision 1 and preserves the prior Generation as
// a historical record. It refuses live generations so a retry never claims
// work that is still executing, and the new Generation carries no endpoint or
// worktree bindings: stale ownership stays on the historical record only.
func (a *Authority) Supersede(req SupersedeRequest) (Result, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return Result{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string `json:"task_id"`
		ExpectedGeneration uint64 `json:"expected_generation"`
		Reason             string `json:"reason"`
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
		if !cur.Phase.terminal() {
			return preconditionError("supersede requires terminal task")
		}
		next, err := cur.Generation.Next()
		if err != nil {
			return err
		}
		historical := cur.clone()
		historical.Current = false
		historical.Revision++
		if err := tx.PutAggregate(historical); err != nil {
			return err
		}
		newGen := Aggregate{
			SchemaVersion: TaskAuthoritySchema,
			TaskID:        cur.TaskID,
			Generation:    next,
			Revision:      FirstRevision,
			Current:       true,
			Definition:    cur.Definition,
			Phase:         PhaseQueued,
		}
		if err := tx.PutAggregate(newGen); err != nil {
			return err
		}
		return tx.AppendAudit(a.audit(op, cur.TaskID, next, req.Reason, cur.Phase, PhaseQueued))
	})
	if err != nil {
		return Result{}, err
	}
	return resultFromReceipt(receipt)
}
