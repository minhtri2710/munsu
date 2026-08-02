package taskauthority

// AttachAttestationRequest is the immutable request payload of one
// generation-bound delivery-plan and capability-attestation acceptance. It
// carries the exact Task Generation fence, the stable Task Operation
// identity, the bounded delivery plan (requested → effective mode with the
// fallback reason), and the capability-attestation reference (project, home,
// config snapshot digest). The Operation ID is excluded from the intent
// digest by the operation helper, so a retry that changes the plan or the
// reference under the same ID detects a conflict.
type AttachAttestationRequest struct {
	OperationID        string
	Actor              Actor
	TaskID             string
	ExpectedGeneration Generation
	DeliveryPlan       DeliveryPlan
	Attestation        CapabilityAttestation
	Reason             string
}

// AttachAttestationResult is the caller-visible outcome of one attestation
// acceptance. Replayed is true when the Operation ID was already committed
// with the same intent and the original receipt was returned without
// re-running the transaction.
type AttachAttestationResult struct {
	TaskID     string
	Generation Generation
	Revision   Revision
	Phase      Phase
	Replayed   bool
}

// AttachAttestation is the named semantic operation that accepts the
// capability attestation as authoritative evidence: it commits the
// generation-bound delivery plan (requested → effective mode with fallback
// reason) and the capability-attestation reference in one Store transaction
// with the Expected Generation fence revalidated inside the transaction,
// exactly one Revision advance, a typed attestation audit event, and the
// durable idempotency receipt. The transition is bounded: a generation
// accepts one plan, so a second attachment under a fresh Operation ID fails
// closed and the delivery mode can never be mutated again within the
// generation. Repeating the same Operation ID with the same intent replays
// the original receipt; a changed digest is a typed non-retryable conflict; a
// stale or missing task fails closed. A rejected or failed acceptance keeps
// the runtime capability observation outside the Aggregate.
func (a *Authority) AttachAttestation(req AttachAttestationRequest) (AttachAttestationResult, error) {
	if err := req.ExpectedGeneration.Validate(); err != nil {
		return AttachAttestationResult{}, err
	}
	if err := validateAttachAttestationRequest(req); err != nil {
		return AttachAttestationResult{}, err
	}
	op, err := a.operation(req.OperationID, req.Actor, struct {
		TaskID             string                `json:"task_id"`
		ExpectedGeneration uint64                `json:"expected_generation"`
		DeliveryPlan       DeliveryPlan          `json:"delivery_plan"`
		Attestation        CapabilityAttestation `json:"attestation"`
		Reason             string                `json:"reason,omitempty"`
	}{req.TaskID, uint64(req.ExpectedGeneration), req.DeliveryPlan, req.Attestation, req.Reason})
	if err != nil {
		return AttachAttestationResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		cur, ok := tx.Current(req.TaskID)
		if !ok {
			return conflictError(ErrNotFound, "task %s not found", req.TaskID)
		}
		if cur.Generation != req.ExpectedGeneration {
			return conflictError(ErrConflict, "task %s is at generation %s, expected %s", req.TaskID, cur.Generation, req.ExpectedGeneration)
		}
		if cur.DeliveryPlan != nil || cur.CapabilityAttestation != nil {
			return conflictError(ErrConflict, "task %s generation %s already has a delivery plan and attestation; the transition is bounded to one acceptance per generation", req.TaskID, cur.Generation)
		}
		updated := cur.clone()
		plan := req.DeliveryPlan
		ref := req.Attestation
		updated.DeliveryPlan = &plan
		updated.CapabilityAttestation = &ref
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		return tx.AppendAudit(AuditEvent{
			OperationID: op.ID,
			Actor:       op.Actor,
			Kind:        AuditAttestation,
			TaskID:      cur.TaskID,
			Generation:  cur.Generation,
			Reason:      req.Reason,
			At:          a.now().UnixNano(),
		})
	})
	if err != nil {
		return AttachAttestationResult{}, err
	}
	return AttachAttestationResult{
		TaskID:     receipt.TaskID,
		Generation: receipt.Generation,
		Revision:   receipt.Revision,
		Phase:      receipt.Phase,
		Replayed:   receipt.Replayed,
	}, nil
}

// validateAttachAttestationRequest validates the bounded delivery plan and
// the capability-attestation reference of one acceptance request.
func validateAttachAttestationRequest(req AttachAttestationRequest) error {
	if err := validateDeliveryPlan(req.DeliveryPlan); err != nil {
		return err
	}
	if err := validateAttestationReference(req.Attestation); err != nil {
		return err
	}
	return nil
}
