package taskauthority

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// PushGrantRecord is immutable Human-word evidence allowing a Soldier to push
// one exact commit during one Task Generation. It is independent of delivery
// merge authorization and does not advance the Task revision.
type PushGrantRecord struct {
	SchemaVersion string       `json:"schema_version"`
	TaskID        string       `json:"task_id"`
	Generation    Generation   `json:"generation"`
	OperationID   string       `json:"operation_id"`
	Digest        string       `json:"digest"`
	HeadSHA       string       `json:"head_sha"`
	Words         domain.Words `json:"words"`
	RecordedAt    int64        `json:"recorded_at"`
}

// CanonicalRecordPushGrantRequest is the typed intent for one exact-head
// Soldier push grant.
type CanonicalRecordPushGrantRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	HeadSHA      string
	Words        domain.Words
}

func (r CanonicalRecordPushGrantRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID     string       `json:"home_id"`
		TaskID     string       `json:"task_id"`
		Generation uint64       `json:"generation"`
		Revision   uint64       `json:"revision"`
		HeadSHA    string       `json:"head_sha"`
		Words      domain.Words `json:"words"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.HeadSHA, r.Words})
}

func pushGrantDir(taskID string, generation Generation) string {
	return "task-authority/push-grants/" + taskID + "/" + generation.String()
}

func pushGrantKey(taskID string, generation Generation, operationID string) string {
	return pushGrantDir(taskID, generation) + "/" + operationID + ".json"
}

// IsFullGitSHA reports whether value is a full 40-hex Git commit SHA.
func IsFullGitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validatePushGrantRecord(grant PushGrantRecord) error {
	if grant.SchemaVersion != TaskAuthoritySchema {
		return validationError("invalid push grant schema %q", grant.SchemaVersion)
	}
	if err := validateTaskID(grant.TaskID); err != nil {
		return err
	}
	if err := grant.Generation.Validate(); err != nil {
		return err
	}
	if !safeIdentityValue(grant.OperationID) {
		return validationError("push grant requires a safe operation identity")
	}
	if !domain.IsSHA256(grant.Digest) {
		return validationError("push grant digest must be a 64-hex sha256 digest")
	}
	if !IsFullGitSHA(grant.HeadSHA) {
		return validationError("push grant head SHA must be a full 40-hex Git commit SHA")
	}
	if err := grant.Words.Validate(); err != nil {
		return validationError("push grant: %v", err)
	}
	if grant.RecordedAt <= 0 {
		return validationError("push grant missing recorded timestamp")
	}
	return nil
}

func (c *Canonical) readPushGrant(taskID string, generation Generation, operationID string) (PushGrantRecord, bool, error) {
	data, ok, err := c.readDoc(pushGrantKey(taskID, generation, operationID))
	if err != nil || !ok {
		return PushGrantRecord{}, ok, err
	}
	var grant PushGrantRecord
	if err := json.Unmarshal(data, &grant); err != nil {
		return PushGrantRecord{}, true, internalError("decode push grant %s for task %s: %v", operationID, taskID, err)
	}
	if err := validatePushGrantRecord(grant); err != nil {
		return PushGrantRecord{}, true, internalError("task %s has malformed push grant %s: %v", taskID, operationID, err)
	}
	if grant.TaskID != taskID || grant.Generation != generation || grant.OperationID != operationID {
		return PushGrantRecord{}, true, internalError("push grant %s is not bound to task %s generation %s", operationID, taskID, generation)
	}
	return grant, true, nil
}

// RecordPushGrant appends immutable exact-head evidence under the current Task
// Generation. Replays return the original record; a reused Operation ID with
// different intent conflicts.
func (c *Canonical) RecordPushGrant(op domain.Operation, req CanonicalRecordPushGrantRequest) (PushGrantRecord, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return PushGrantRecord{}, err
	}
	if err := req.TaskID.Validate(); err != nil {
		return PushGrantRecord{}, err
	}
	if err := req.Precondition.Validate(); err != nil {
		return PushGrantRecord{}, err
	}
	if !IsFullGitSHA(req.HeadSHA) {
		return PushGrantRecord{}, validationError("push grant head SHA must be a full 40-hex Git commit SHA")
	}
	if err := req.Words.Validate(); err != nil {
		return PushGrantRecord{}, validationError("push grant: %v", err)
	}

	lk, err := c.h.Lock(taskScope(req.TaskID.Value()))
	if err != nil {
		return PushGrantRecord{}, err
	}
	defer lk.Release()

	if _, ok, err := c.checkedReceipt(op); err != nil {
		return PushGrantRecord{}, err
	} else if ok {
		grant, found, err := c.readPushGrant(req.TaskID.Value(), Generation(req.Precondition.Generation), op.ID.Value())
		if err != nil {
			return PushGrantRecord{}, err
		}
		if !found {
			return PushGrantRecord{}, internalError("replay of push grant %s cannot reconstruct the committed evidence", op.ID.Value())
		}
		return grant, nil
	}

	doc, exists, err := c.readTaskDoc(req.TaskID.Value())
	if err != nil {
		return PushGrantRecord{}, err
	}
	if !exists {
		return PushGrantRecord{}, conflictError(ErrNotFound, "task %s not found", req.TaskID.Value())
	}
	cur := doc.Aggregate
	if err := verifyPrecondition(req.TaskID, cur, req.Precondition); err != nil {
		return PushGrantRecord{}, err
	}
	if err := c.checkMutableCurrent(cur); err != nil {
		return PushGrantRecord{}, err
	}
	if err := c.checkReservationFence(cur, nil); err != nil {
		return PushGrantRecord{}, err
	}
	if err := c.checkCleanupFence(cur, nil); err != nil {
		return PushGrantRecord{}, err
	}

	grant := PushGrantRecord{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        cur.TaskID,
		Generation:    cur.Generation,
		OperationID:   op.ID.Value(),
		Digest:        op.Digest,
		HeadSHA:       req.HeadSHA,
		Words:         req.Words,
		RecordedAt:    c.now().UnixNano(),
	}
	if err := validatePushGrantRecord(grant); err != nil {
		return PushGrantRecord{}, err
	}
	grantData, err := json.Marshal(grant)
	if err != nil {
		return PushGrantRecord{}, err
	}
	docData, err := json.Marshal(taskDoc{HomeRevision: doc.HomeRevision + 1, Aggregate: cur})
	if err != nil {
		return PushGrantRecord{}, err
	}
	recData, err := json.Marshal(receiptFor(op, cur))
	if err != nil {
		return PushGrantRecord{}, err
	}
	items := []home.ChangeItem{
		{Root: canonicalRoot, Key: taskCurrentKey(cur.TaskID), Data: docData},
		{Root: canonicalRoot, Key: pushGrantKey(cur.TaskID, cur.Generation, op.ID.Value()), Data: grantData},
		{Root: canonicalRoot, Key: receiptKey(op.ID.Value()), Data: recData},
	}
	if _, err := c.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return PushGrantRecord{}, commitError(req.TaskID, req.Precondition, err)
	}
	return grant, nil
}

// HasPushGrant reports whether the current task generation has a recorded grant
// for the exact full Git commit SHA. It reads under the task lock so reopen and
// grant recording cannot produce a mixed-generation decision.
func (c *Canonical) HasPushGrant(taskID domain.TaskID, headSHA string) (bool, error) {
	if err := taskID.Validate(); err != nil {
		return false, err
	}
	if !IsFullGitSHA(headSHA) {
		return false, validationError("push grant lookup head SHA must be a full 40-hex Git commit SHA")
	}
	lk, err := c.h.Lock(taskScope(taskID.Value()))
	if err != nil {
		return false, err
	}
	defer lk.Release()

	doc, exists, err := c.readTaskDoc(taskID.Value())
	if err != nil {
		return false, err
	}
	if !exists {
		return false, conflictError(ErrNotFound, "task %s not found", taskID.Value())
	}
	cur := doc.Aggregate
	if err := c.checkMutableCurrent(cur); err != nil {
		return false, err
	}
	entries, err := c.h.ReadDir(canonicalRoot, pushGrantDir(cur.TaskID, cur.Generation))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		opID := strings.TrimSuffix(entry.Name(), ".json")
		grant, found, err := c.readPushGrant(cur.TaskID, cur.Generation, opID)
		if err != nil {
			return false, err
		}
		if found && strings.EqualFold(grant.HeadSHA, headSHA) {
			return true, nil
		}
	}
	return false, nil
}
