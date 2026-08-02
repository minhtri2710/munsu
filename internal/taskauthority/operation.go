package taskauthority

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Actor identifies who performed an authoritative mutation.
type Actor struct {
	ID   string `json:"id"`
	Rank string `json:"rank,omitempty"`
}

// Operation is the stable identity of one authoritative mutation. Repeating
// the same ID with the same Digest returns the original receipt; reusing the
// ID with a different Digest is a typed non-retryable conflict.
type Operation struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	Actor  Actor  `json:"actor,omitempty"`
}

// Validate rejects empty or unsafe operation identities and malformed
// digests. Digest shape is the full 64-hex rule, not length alone: a
// 64-character non-hex digest must fail exactly like a short one so every
// adapter rejects it with the same typed error.
func (op Operation) Validate() error {
	if err := validateOperationID(op.ID); err != nil {
		return err
	}
	if !isSHA256Hex(op.Digest) {
		return validationError("operation digest must be a 64-hex sha256 digest")
	}
	return nil
}

// validateOperationID rejects empty or unsafe operation identities shared by
// operations and transfer intents.
func validateOperationID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\\`) {
		return validationError("operation ID must be a safe non-empty value")
	}
	return nil
}

// isSHA256Hex reports whether the value is a full 64-hex sha256 digest.
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// Receipt is the durable record of one committed operation. Task identity is
// read back from the committed view by the Authority; the receipt itself only
// pins the operation identity and its intent digest. Replayed is computed
// metadata (never persisted): true when the update returned a pre-existing
// receipt for the same Operation ID and digest.
type Receipt struct {
	OperationID string     `json:"operation_id"`
	Digest      string     `json:"digest"`
	CommittedAt int64      `json:"committed_at"`
	TaskID      string     `json:"task_id,omitempty"`
	Generation  Generation `json:"generation,omitempty"`
	Revision    Revision   `json:"revision,omitempty"`
	Phase       Phase      `json:"phase,omitempty"`
	Reopened    bool       `json:"reopened,omitempty"`
	// InterpretationID pins the dispatch interpretation record committed by
	// the operation, so replay returns the original committed record.
	InterpretationID string `json:"interpretation_id,omitempty"`
	Replayed         bool   `json:"-"`
}

// AuditEvent kinds.
const (
	// AuditLifecycle records a task lifecycle transition (task-bound).
	AuditLifecycle = "lifecycle"
	// AuditDispatch records a dispatch-control mutation (not task-bound).
	AuditDispatch = "dispatch"
	// AuditBinding records a generation-bound worktree binding (task-bound).
	AuditBinding = "binding"
	// AuditIssueLinks records a generation-bound issue link definition and
	// reconciliation commit (task-bound).
	AuditIssueLinks = "issue-links"
	// AuditAttestation records the acceptance of a generation-bound delivery
	// plan and capability attestation (task-bound).
	AuditAttestation = "attestation"
	// AuditMergeAuthorization records a generation-bound merge authorization
	// or external merge evidence commit (task-bound, Task 7.4).
	AuditMergeAuthorization = "merge-authorization"
	// AuditGitAuthorization records a generation-bound git authorization
	// change: the capability tier, the amendment/retirement context, or the
	// elevated git mutation authorization (task-bound, Task 7.4).
	AuditGitAuthorization = "git-authorization"
	// AuditDeliveryPrepare records a generation-bound delivery preparation:
	// the provider identity, head SHA, and review-ready state committed by
	// pr-check (task-bound, Task 7.5).
	AuditDeliveryPrepare = "delivery-prepare"
	// AuditDeliveryTerminal records a generation-bound delivery completion:
	// the delivered/done terminal transition and terminal provider evidence
	// (task-bound, Task 7.5).
	AuditDeliveryTerminal = "delivery-terminal"
	// AuditMergeOutcome records a generation-bound merge attempt and its
	// provider-verified remote outcome (merged, already-merged, open, failed,
	// or remote-unknown; task-bound, Task 7.6).
	AuditMergeOutcome = "merge-outcome"
	// AuditRetirement records the generation-bound retired phase transition
	// committed by the Retire operation after verified merged/delivered
	// evidence (task-bound, Task 7.7).
	AuditRetirement = "retirement"
	// AuditPromote records the generation-bound scout → ship kind promotion
	// committed by the Promote operation (task-bound, Task 7.8).
	AuditPromote = "promote"
)

// AuditEvent is a typed audit record committed in the same Store transaction
// as the authoritative mutation it describes.
type AuditEvent struct {
	OperationID string     `json:"operation_id"`
	Actor       Actor      `json:"actor"`
	Kind        string     `json:"kind"`
	TaskID      string     `json:"task_id,omitempty"`
	Generation  Generation `json:"generation,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	Before      Phase      `json:"before,omitempty"`
	After       Phase      `json:"after"`
	At          int64      `json:"at"`
}

// Validate checks the record shape of an audit event. Lifecycle events bind a
// task and generation and carry valid phases; dispatch events are not
// task-bound and carry no phases.
func (ev AuditEvent) Validate() error {
	if ev.OperationID == "" {
		return validationError("audit event missing operation id")
	}
	if ev.Actor.ID == "" {
		return validationError("audit event missing actor id")
	}
	if ev.At <= 0 {
		return validationError("audit event missing timestamp")
	}
	switch ev.Kind {
	case AuditLifecycle:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
		if ev.Before != "" && !ev.Before.Valid() {
			return validationError("audit event has invalid before phase %q", ev.Before)
		}
		if !ev.After.Valid() {
			return validationError("audit event has invalid after phase %q", ev.After)
		}
	case AuditBinding:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
	case AuditIssueLinks:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
	case AuditAttestation:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
	case AuditMergeAuthorization:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
	case AuditGitAuthorization:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
	case AuditDeliveryPrepare:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
	case AuditDeliveryTerminal:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
	case AuditMergeOutcome:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
	case AuditRetirement:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
		if ev.Before != "" && !ev.Before.Valid() {
			return validationError("audit event has invalid before phase %q", ev.Before)
		}
		if ev.After != PhaseRetired {
			return validationError("audit event has invalid after phase %q", ev.After)
		}
	case AuditPromote:
		if err := validateTaskID(ev.TaskID); err != nil {
			return err
		}
		if err := ev.Generation.Validate(); err != nil {
			return err
		}
		if ev.After != "" && !ev.After.Valid() {
			return validationError("audit event has invalid after phase %q", ev.After)
		}
	case AuditDispatch:
		// Task identity and phases are not applicable.
	default:
		return validationError("audit event has unknown kind %q", ev.Kind)
	}
	return nil
}

// requestDigest computes the deterministic intent digest of a semantic
// request. MarshalJSON field order is stable for structs, so the digest is
// stable across identical requests. The Operation ID itself is excluded so a
// request that changes intent under the same ID detects a conflict.
func requestDigest(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encoding request digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
