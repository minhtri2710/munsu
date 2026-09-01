package taskauthority

import (
	"encoding/json"
	"strings"

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

// CanonicalRecordDeliveryFallbackRequest records the authorized delivery
// transition that moved a contracted task off its recorded mode. From is the
// mode the contract currently states; the op refuses the transition unless it
// matches, so a fallback can never silently overwrite a contract it did not
// read. Reason is the operator-facing evidence for the transition (the
// no-mistakes blocker, or the late capability loss detail) and is part of the
// digest, so a replay can never acquire a reason the original did not carry.
type CanonicalRecordDeliveryFallbackRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	From         string
	To           string
	Reason       string
}

func (r CanonicalRecordDeliveryFallbackRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string `json:"home_id"`
		TaskID     string `json:"task_id"`
		Generation uint64 `json:"generation"`
		Revision   uint64 `json:"revision"`
		From       string `json:"from"`
		To         string `json:"to"`
		Reason     string `json:"reason"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.From, r.To, r.Reason})
}

// validateRecordDeliveryFallbackRequest checks the typed REQUEST shape: a
// valid task identity and precondition, the authorized transition direction,
// and a non-empty reason. A transition with no stated reason is never
// recorded, and a direction ADR-0022 does not authorize is refused at the
// boundary rather than persisted.
//
// It deliberately does not call validateDeliveryFallback: that validator owns
// full well-formedness of a PERSISTED record, including the operation id,
// generation and timestamp this op stamps at commit time. The two share the
// direction rule through validateDeliveryFallbackDirection and nothing else,
// so neither path asserts fields the other does not carry.
func validateRecordDeliveryFallbackRequest(req CanonicalRecordDeliveryFallbackRequest) error {
	if err := req.TaskID.Validate(); err != nil {
		return err
	}
	if err := req.Precondition.Validate(); err != nil {
		return err
	}
	if err := validateDeliveryFallbackDirection(req.From, req.To); err != nil {
		return err
	}
	if strings.TrimSpace(req.Reason) == "" {
		return validationError("delivery fallback requires a reason")
	}
	return nil
}

// RecordDeliveryFallback moves the durable delivery contract to the mode an
// authorized fallback put in force and records the transition that did it, so
// the contract always states the mode in force and how it got there
// (ADR-0022 Decision #2). After it the contract's Mode is the to-mode and its
// Fallback carries from/to/reason/generation; the next generation READS the
// transitioned mode and never falls back again.
//
// It fails closed on a direction it does not authorize and on a contract it
// did not read: only "the authorized no-mistakes -> direct-PR downgrade"
// (ADR-0022 Decision #2) is accepted, a From that does not match the recorded
// mode-in-force is a typed conflict, and a task with no contract at all has
// nothing to transition. Re-recording the SAME transition is an
// idempotent no-op that never bumps the revision, so recovery re-entry can
// never double-record.
func (c *Canonical) RecordDeliveryFallback(op domain.Operation, req CanonicalRecordDeliveryFallbackRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateRecordDeliveryFallbackRequest(req); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.DeliveryContract == nil {
			return Aggregate{}, validationError("task %s generation %s contracts no delivery mode; refusing to record a %q -> %q fallback with no contract to transition", cur.TaskID, cur.Generation, req.From, req.To)
		}
		if fb := cur.DeliveryContract.Fallback; cur.DeliveryContract.Mode == req.To && fb != nil &&
			fb.From == req.From && fb.To == req.To && fb.Reason == req.Reason {
			return cur.clone(), nil
		}
		if cur.DeliveryContract.Mode != req.From {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s contracts delivery mode %q; refusing a fallback transitioning from %q", cur.TaskID, cur.Generation, cur.DeliveryContract.Mode, req.From)
		}
		next := cur.clone()
		next.DeliveryContract.Mode = req.To
		next.DeliveryContract.Fallback = &DeliveryFallback{
			From:        req.From,
			To:          req.To,
			Reason:      req.Reason,
			Generation:  uint64(cur.Generation),
			OperationID: op.ID.Value(),
			RecordedAt:  c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}
