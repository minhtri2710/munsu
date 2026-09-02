package taskauthority

import (
	"encoding/json"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// TransferBoundary is the Task Authority surface that enables a Fleet-owned
// Task Transfer journal (ADR-0008 §3) over two local Task Authority
// interfaces. Fleet orchestrates the ordering and retries; Task Authority
// exposes only the exact-generation, idempotent, home-backed primitives:
//
//   - source reservation/fencing (ReserveTransfer) and source
//     commit/supersession (CommitTransfer) on the source home;
//   - destination generation receipt/creation (ReceiveTransfer) and
//     destination activation/current ownership (ActivateTransfer) on the
//     destination home.
//
// The transfer never copies raw documents and never resolves divergence with
// a source/destination-wins heuristic: every operation is a typed,
// generation/revision-fenced, Operation-ID+digest-idempotent mutation that
// fails closed on a stale precondition or a destination that already owns the
// task. Flight coordination (cross-home ordering, retries, the journal) is
// owned by #413, not here.

// ReservationID is the stable identity of one transfer reservation shared
// between the source and destination homes. It must be a safe non-empty value.
func validateReservationID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\\`) {
		return validationError("transfer reservation ID must be a safe non-empty value")
	}
	return nil
}

// validateFenceToken accepts a safe non-empty fence token value. The token is
// enforced by the common reservation fence, not merely persisted.
func validateFenceToken(token string) error {
	if token == "" || strings.ContainsAny(token, `/\\`) {
		return validationError("transfer reservation fence token must be a safe non-empty value")
	}
	return nil
}

// CanonicalReserveTransferRequest reserves a source task generation for
// transfer to a destination home, fencing it so no further mutation can
// proceed outside the transfer. The request is the typed intent for the
// operation digest.
type CanonicalReserveTransferRequest struct {
	HomeID        domain.HomeID
	TaskID        domain.TaskID
	Precondition  domain.Precondition
	ReservationID string
	Destination   domain.HomeID
	FenceToken    string
	Reason        string
}

func (r CanonicalReserveTransferRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID        string `json:"home_id"`
		TaskID        string `json:"task_id"`
		Generation    uint64 `json:"generation"`
		Revision      uint64 `json:"revision"`
		ReservationID string `json:"reservation_id"`
		Destination   string `json:"destination_home"`
		FenceToken    string `json:"fence_token"`
		Reason        string `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.ReservationID, r.Destination.Value(), r.FenceToken, r.Reason})
}

