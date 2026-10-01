package taskauthority

import (
	"encoding/json"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// This file owns the Human landing gate record (G1): one immutable, append-only
// evidence document per external delivery write, keyed by Task ID + operation
// identity and committed with its operation receipt through home.Commit. Fleet
// appends the record before it performs the write and refuses the write when
// the append fails. Internal receipts stay receipts; a gate record is the
// Human-facing statement of which exact write was permitted by whose words.
//
// A record does not advance the Task revision: it grants nothing and changes no
// Task state, so the delivery authorization it accompanies stays current. The
// task document is rewritten unchanged only to carry the advanced scope
// revision.

// GateRecord is the immutable gate evidence of one external delivery write:
// the operation (today only the provider merge), the exact head it lands, the
// authorization it accompanies and the Human's words.
type GateRecord struct {
	SchemaVersion            string                    `json:"schema_version"`
	TaskID                   string                    `json:"task_id"`
	Generation               Generation                `json:"generation"`
	AuthorizationOperationID string                    `json:"authorization_operation_id"`
	OperationID              string                    `json:"operation_id"`
	Digest                   string                    `json:"digest"`
	Operation                DeliveryAuthorizationKind `json:"operation"`
	HeadSHA                  string                    `json:"head_sha"`
	Words                    domain.Words              `json:"words"`
	RecordedAt               int64                     `json:"recorded_at"`
}

func gateKey(taskID, opID string) string {
	return "task-authority/gates/" + taskID + "/" + opID + ".json"
}

// CanonicalRecordGateRequest is the typed intent for appending one gate record.
type CanonicalRecordGateRequest struct {
	HomeID                   domain.HomeID
	TaskID                   domain.TaskID
	Precondition             domain.Precondition
	AuthorizationOperationID string
	HeadSHA                  string
	Words                    domain.Words
}

func (r CanonicalRecordGateRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID                   string       `json:"home_id"`
		TaskID                   string       `json:"task_id"`
		Generation               uint64       `json:"generation"`
		Revision                 uint64       `json:"revision"`
		AuthorizationOperationID string       `json:"authorization_operation_id"`
		HeadSHA                  string       `json:"head_sha"`
		Words                    domain.Words `json:"words"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.AuthorizationOperationID, r.HeadSHA, r.Words})
}

// validateGateRecord checks the persisted record shape.
func validateGateRecord(g GateRecord) error {
	if g.SchemaVersion != TaskAuthoritySchema {
		return validationError("invalid gate record schema %q", g.SchemaVersion)
	}
	if g.TaskID == "" || strings.ContainsAny(g.TaskID, `/\\`) {
		return validationError("gate record missing safe task id")
	}
	if err := g.Generation.Validate(); err != nil {
		return err
	}
	if !safeIdentityValue(g.AuthorizationOperationID) || !safeIdentityValue(g.OperationID) {
		return validationError("gate record missing safe operation identities")
	}
	if !domain.IsSHA256(g.Digest) {
		return validationError("gate record digest must be a 64-hex sha256 digest")
	}
	if !g.Operation.Valid() {
		return validationError("gate record has invalid operation %q", g.Operation)
	}
	if !safeSHAValue(g.HeadSHA) {
		return validationError("gate record head SHA must be a safe non-empty value")
	}
	if err := g.Words.Validate(); err != nil {
		return validationError("gate record: %v", err)
	}
	if g.RecordedAt <= 0 {
		return validationError("gate record missing recorded timestamp")
	}
	return nil
}

func (c *Canonical) readGateRecord(taskID, opID string) (GateRecord, bool, error) {
	data, ok, err := c.readDoc(gateKey(taskID, opID))
	if err != nil || !ok {
		return GateRecord{}, ok, err
	}
	var g GateRecord
	if err := json.Unmarshal(data, &g); err != nil {
		return GateRecord{}, true, internalError("decode gate record %s for task %s: %v", opID, taskID, err)
	}
	if err := validateGateRecord(g); err != nil {
		return GateRecord{}, true, internalError("task %s has malformed gate record %s: %v", taskID, opID, err)
	}
	if g.TaskID != taskID {
		return GateRecord{}, true, internalError("task %s gate record %s is bound to a different task", taskID, opID)
	}
	return g, true, nil
}

// RecordGate appends the gate record of one external delivery write. It fails
// closed unless the named authorization is the task's current, unrevoked and
// non-terminal one, is still current (no hold, no reservation, no drift), and
// binds exactly the gated head; and unless the Human's words are present. Same
// Operation ID with the same digest replays the durable record; the record is
// never rewritten.
func (c *Canonical) RecordGate(op domain.Operation, req CanonicalRecordGateRequest) (GateRecord, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return GateRecord{}, err
	}
	if err := req.TaskID.Validate(); err != nil {
		return GateRecord{}, err
	}
	if err := req.Precondition.Validate(); err != nil {
		return GateRecord{}, err
	}
	if !safeIdentityValue(req.AuthorizationOperationID) {
		return GateRecord{}, validationError("gate record requires the exact authorization operation identity")
	}
	if !safeSHAValue(req.HeadSHA) {
		return GateRecord{}, validationError("gate record head SHA must be a safe non-empty value")
	}
	if err := req.Words.Validate(); err != nil {
		return GateRecord{}, validationError("gate record: %v", err)
	}

	dispatch, err := c.h.Lock(dispatchScope)
	if err != nil {
		return GateRecord{}, err
	}
	defer dispatch.Release()
	lk, err := c.h.Lock(taskScope(req.TaskID.Value()))
	if err != nil {
		return GateRecord{}, err
	}
	defer lk.Release()

	if _, ok, err := c.checkedReceipt(op); err != nil {
		return GateRecord{}, err
	} else if ok {
		g, found, err := c.readGateRecord(req.TaskID.Value(), op.ID.Value())
		if err != nil {
			return GateRecord{}, err
		}
		if !found {
			return GateRecord{}, internalError("replay of gate record %s cannot reconstruct the committed evidence", op.ID.Value())
		}
		return g, nil
	}

	doc, exists, err := c.readTaskDoc(req.TaskID.Value())
	if err != nil {
		return GateRecord{}, err
	}
	if !exists {
		return GateRecord{}, conflictError(ErrNotFound, "task %s not found", req.TaskID.Value())
	}
	cur := doc.Aggregate
	if err := verifyPrecondition(req.TaskID, cur, req.Precondition); err != nil {
		return GateRecord{}, err
	}
	if err := c.checkMutableCurrent(cur); err != nil {
		return GateRecord{}, err
	}
	if err := c.checkReservationFence(cur, nil); err != nil {
		return GateRecord{}, err
	}
	if err := c.checkCleanupFence(cur, nil); err != nil {
		return GateRecord{}, err
	}
	index, _, err := c.readDeliveryIndex(cur.TaskID)
	if err != nil {
		return GateRecord{}, err
	}
	if index.AuthorizationOpID != req.AuthorizationOperationID {
		return GateRecord{}, conflictError(ErrConflict, "gate record authorization identity mismatch: current %q vs requested %q", index.AuthorizationOpID, req.AuthorizationOperationID)
	}
	revoked, err := c.deliveryRevoked(cur.TaskID, index)
	if err != nil {
		return GateRecord{}, err
	}
	if revoked || index.Terminal {
		return GateRecord{}, conflictError(ErrConflict, "task %s delivery authorization %s is revoked or already terminal; it cannot gate a write", cur.TaskID, req.AuthorizationOperationID)
	}
	auth, ok, err := c.readDeliveryAuthorization(cur.TaskID, index.AuthorizationOpID)
	if err != nil {
		return GateRecord{}, err
	}
	if !ok {
		return GateRecord{}, internalError("task %s delivery index points at missing authorization %s", cur.TaskID, index.AuthorizationOpID)
	}
	if auth.Identity.HeadSHA != req.HeadSHA {
		return GateRecord{}, preconditionError("gate head %q is not the authorized head %q", req.HeadSHA, auth.Identity.HeadSHA)
	}
	holds, err := c.listHolds()
	if err != nil {
		return GateRecord{}, err
	}
	if reasons := c.authorizationCurrencyReasons(cur, auth, holds, deliveryCurrencyRead); len(reasons) > 0 {
		return GateRecord{}, preconditionError("delivery authorization is not current for task %s: %v", cur.TaskID, reasons)
	}

	g := GateRecord{
		SchemaVersion:            TaskAuthoritySchema,
		TaskID:                   cur.TaskID,
		Generation:               cur.Generation,
		AuthorizationOperationID: auth.OperationID,
		OperationID:              op.ID.Value(),
		Digest:                   op.Digest,
		Operation:                auth.Kind,
		HeadSHA:                  req.HeadSHA,
		Words:                    req.Words,
		RecordedAt:               c.now().UnixNano(),
	}
	if err := validateGateRecord(g); err != nil {
		return GateRecord{}, err
	}
	data, err := json.Marshal(g)
	if err != nil {
		return GateRecord{}, err
	}
	// The scope revision advances with every commit and the task document
	// carries it, so the unchanged aggregate is rewritten at the next one.
	docData, err := json.Marshal(taskDoc{HomeRevision: doc.HomeRevision + 1, Aggregate: cur})
	if err != nil {
		return GateRecord{}, err
	}
	recData, err := json.Marshal(receiptFor(op, cur))
	if err != nil {
		return GateRecord{}, err
	}
	items := []home.ChangeItem{
		{Root: canonicalRoot, Key: taskCurrentKey(cur.TaskID), Data: docData},
		{Root: canonicalRoot, Key: gateKey(cur.TaskID, op.ID.Value()), Data: data},
		{Root: canonicalRoot, Key: receiptKey(op.ID.Value()), Data: recData},
	}
	if _, err := c.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return GateRecord{}, commitError(req.TaskID, req.Precondition, err)
	}
	return g, nil
}
