package taskauthority

// TransferRequest is the immutable request payload of one cross-home task
// transfer (ADR-0007 §10). Its digest binds the transfer intent; Operation
// IDs are excluded so a retry that changes the request under the same IDs
// detects a conflict.
type TransferRequest struct {
	SourceHome      string     `json:"source_home"`
	DestinationHome string     `json:"destination_home"`
	TaskID          string     `json:"task_id"`
	Generation      Generation `json:"generation"`
}

// Digest returns the deterministic sha256 request digest binding the
// transfer request.
func (r TransferRequest) Digest() (string, error) {
	return requestDigest(r)
}

// TransferIntent is the durable binding of one cross-home task transfer:
// source and destination home identity, the exact Task Generation, the
// request digest, and the stable Task Operation identities on the source and
// destination sides (ADR-0007 §10). Retry reuses the same intent and
// Operation IDs; the destination receipt replays or conflicts on the request
// digest.
type TransferIntent struct {
	SourceHome             string     `json:"source_home"`
	DestinationHome        string     `json:"destination_home"`
	TaskID                 string     `json:"task_id"`
	Generation             Generation `json:"generation"`
	RequestDigest          string     `json:"request_digest"`
	SourceOperationID      string     `json:"source_operation_id"`
	DestinationOperationID string     `json:"destination_operation_id"`
}

// TransferPayload is the complete Task Generation payload transferred from
// one home to another (ADR-0007 §10): the canonical Aggregate (with its
// bindings and dispatch interpretation binding), the dispatch records
// associated with the generation, and the typed audit history of the
// generation at the source. The receive operation commits the whole payload
// in one Store transaction at the destination.
type TransferPayload struct {
	Aggregate       Aggregate
	Interpretations []DispatchInterpretation
	Decisions       []DispatchDecision
	Holds           []DispatchHold
	History         []AuditEvent
}

// ReceiveTransferRequest carries one destination receive: the durable
// TransferIntent (binding source/destination home identity, Task ID, exact
// Generation, request digest, and both Operation IDs) plus the complete Task
// Generation payload to commit at the destination.
type ReceiveTransferRequest struct {
	Actor   Actor
	Intent  TransferIntent
	Payload TransferPayload
}

// ReceiveTransferResult is the caller-visible outcome of one destination
// receive. Replayed is true when the destination Operation ID was already
// committed with the same request digest and the original receipt was
// returned without re-running the transaction.
type ReceiveTransferResult struct {
	TaskID     string
	Generation Generation
	Replayed   bool
}

// ReceiveTransfer is the named semantic operation that commits the complete
// transferred Task Generation at the destination Authority in one Store
// transaction (ADR-0007 §10). It reuses the intent's destination Operation ID
// and request digest, so a retry replays the original receipt idempotently
// and a changed digest conflicts non-retryably; the destination must not
// already own the task (same or newer Generation fails closed with a typed
// conflict and destination truth is never overwritten). The received
// Aggregate keeps the exact Task Generation but restarts the internal
// Revision at FirstRevision: Revision is the ordering of mutations within one
// generation at one authority, and the destination's mutation history starts
// with this receive. The payload's dispatch records and the generation's
// typed audit history commit in the same transaction, and one typed receive
// audit event is appended under the destination Operation ID.
func (a *Authority) ReceiveTransfer(req ReceiveTransferRequest) (ReceiveTransferResult, error) {
	if err := req.Intent.Validate(); err != nil {
		return ReceiveTransferResult{}, err
	}
	payload := req.Payload
	if payload.Aggregate.TaskID != req.Intent.TaskID {
		return ReceiveTransferResult{}, validationError("receive payload task %s does not match intent task %s", payload.Aggregate.TaskID, req.Intent.TaskID)
	}
	if payload.Aggregate.Generation != req.Intent.Generation {
		return ReceiveTransferResult{}, validationError("receive payload generation %s does not match intent generation %s", payload.Aggregate.Generation, req.Intent.Generation)
	}
	if err := validateAggregate(payload.Aggregate); err != nil {
		return ReceiveTransferResult{}, err
	}
	op, err := a.transferOperation(req)
	if err != nil {
		return ReceiveTransferResult{}, err
	}
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		if existing, ok := tx.Current(req.Intent.TaskID); ok {
			return conflictError(ErrConflict, "destination already owns %s generation %s; transfer quarantines and never overwrites destination truth", req.Intent.TaskID, existing.Generation)
		}
		agg := payload.Aggregate.clone()
		agg.Current = true
		agg.Revision = FirstRevision
		if err := tx.PutAggregate(agg); err != nil {
			return err
		}
		for _, rec := range payload.Interpretations {
			if err := tx.PutInterpretation(rec); err != nil {
				return err
			}
		}
		for _, dec := range payload.Decisions {
			if err := tx.PutDecision(dec); err != nil {
				return err
			}
		}
		for _, hold := range payload.Holds {
			if err := tx.PutHold(hold); err != nil {
				return err
			}
		}
		for _, ev := range payload.History {
			if err := tx.PutAuditRecord(ev); err != nil {
				return err
			}
		}
		return tx.AppendAudit(a.audit(op, agg.TaskID, agg.Generation, "transferred from "+req.Intent.SourceHome, "", agg.Phase))
	})
	if err != nil {
		return ReceiveTransferResult{}, err
	}
	return ReceiveTransferResult{TaskID: receipt.TaskID, Generation: receipt.Generation, Replayed: receipt.Replayed}, nil
}

// transferOperation builds the destination Operation for one receive. Its
// identity is the intent's destination Operation ID and its digest is the
// intent's request digest: replay returns the original receipt and a changed
// request under the same destination Operation ID conflicts non-retryably
// (ADR-0007 §10).
func (a *Authority) transferOperation(req ReceiveTransferRequest) (Operation, error) {
	op := Operation{ID: req.Intent.DestinationOperationID, Digest: req.Intent.RequestDigest, Actor: req.Actor}
	if err := op.Validate(); err != nil {
		return Operation{}, err
	}
	return op, nil
}

// Validate rejects empty or mismatched home identities, unsafe task IDs,
// invalid Generations, malformed request digests, and unsafe Operation IDs.
func (ti TransferIntent) Validate() error {
	if ti.SourceHome == "" || ti.DestinationHome == "" {
		return validationError("transfer intent missing home identity")
	}
	if ti.SourceHome == ti.DestinationHome {
		return validationError("transfer intent source and destination are the same home")
	}
	if err := validateTaskID(ti.TaskID); err != nil {
		return err
	}
	if err := ti.Generation.Validate(); err != nil {
		return err
	}
	if !isSHA256Hex(ti.RequestDigest) {
		return validationError("transfer intent request digest must be a 64-hex sha256 digest")
	}
	if err := validateOperationID(ti.SourceOperationID); err != nil {
		return err
	}
	if err := validateOperationID(ti.DestinationOperationID); err != nil {
		return err
	}
	return nil
}