// ReserveTransfer fences the source task's current generation for transfer to
// a destination home. It is exact-generation and idempotent: the request
// carries the expected Generation/Revision precondition, and reusing the same
// Operation ID with the same digest replays the durable prior outcome. A
// stale precondition, an already-reserved generation, or a same-home
// destination fails closed with a typed conflict.
func (c *Canonical) ReserveTransfer(op domain.Operation, req CanonicalReserveTransferRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateReservationID(req.ReservationID); err != nil {
		return Outcome{}, err
	}
	if req.Destination.Value() == "" || req.Destination == c.homeID {
		return Outcome{}, validationError("transfer destination must be a different home")
	}
	if err := validateFenceToken(req.FenceToken); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Transfer != nil && !cur.Transfer.Transferred {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s is already reserved for transfer", cur.TaskID, cur.Generation)
		}
		next := cur.clone()
		next.Transfer = &TransferState{
			ReservationID:   req.ReservationID,
			DestinationHome: req.Destination.Value(),
			FenceToken:      req.FenceToken,
			ReservedAt:      c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}

// CanonicalCommitTransferRequest confirms the source-side supersession of a
// task generation after the destination has received and activated it. The
// request is the typed intent for the operation digest; it carries the exact
// reservation ID and fence token and the destination-activation evidence bound
// to the reservation.
type CanonicalCommitTransferRequest struct {
	HomeID        domain.HomeID
	TaskID        domain.TaskID
	Precondition  domain.Precondition
	ReservationID string
	FenceToken    string
	Evidence      TransferActivationInfo
	Reason        string
}

func (r CanonicalCommitTransferRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID        string                 `json:"home_id"`
		TaskID        string                 `json:"task_id"`
		Generation    uint64                 `json:"generation"`
		Revision      uint64                 `json:"revision"`
		ReservationID string                 `json:"reservation_id"`
		FenceToken    string                 `json:"fence_token"`
		Evidence      TransferActivationInfo `json:"activation_evidence"`
		Reason        string                 `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.ReservationID, r.FenceToken, r.Evidence, r.Reason})
}

// CommitTransfer supersedes the source task generation after a completed
// transfer: the source generation is marked non-current and its transfer
// state records the committed supersession and the destination-activation
// evidence. It requires the exact reservation (ID + fence token, verified by
// the common mutation fence), is exact-generation and idempotent, and fails
// closed on a stale precondition, a missing/mismatched reservation, a stale
// fence token, or an already-committed transfer. The activation evidence must
// bind the reservation, task, source home/generation, destination
// home/generation, the activation Operation ID, and a stable digest; the
// source is never superseded without that bound evidence.
func (c *Canonical) CommitTransfer(op domain.Operation, req CanonicalCommitTransferRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateReservationID(req.ReservationID); err != nil {
		return Outcome{}, err
	}
	if err := validateFenceToken(req.FenceToken); err != nil {
		return Outcome{}, err
	}
	if err := validateTransferActivation(req.Evidence); err != nil {
		return Outcome{}, err
	}
	if req.Evidence.ReservationID != req.ReservationID {
		return Outcome{}, validationError("activation evidence reservation does not match the commit reservation")
	}
	if req.Evidence.TaskID != req.TaskID.Value() {
		return Outcome{}, validationError("activation evidence task does not match the commit task")
	}
	gate := reservationGate{reservationID: req.ReservationID, fenceToken: req.FenceToken}
	return c.mutateTaskTransfer(op, req.TaskID, req.Precondition, gate, func(cur Aggregate) (Aggregate, error) {
		if cur.Transfer == nil || cur.Transfer.ReservationID != req.ReservationID {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s is not reserved under %s", cur.TaskID, cur.Generation, req.ReservationID)
		}
		if cur.Transfer.Transferred {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s transfer is already committed", cur.TaskID, cur.Generation)
		}
		// The evidence must bind this authority's home and the reserved source
		// generation: the destination activation proof is bound to the exact
		// reservation being committed.
		if req.Evidence.SourceHome != c.homeID.Value() {
			return Aggregate{}, conflictError(ErrConflict, "activation evidence source home %s does not match this authority %s", req.Evidence.SourceHome, c.homeID.Value())
		}
		if req.Evidence.SourceGeneration != cur.Generation {
			return Aggregate{}, conflictError(ErrConflict, "activation evidence source generation %s does not match reserved generation %s", req.Evidence.SourceGeneration, cur.Generation)
		}
		if req.Evidence.DestinationHome != cur.Transfer.DestinationHome {
			return Aggregate{}, conflictError(ErrConflict, "activation evidence destination home %s does not match reservation target %s", req.Evidence.DestinationHome, cur.Transfer.DestinationHome)
		}
		next := cur.clone()
		next.Transfer.Transferred = true
		next.Transfer.Activation = &req.Evidence
		next.Current = false
		next.Revision++
		return next, nil
	})
}

// CanonicalReceiveTransferRequest creates a destination generation for a
// transferred task. The destination re-creates the generation from the typed
// TaskDefinition — never from a raw source document — and must not already
// own the task. The request is the typed intent for the operation digest.
type CanonicalReceiveTransferRequest struct {
	HomeID           domain.HomeID
	TaskID           domain.TaskID
	ReservationID    string
	SourceHome       domain.HomeID
	SourceGeneration Generation
	Definition       TaskDefinition
	DeliveryContract *DeliveryContract
	Reason           string
}

func (r CanonicalReceiveTransferRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID           string            `json:"home_id"`
		TaskID           string            `json:"task_id"`
		ReservationID    string            `json:"reservation_id"`
		SourceHome       string            `json:"source_home"`
		SourceGeneration uint64            `json:"source_generation"`
		Definition       TaskDefinition    `json:"definition"`
		DeliveryContract *DeliveryContract `json:"delivery_contract,omitempty"`
		Reason           string            `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.ReservationID, r.SourceHome.Value(), uint64(r.SourceGeneration), r.Definition, r.DeliveryContract, r.Reason})
}

