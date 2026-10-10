package taskauthority

import (
	"encoding/json"

	"github.com/minhtri2710/munsu/internal/domain"
)

// CanonicalRecordDeliveryContractRequest captures the resolved delivery mode
// and the exact review and forge tools used by this task.
type CanonicalRecordDeliveryContractRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Mode         string
	Review       DeliveryStep
	Forge        DeliveryStep
}

func (r CanonicalRecordDeliveryContractRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string       `json:"home_id"`
		TaskID     string       `json:"task_id"`
		Generation uint64       `json:"generation"`
		Revision   uint64       `json:"revision"`
		Mode       string       `json:"mode"`
		Review     DeliveryStep `json:"review"`
		Forge      DeliveryStep `json:"forge"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.Mode, r.Review, r.Forge})
}

// validateRecordDeliveryContractRequest checks the typed request shape: a valid
// task identity and precondition, and a mode inside the authoritative delivery
// mode set. An unknown or empty mode is never recorded.
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
	if err := validateDeliveryStepPair(req.Review, req.Forge); err != nil {
		return err
	}
	if req.Mode != DeliveryModeForSteps(req.Review, req.Forge) {
		return validationError("delivery contract mode disagrees with captured review and forge steps")
	}
	return nil
}

// RecordDeliveryContract records the exact resolved delivery plan for one task
// generation. Replaying the same plan is a no-op; a different plan conflicts.
func (c *Canonical) RecordDeliveryContract(op domain.Operation, req CanonicalRecordDeliveryContractRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateRecordDeliveryContractRequest(req); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.DeliveryContract != nil {
			if cur.DeliveryContract.Matches(req.Mode, req.Review, req.Forge) {
				return cur.clone(), nil
			}
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s already carries a different delivery contract", cur.TaskID, cur.Generation)
		}
		next := cur.clone()
		next.DeliveryContract = &DeliveryContract{
			OperationID: op.ID.Value(),
			Mode:        req.Mode,
			Review:      cloneDeliveryStep(req.Review),
			Forge:       cloneDeliveryStep(req.Forge),
			RecordedAt:  c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}
