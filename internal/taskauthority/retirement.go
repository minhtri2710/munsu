package taskauthority

// This file owns the retired phase transition as a named semantic operation
// (Task 7.7). Retire is the retirement transition after external
// merge/reconciliation: it commits the retired phase, exactly one Revision
// advance, one typed retirement audit event, and the durable idempotency
// receipt in ONE Store transaction. The operation itself never touches the
// filesystem — cleanup stays fleet/saga-side, strictly after the receipt.

// RetireRequest is the immutable request payload of one generation-bound
// retirement. It carries the exact Task Generation fence, the stable Task
// Operation identity, the actor, and the verified prerequisite declaration:
// RequireVerifiedDelivery requires the task's authoritative state to show the
// provider-verified merged/delivered evidence the calling flow verified
// before eligibility (ADR-0004 §4). The Operation ID is excluded from the
// intent digest, so a retry that changes the prerequisite or the reason under
// the same ID detects a conflict.
type RetireRequest struct {
	OperationID string
	Actor       Actor
	TaskID      string
	// ExpectedGeneration is the incarnation fence: retirement only applies to
	// the exact current Generation the calling flow verified.
	ExpectedGeneration Generation
	// RequireVerifiedDelivery requires the task's authoritative state to show
	// provider-verified merged/delivered evidence: a committed merge attempt
	// with a merged-equivalent outcome (merged/already-merged) or a
	// delivered/done delivery terminal. A task that is not in a
	// retired-eligible authoritative state fails closed with a typed
	// precondition error.
	RequireVerifiedDelivery bool
	Reason                  string
}

// Retire is the named semantic operation that transitions a task into the
// retired terminal phase. In one Store transaction it revalidates the
// Expected Generation fence, enforces the verified prerequisites, advances
// the Revision by exactly one, commits one typed retirement audit event, and
// persists the durable idempotency receipt. Same-op replay is idempotent and
// returns the original receipt without a second audit or transition; reusing
// the Operation ID with a different intent is a non-retryable conflict; a
// stale or missing task fails closed; an already-retired generation refuses
// a second transition (the retired phase and its audit event commit exactly
// once).
func (a *Authority) Retire(req RetireRequest) (Result, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return Result{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID                  string `json:"task_id"`
		ExpectedGeneration      uint64 `json:"expected_generation"`
		RequireVerifiedDelivery bool   `json:"require_verified_delivery"`
		Reason                  string `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.RequireVerifiedDelivery, req.Reason})
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
		if cur.Phase == PhaseRetired {
			return conflictError(ErrConflict, "task %s generation %s is already retired", req.TaskID, cur.Generation)
		}
		if req.RequireVerifiedDelivery && !verifiedDeliveryEvidence(cur) {
			return preconditionError("task %s generation %s has no verified merged/delivered evidence; retirement requires a provider-verified merged attempt or delivered/done delivery terminal", req.TaskID, cur.Generation)
		}
		updated := cur.clone()
		updated.Phase = PhaseRetired
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditRetirement,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			Before:      cur.Phase,
			After:       PhaseRetired,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return Result{}, err
	}
	return resultFromReceipt(receipt)
}

// verifiedDeliveryEvidence reports whether the current authoritative state
// shows the provider-verified merged/delivered evidence the fleet retirement
// path requires: a committed merge attempt with a merged-equivalent outcome
// or a delivered/done delivery terminal. Verified merged truth is never
// erased by a later ambiguous read, so the evidence is the committed record.
func verifiedDeliveryEvidence(agg Aggregate) bool {
	if agg.MergeAttempt != nil && mergeOutcomeIsMergedEquiv(agg.MergeAttempt.Outcome) {
		return true
	}
	if agg.DeliveryTerminal != nil {
		switch agg.DeliveryTerminal.Terminal {
		case DeliveryTerminalDelivered, DeliveryTerminalDone:
			return true
		}
	}
	return false
}
