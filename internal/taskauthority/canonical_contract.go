package taskauthority

import (
	"encoding/json"

	"github.com/minhtri2710/munsu/internal/domain"
)

// CanonicalRecordDeliveryContractRequest records the task's durable delivery
// contract: the delivery mode resolved for the launch. Rescaffold is the
// explicit re-scaffold intent (an explicit mode selection on the spawn that
// opens a new generation); it is the ONE sanctioned way a recorded contract
// changes, and it is part of the operation digest so a replay can never
// acquire an intent the original submission did not carry.
type CanonicalRecordDeliveryContractRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Mode         string
	Rescaffold   bool
	Reason       string
}

func (r CanonicalRecordDeliveryContractRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string `json:"home_id"`
		TaskID     string `json:"task_id"`
		Generation uint64 `json:"generation"`
		Revision   uint64 `json:"revision"`
		Mode       string `json:"mode"`
		Rescaffold bool   `json:"rescaffold"`
		Reason     string `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.Mode, r.Rescaffold, r.Reason})
}

// validateRecordDeliveryContractRequest checks the typed request shape: a
// valid task identity and precondition, and a mode inside the authoritative
// delivery mode set. An unknown or empty mode is never recorded.
func validateRecordDeliveryContractRequest(req CanonicalRecordDeliveryContractRequest) error {
	if err := req.TaskID.Validate(); err != nil {
		return err
	}
	if err := req.Precondition.Validate(); err != nil {
		return err
	}
	if !DeliveryModes[req.Mode] {
		return validationError("delivery contract requires a valid delivery mode, got %q", req.Mode)
	}
	return nil
}

// RecordDeliveryContract fixes the task's delivery mode durably on the
// canonical record so later generations READ the contract instead of
// re-resolving the mode on every spawn. It is exact-generation and
// idempotent: reusing the same Operation ID with the same digest replays the
// durable prior outcome, and re-recording the mode already contracted is a
// no-op that never bumps the revision.
//
// A committed contract is never silently overridden: a request carrying a
// DIFFERENT mode without the explicit re-scaffold intent fails closed with a
// typed conflict. Only a re-scaffold (an explicit mode selection on the spawn
// that opens the generation) replaces a recorded contract.
func (c *Canonical) RecordDeliveryContract(op domain.Operation, req CanonicalRecordDeliveryContractRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateRecordDeliveryContractRequest(req); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.DeliveryContract != nil {
			if cur.DeliveryContract.Mode == req.Mode {
				return cur.clone(), nil
			}
			if !req.Rescaffold {
				return Aggregate{}, conflictError(ErrConflict, "task %s generation %s already contracts delivery mode %q; refusing to record %q without an explicit re-scaffold", cur.TaskID, cur.Generation, cur.DeliveryContract.Mode, req.Mode)
			}
		}
		next := cur.clone()
		next.DeliveryContract = &DeliveryContract{
			OperationID: op.ID.Value(),
			Mode:        req.Mode,
			RecordedAt:  c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}