// ReceiveTransfer creates the destination generation record for a transferred
// task under the given reservation. The destination must not already own the
// task (fail closed — destination truth is never overwritten) and must not
// have ANY existing current or non-current generation history that the receive
// would overwrite or conflict with. The created generation is not yet current;
// ActivateTransfer makes it the owned/current generation. The operation is
// home-backed, idempotent by Operation ID+digest, and crash-recoverable; it
// never copies the source document.
func (c *Canonical) ReceiveTransfer(op domain.Operation, req CanonicalReceiveTransferRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := req.TaskID.Validate(); err != nil {
		return Outcome{}, err
	}
	if err := validateReservationID(req.ReservationID); err != nil {
		return Outcome{}, err
	}
	if req.SourceHome.Value() == "" || req.SourceHome == c.homeID {
		return Outcome{}, validationError("transfer source must be a different home")
	}
	if err := req.SourceGeneration.Validate(); err != nil {
		return Outcome{}, err
	}
	if strings.TrimSpace(req.Definition.Owner) == "" {
		return Outcome{}, validationError("receive requires an owner")
	}

	lk, err := c.h.Lock(taskScope(req.TaskID.Value()))
	if err != nil {
		return Outcome{}, err
	}
	defer lk.Release()

	if rec, ok, err := c.checkedReceipt(op); err != nil {
		return Outcome{}, err
	} else if ok {
		return rec.outcome(), nil
	}

	// The destination must not already own the task or hold ANY generation
	// history for it: a receive must never overwrite or conflict with existing
	// current or non-current destination state. Absence is proven by the task
	// directory holding no current or generation documents at all.
	if exists, err := c.taskHasGenerationDocs(req.TaskID.Value()); err != nil {
		return Outcome{}, err
	} else if exists {
		return Outcome{}, conflictError(ErrConflict, "destination already has task %s history; transfer quarantines and never overwrites destination truth", req.TaskID.Value())
	}

	agg := Aggregate{
		SchemaVersion:    TaskAuthoritySchema,
		TaskID:           req.TaskID.Value(),
		Generation:       Generation(1),
		Revision:         FirstRevision,
		Current:          false,
		Definition:       req.Definition,
		DeliveryContract: req.DeliveryContract,
		Phase:            PhaseQueued,
		Transfer: &TransferState{
			ReservationID:    req.ReservationID,
			SourceHome:       req.SourceHome.Value(),
			SourceGeneration: req.SourceGeneration,
		},
	}
	if err := validateAggregate(agg); err != nil {
		return Outcome{}, err
	}
	doc := taskDoc{HomeRevision: 1, Aggregate: agg}
	rec := receiptFor(op, agg)
	items, err := genItems(req.TaskID.Value(), uint64(agg.Generation), doc, rec)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := c.h.Commit(lk, op.ID.Value(), 0, items); err != nil {
		return Outcome{}, commitError(req.TaskID, domain.Precondition{}, err)
	}
	return outcomeFor(op, agg, false), nil
}

// CanonicalActivateTransferRequest activates a received destination
// generation, making it the current/owned generation. The request is the
// typed intent for the operation digest.
type CanonicalActivateTransferRequest struct {
	HomeID        domain.HomeID
	TaskID        domain.TaskID
	Precondition  domain.Precondition
	ReservationID string
	Reason        string
}

func (r CanonicalActivateTransferRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID        string `json:"home_id"`
		TaskID        string `json:"task_id"`
		Generation    uint64 `json:"generation"`
		Revision      uint64 `json:"revision"`
		ReservationID string `json:"reservation_id"`
		Reason        string `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.ReservationID, r.Reason})
}

// ActivateTransfer makes the received destination generation the
// current/owned generation. It is exact-generation and idempotent: the
// request carries the received generation's expected Generation/Revision
// precondition, and reusing the same Operation ID with the same digest
// replays the durable prior outcome. Activation is unique: activating a
// generation that is already current, or continuing activation of a
// generation that is not the received one, fails closed with a typed
// conflict.
func (c *Canonical) ActivateTransfer(op domain.Operation, req CanonicalActivateTransferRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateReservationID(req.ReservationID); err != nil {
		return Outcome{}, err
	}
	if err := req.Precondition.Validate(); err != nil {
		return Outcome{}, err
	}
	lk, err := c.h.Lock(taskScope(req.TaskID.Value()))
	if err != nil {
		return Outcome{}, err
	}
	defer lk.Release()

	if rec, ok, err := c.checkedReceipt(op); err != nil {
		return Outcome{}, err
	} else if ok {
		return rec.outcome(), nil
	}

	// Activation is unique: if the task already has a current generation, the
	// received generation was already activated under this or another
	// reservation and activation must fail closed.
	if curDoc, exists, err := c.readTaskDoc(req.TaskID.Value()); err != nil {
		return Outcome{}, err
	} else if exists && curDoc.Aggregate.Current {
		return Outcome{}, conflictError(ErrConflict, "task %s generation %s is already current; activation is unique", req.TaskID.Value(), curDoc.Aggregate.Generation)
	}

	doc, exists, err := c.readGenDoc(req.TaskID.Value(), req.Precondition.Generation)
	if err != nil {
		return Outcome{}, err
	}
	if !exists {
		return Outcome{}, conflictError(ErrNotFound, "task %s has no received generation %d", req.TaskID.Value(), req.Precondition.Generation)
	}
	cur := doc.Aggregate
	if cur.Transfer == nil || cur.Transfer.ReservationID != req.ReservationID {
		return Outcome{}, conflictError(ErrConflict, "task %s generation %s is not received under %s", cur.TaskID, cur.Generation, req.ReservationID)
	}
	if err := verifyPrecondition(req.TaskID, cur, req.Precondition); err != nil {
		return Outcome{}, err
	}
	next := cur.clone()
	next.Current = true
	next.Revision++
	rec := receiptFor(op, next)
	docData, err := json.Marshal(taskDoc{HomeRevision: doc.HomeRevision + 1, Aggregate: next})
	if err != nil {
		return Outcome{}, err
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		return Outcome{}, err
	}
	items := []home.ChangeItem{
		{Root: canonicalRoot, Key: taskCurrentKey(req.TaskID.Value()), Data: docData},
		{Root: canonicalRoot, Key: receiptKey(rec.OperationID), Data: recData},
	}
	if _, err := c.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return Outcome{}, commitError(req.TaskID, req.Precondition, err)
	}
	return outcomeFor(op, next, false), nil
}

// genItems builds the change-set that writes a non-current generation document
// and its operation receipt together (used by the destination receive path).
func genItems(taskID string, gen uint64, doc taskDoc, rec receipt) ([]home.ChangeItem, error) {
	docData, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	return []home.ChangeItem{
		{Root: canonicalRoot, Key: taskGenKey(taskID, gen), Data: docData},
		{Root: canonicalRoot, Key: receiptKey(rec.OperationID), Data: recData},
	}, nil
}
