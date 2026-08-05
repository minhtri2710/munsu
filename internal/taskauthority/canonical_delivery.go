package taskauthority

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// This file owns the canonical Delivery Authorization and outcome foundation
// (ADR-0008 §3): Task Authority issues an immutable Delivery Authorization
// bound to the exact Task Generation and Operation ID, and Fleet commits the
// resulting delivery lifecycle consequence (the committed outcome) through
// Task Authority. Authorizations and outcomes are durable canonical documents
// beside the Task Aggregate (holds-style, keyed per task); every delivery
// mutation is an exact-generation/revision-fenced task-scoped mutation that
// advances the Task revision, preserves prior evidence (never overwrites an
// earlier authorization or outcome), and commits a durable operation receipt.

// DeliveryAuthorizationKind is the typed, closed set of irreversible delivery
// operations the canonical foundation authorizes. Kinds cover the actual #414
// operations (provider merge and repository mutation / local fast-forward);
// speculative capability tiers are deliberately absent.
type DeliveryAuthorizationKind string

const (
	// DeliveryAuthorizationProviderMerge authorizes merging the bound pull
	// request on the provider under the exact delivery identity and head.
	DeliveryAuthorizationProviderMerge DeliveryAuthorizationKind = "provider-merge"
	// DeliveryAuthorizationRepositoryMutation authorizes a local repository
	// mutation (e.g. amendment or local fast-forward) on the bound worktree
	// repository under the exact expected repository state (old-SHA lease
	// semantics).
	DeliveryAuthorizationRepositoryMutation DeliveryAuthorizationKind = "repository-mutation"
)

// Valid reports whether the kind is a known delivery authorization kind.
func (k DeliveryAuthorizationKind) Valid() bool {
	switch k {
	case DeliveryAuthorizationProviderMerge, DeliveryAuthorizationRepositoryMutation:
		return true
	}
	return false
}

// DeliveryExpectedState is the operation-specific expected repository state a
// repository-mutation authorization binds: the ref being mutated and the
// exact SHA the ref is expected to hold before the mutation (force-with-lease
// / lease semantics). Provider-merge authorizations carry no expected state.
type DeliveryExpectedState struct {
	Ref    string `json:"ref"`
	OldSHA string `json:"old_sha"`
}

// DeliveryPrecondition is a typed, closed-set precondition the Fleet delivery
// journal asserts was verified before authorization. Task Authority binds the
// asserted set durably; it never fabricates provider verification.
type DeliveryPrecondition string

const (
	// DeliveryPreconditionPRMergeable asserts the provider PR satisfies the
	// domain merge rules (open, checks passing, approving review).
	DeliveryPreconditionPRMergeable DeliveryPrecondition = "pr-mergeable"
	// DeliveryPreconditionPRHeadCurrent asserts the provider head equals the
	// bound delivery identity head.
	DeliveryPreconditionPRHeadCurrent DeliveryPrecondition = "pr-head-current"
	// DeliveryPreconditionWorktreeClean asserts the bound worktree has no
	// uncommitted changes.
	DeliveryPreconditionWorktreeClean DeliveryPrecondition = "worktree-clean"
)

// Valid reports whether the precondition is a known closed-set precondition.
func (p DeliveryPrecondition) Valid() bool {
	switch p {
	case DeliveryPreconditionPRMergeable, DeliveryPreconditionPRHeadCurrent, DeliveryPreconditionWorktreeClean:
		return true
	}
	return false
}

// DeliveryRevocation is the durable revocation evidence bound to one issued
// authorization. It preserves the revoking canonical mutation Operation
// identity and timestamp so the prior authorization remains auditable after
// revocation; revocation never deletes or rewrites the authorization.
type DeliveryRevocation struct {
	OperationID string `json:"operation_id"`
	Digest      string `json:"digest"`
	RevokedAt   int64  `json:"revoked_at"`
	Reason      string `json:"reason"`
}

// DeliveryAuthorization is one immutable issuance record. It durably binds:
//
//   - the Fleet delivery journal Operation identity (the canonical mutation
//     Operation identity whose receipt is committed with the record);
//   - the exact Task ID, Generation, post-issuance Revision, and current
//     phase;
//   - current ownership and the typed domain.DeliveryIdentity including the
//     exact provider head;
//   - the operation kind and the operation-specific expected repository state
//     (old SHA for lease semantics; none for provider merge);
//   - the exact Endpoint/Worktree binding digest (lease/fence and repository
//     identity/path/head fields);
//   - the relevant delivery-holds digest;
//   - the typed, closed-set delivery preconditions;
//   - the issuance timestamp.
//
// Revocation appends evidence to the same record; a later distinct
// authorization is a new issuance, never a mutation of this record.
type DeliveryAuthorization struct {
	TaskID        string                    `json:"task_id"`
	Generation    Generation                `json:"generation"`
	Revision      Revision                  `json:"revision"`
	Phase         Phase                     `json:"phase"`
	Owner         string                    `json:"owner"`
	Kind          DeliveryAuthorizationKind `json:"kind"`
	Identity      domain.DeliveryIdentity   `json:"identity"`
	ExpectedState *DeliveryExpectedState    `json:"expected_state,omitempty"`
	BindingDigest string                    `json:"binding_digest"`
	HoldsDigest   string                    `json:"holds_digest"`
	Preconditions []DeliveryPrecondition    `json:"preconditions"`
	OperationID   string                    `json:"operation_id"`
	Digest        string                    `json:"digest"`
	IssuedAt      int64                     `json:"issued_at"`
	Revoked       *DeliveryRevocation       `json:"revoked,omitempty"`
}

// clone deep-copies the record so committed records are never aliased.
func (a DeliveryAuthorization) clone() DeliveryAuthorization {
	out := a
	if a.ExpectedState != nil {
		es := *a.ExpectedState
		out.ExpectedState = &es
	}
	if a.Revoked != nil {
		r := *a.Revoked
		out.Revoked = &r
	}
	out.Preconditions = append([]DeliveryPrecondition(nil), a.Preconditions...)
	return out
}

// DeliveryOutcomeStatus is the typed, closed set of truthful delivery outcome
// statuses. completed, partial, and remote-unknown are terminal: once
// committed they bind the record and a distinct incompatible outcome
// conflicts. retryable records a transient failure that a later distinct
// authorization may follow.
type DeliveryOutcomeStatus string

const (
	DeliveryOutcomeCompleted     DeliveryOutcomeStatus = "completed"
	DeliveryOutcomePartial       DeliveryOutcomeStatus = "partial"
	DeliveryOutcomeRemoteUnknown DeliveryOutcomeStatus = "remote-unknown"
	DeliveryOutcomeRetryable     DeliveryOutcomeStatus = "retryable"
)

// Valid reports whether the status is a known closed-set outcome status.
func (s DeliveryOutcomeStatus) Valid() bool {
	switch s {
	case DeliveryOutcomeCompleted, DeliveryOutcomePartial, DeliveryOutcomeRemoteUnknown, DeliveryOutcomeRetryable:
		return true
	}
	return false
}

// terminal reports whether the status is an end state of the delivery record.
func (s DeliveryOutcomeStatus) terminal() bool {
	return s == DeliveryOutcomeCompleted || s == DeliveryOutcomePartial || s == DeliveryOutcomeRemoteUnknown
}

// DeliveryOutcome is one committed, truthful delivery outcome. It binds the
// exact journal operation, the authorization identity the delivery executed
// under, the task generation, the provider/repository evidence (head/merged
// SHA as applicable), the detail classification, and the commit time. Prior
// outcomes are preserved; a terminal outcome bounds the record.
type DeliveryOutcome struct {
	TaskID                   string                `json:"task_id"`
	Generation               Generation            `json:"generation"`
	AuthorizationOperationID string                `json:"authorization_operation_id"`
	OperationID              string                `json:"operation_id"`
	Digest                   string                `json:"digest"`
	Status                   DeliveryOutcomeStatus `json:"status"`
	Detail                   string                `json:"detail"`
	HeadSHA                  string                `json:"head_sha,omitempty"`
	MergedSHA                string                `json:"merged_sha,omitempty"`
	CommittedAt              int64                 `json:"committed_at"`
}

// clone returns a deep copy of the outcome.
func (o DeliveryOutcome) clone() DeliveryOutcome { return o }

// DeliveryRecord is the per-task canonical delivery ledger: the bounded
// issuance history (at most one active authorization, always the last entry)
// and the outcome history (terminal outcomes bound the record). Prior
// authorization and outcome evidence is preserved, never overwritten.
type DeliveryRecord struct {
	SchemaVersion  string                  `json:"schema_version"`
	TaskID         string                  `json:"task_id"`
	Authorizations []DeliveryAuthorization `json:"authorizations,omitempty"`
	Outcomes       []DeliveryOutcome       `json:"outcomes,omitempty"`
}

// clone deep-copies the record so committed records are never aliased.
func (r DeliveryRecord) clone() DeliveryRecord {
	out := r
	out.Authorizations = make([]DeliveryAuthorization, len(r.Authorizations))
	for i := range r.Authorizations {
		out.Authorizations[i] = r.Authorizations[i].clone()
	}
	out.Outcomes = make([]DeliveryOutcome, len(r.Outcomes))
	for i := range r.Outcomes {
		out.Outcomes[i] = r.Outcomes[i].clone()
	}
	return out
}

// Canonical storage layout: delivery records live under the canonical state
// root beside task documents and holds, keyed per task.
const deliveryDir = "task-authority/delivery"

func deliveryKey(taskID string) string { return deliveryDir + "/" + taskID + ".json" }

// CanonicalDeliveryAuthorizationRequest is the typed intent for one delivery
// authorization issuance.
type CanonicalDeliveryAuthorizationRequest struct {
	HomeID        domain.HomeID
	TaskID        domain.TaskID
	Precondition  domain.Precondition
	Kind          DeliveryAuthorizationKind
	Identity      domain.DeliveryIdentity
	ExpectedState *DeliveryExpectedState
	Preconditions []DeliveryPrecondition
}

func (r CanonicalDeliveryAuthorizationRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID        string                    `json:"home_id"`
		TaskID        string                    `json:"task_id"`
		Generation    uint64                    `json:"generation"`
		Revision      uint64                    `json:"revision"`
		Kind          DeliveryAuthorizationKind `json:"kind"`
		Identity      domain.DeliveryIdentity   `json:"identity"`
		ExpectedState *DeliveryExpectedState    `json:"expected_state,omitempty"`
		Preconditions []DeliveryPrecondition    `json:"preconditions"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.Kind, r.Identity, r.ExpectedState, uniqueDeliveryPreconditions(r.Preconditions)})
}

// DeliveryAuthorizationResult is the committed outcome of an authorization
// issuance or revocation.
type DeliveryAuthorizationResult struct {
	Authorization DeliveryAuthorization
	Replayed      bool
}

// CanonicalRevokeDeliveryRequest is the typed intent for one delivery
// authorization revocation.
type CanonicalRevokeDeliveryRequest struct {
	HomeID                   domain.HomeID
	TaskID                   domain.TaskID
	Precondition             domain.Precondition
	AuthorizationOperationID string
	Reason                   string
}

func (r CanonicalRevokeDeliveryRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID                   string `json:"home_id"`
		TaskID                   string `json:"task_id"`
		Generation               uint64 `json:"generation"`
		Revision                 uint64 `json:"revision"`
		AuthorizationOperationID string `json:"authorization_operation_id"`
		Reason                   string `json:"reason"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.AuthorizationOperationID, r.Reason})
}

// CanonicalDeliveryOutcomeRequest is the typed intent for one delivery
// outcome commit.
type CanonicalDeliveryOutcomeRequest struct {
	HomeID                   domain.HomeID
	TaskID                   domain.TaskID
	Precondition             domain.Precondition
	AuthorizationOperationID string
	Status                   DeliveryOutcomeStatus
	Detail                   string
	HeadSHA                  string
	MergedSHA                string
}

func (r CanonicalDeliveryOutcomeRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID                   string                `json:"home_id"`
		TaskID                   string                `json:"task_id"`
		Generation               uint64                `json:"generation"`
		Revision                 uint64                `json:"revision"`
		AuthorizationOperationID string                `json:"authorization_operation_id"`
		Status                   DeliveryOutcomeStatus `json:"status"`
		Detail                   string                `json:"detail"`
		HeadSHA                  string                `json:"head_sha,omitempty"`
		MergedSHA                string                `json:"merged_sha,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.AuthorizationOperationID, r.Status, r.Detail, r.HeadSHA, r.MergedSHA})
}

// DeliveryOutcomeResult is the committed outcome of an outcome mutation.
type DeliveryOutcomeResult struct {
	Outcome  DeliveryOutcome
	Replayed bool
}

// DeliveryCurrencyReason is a typed reason why the current delivery
// authorization is not valid against current task state.
type DeliveryCurrencyReason string

const (
	DeliveryCurrencyNotFound        DeliveryCurrencyReason = "not-found"
	DeliveryCurrencyNotCurrent      DeliveryCurrencyReason = "not-current"
	DeliveryCurrencyGeneration      DeliveryCurrencyReason = "generation-mismatch"
	DeliveryCurrencyRevision        DeliveryCurrencyReason = "revision-mismatch"
	DeliveryCurrencyPhase           DeliveryCurrencyReason = "phase-mismatch"
	DeliveryCurrencyMissingOwner    DeliveryCurrencyReason = "missing-owner"
	DeliveryCurrencyMissingBindings DeliveryCurrencyReason = "missing-bindings"
	DeliveryCurrencyBindingDigest   DeliveryCurrencyReason = "binding-digest"
	DeliveryCurrencyHoldsDigest     DeliveryCurrencyReason = "holds-digest"
	DeliveryCurrencyMatchingHold    DeliveryCurrencyReason = "matching-hold"
	DeliveryCurrencyReservation     DeliveryCurrencyReason = "transfer-reserved"
	DeliveryCurrencyRevoked         DeliveryCurrencyReason = "revoked"
	DeliveryCurrencyIdentityHead    DeliveryCurrencyReason = "identity-head"
	DeliveryCurrencyNoAuthorization DeliveryCurrencyReason = "no-authorization"
)

// DeliveryCurrency is the narrow read-only currency evaluation of one task's
// current delivery authorization. It recomputes the current
// generation/revision/phase/currentness, transfer reservation, delivery-holds
// digest, binding digest, authorization status, and identity/head, and returns
// typed valid/invalid reasons. It never mutates state and never creates
// receipts.
type DeliveryCurrency struct {
	TaskID           string
	Generation       Generation
	Revision         Revision
	Phase            Phase
	Current          bool
	Authorization    *DeliveryAuthorization
	Valid            bool
	Reasons          []DeliveryCurrencyReason
	TransferReserved bool
	HoldsDigest      string
	BindingDigest    string
	Identity         *domain.DeliveryIdentity
	Outcome          *DeliveryOutcome
}

// safeSHAValue accepts a safe non-empty SHA identity (no path separators).
// The canonical Git fixtures carry full 40-hex object IDs; validation follows
// the existing domain delivery identity/SHA rules and never invents a
// hex-only rule.
func safeSHAValue(s string) bool {
	return s != "" && s == strings.TrimSpace(s) && !strings.ContainsAny(s, `/\\`)
}

// validateDeliveryExpectedState checks the operation-specific expected
// repository state: a safe ref (refs may legitimately contain slashes) and a
// safe old SHA for lease semantics.
func validateDeliveryExpectedState(es DeliveryExpectedState) error {
	if es.Ref == "" || es.Ref != strings.TrimSpace(es.Ref) || strings.ContainsAny(es.Ref, `\\`) || strings.ContainsAny(es.Ref, " \t\n\r") || es.Ref == "." || es.Ref == ".." {
		return validationError("delivery expected state requires a safe ref")
	}
	if !safeSHAValue(es.OldSHA) {
		return validationError("delivery expected state requires a safe old SHA")
	}
	return nil
}

// validateDeliveryAuthorizationRequest checks the issuance intent: a known
// kind, a valid typed domain delivery identity, kind-appropriate expected
// repository state, and a non-empty unique closed-set of preconditions.
func validateDeliveryAuthorizationRequest(req CanonicalDeliveryAuthorizationRequest) error {
	if !req.Kind.Valid() {
		return validationError("invalid delivery authorization kind %q", req.Kind)
	}
	if err := domain.ValidateIdentity(&req.Identity); err != nil {
		return validationError("delivery identity is invalid: %v", err)
	}
	if !safeSHAValue(req.Identity.HeadSHA) {
		return validationError("delivery identity head SHA must be a safe non-empty value")
	}
	switch req.Kind {
	case DeliveryAuthorizationProviderMerge:
		if req.ExpectedState != nil {
			return validationError("provider-merge authorization carries no expected repository state")
		}
	case DeliveryAuthorizationRepositoryMutation:
		if req.ExpectedState == nil {
			return validationError("repository-mutation authorization requires the expected repository state")
		}
		if err := validateDeliveryExpectedState(*req.ExpectedState); err != nil {
			return err
		}
	}
	if len(req.Preconditions) == 0 {
		return validationError("delivery authorization requires at least one typed precondition")
	}
	seen := map[DeliveryPrecondition]bool{}
	for _, p := range req.Preconditions {
		if !p.Valid() {
			return validationError("invalid delivery precondition %q", p)
		}
		if seen[p] {
			return validationError("duplicate delivery precondition %q", p)
		}
		seen[p] = true
	}
	return nil
}

// validateDeliveryRevocation checks the revocation evidence shape.
func validateDeliveryRevocation(r DeliveryRevocation) error {
	if r.OperationID == "" || strings.ContainsAny(r.OperationID, `/\\`) {
		return validationError("delivery revocation missing operation id")
	}
	if !domain.IsSHA256(r.Digest) {
		return validationError("delivery revocation digest must be a 64-hex sha256 digest")
	}
	if r.RevokedAt <= 0 {
		return validationError("delivery revocation missing revoked timestamp")
	}
	if strings.TrimSpace(r.Reason) == "" {
		return validationError("delivery revocation missing reason")
	}
	return nil
}

// validateDeliveryAuthorization checks one committed authorization record
// shape and the kind-appropriate expected state.
func validateDeliveryAuthorization(a DeliveryAuthorization) error {
	if a.TaskID == "" || strings.ContainsAny(a.TaskID, `/\\`) {
		return validationError("delivery authorization missing safe task id")
	}
	if err := a.Generation.Validate(); err != nil {
		return err
	}
	if a.Revision == 0 {
		return validationError("delivery authorization missing revision")
	}
	if !a.Phase.Valid() || a.Phase != PhaseWorking {
		return validationError("delivery authorization binds a non-working phase %q", a.Phase)
	}
	if strings.TrimSpace(a.Owner) == "" {
		return validationError("delivery authorization missing owner")
	}
	if !a.Kind.Valid() {
		return validationError("delivery authorization has invalid kind %q", a.Kind)
	}
	if err := domain.ValidateIdentity(&a.Identity); err != nil {
		return validationError("delivery authorization identity is invalid: %v", err)
	}
	if !safeSHAValue(a.Identity.HeadSHA) {
		return validationError("delivery authorization identity head SHA must be a safe non-empty value")
	}
	switch a.Kind {
	case DeliveryAuthorizationProviderMerge:
		if a.ExpectedState != nil {
			return validationError("provider-merge authorization carries expected repository state")
		}
	case DeliveryAuthorizationRepositoryMutation:
		if a.ExpectedState == nil {
			return validationError("repository-mutation authorization missing expected repository state")
		}
		if err := validateDeliveryExpectedState(*a.ExpectedState); err != nil {
			return err
		}
	}
	if !domain.IsSHA256(a.BindingDigest) {
		return validationError("delivery authorization binding digest must be a 64-hex sha256 digest")
	}
	if !domain.IsSHA256(a.HoldsDigest) {
		return validationError("delivery authorization holds digest must be a 64-hex sha256 digest")
	}
	if len(a.Preconditions) == 0 {
		return validationError("delivery authorization missing preconditions")
	}
	seen := map[DeliveryPrecondition]bool{}
	for _, p := range a.Preconditions {
		if !p.Valid() {
			return validationError("delivery authorization has invalid precondition %q", p)
		}
		if seen[p] {
			return validationError("delivery authorization has duplicate precondition %q", p)
		}
		seen[p] = true
	}
	if a.OperationID == "" || strings.ContainsAny(a.OperationID, `/\\`) {
		return validationError("delivery authorization missing operation id")
	}
	if !domain.IsSHA256(a.Digest) {
		return validationError("delivery authorization digest must be a 64-hex sha256 digest")
	}
	if a.IssuedAt <= 0 {
		return validationError("delivery authorization missing issued timestamp")
	}
	if a.Revoked != nil {
		if err := validateDeliveryRevocation(*a.Revoked); err != nil {
			return err
		}
	}
	return nil
}

// validateDeliveryOutcome checks one committed outcome record shape: the
// closed-set status, the bound journal and authorization identities, the
// detail classification, safe evidence SHAs when present, and the commit time.
func validateDeliveryOutcome(o DeliveryOutcome) error {
	if o.TaskID == "" || strings.ContainsAny(o.TaskID, `/\\`) {
		return validationError("delivery outcome missing safe task id")
	}
	if err := o.Generation.Validate(); err != nil {
		return err
	}
	if o.AuthorizationOperationID == "" || strings.ContainsAny(o.AuthorizationOperationID, `/\\`) {
		return validationError("delivery outcome missing authorization operation id")
	}
	if o.OperationID == "" || strings.ContainsAny(o.OperationID, `/\\`) {
		return validationError("delivery outcome missing operation id")
	}
	if !domain.IsSHA256(o.Digest) {
		return validationError("delivery outcome digest must be a 64-hex sha256 digest")
	}
	if !o.Status.Valid() {
		return validationError("delivery outcome has invalid status %q", o.Status)
	}
	if strings.TrimSpace(o.Detail) == "" {
		return validationError("delivery outcome missing detail classification")
	}
	if o.HeadSHA != "" && !safeSHAValue(o.HeadSHA) {
		return validationError("delivery outcome head SHA must be a safe value")
	}
	if o.MergedSHA != "" && !safeSHAValue(o.MergedSHA) {
		return validationError("delivery outcome merged SHA must be a safe value")
	}
	if o.CommittedAt <= 0 {
		return validationError("delivery outcome missing committed timestamp")
	}
	return nil
}

// validateDeliveryRecord checks the per-task delivery ledger invariants: the
// current schema identity, at most one active authorization (always the last
// entry, so there is no current/superseded ambiguity), and no outcome after a
// terminal outcome.
func validateDeliveryRecord(rec DeliveryRecord) error {
	if rec.SchemaVersion != TaskAuthoritySchema {
		return validationError("invalid delivery record schema %q", rec.SchemaVersion)
	}
	if rec.TaskID == "" || strings.ContainsAny(rec.TaskID, `/\\`) {
		return validationError("delivery record missing safe task id")
	}
	for i, a := range rec.Authorizations {
		if err := validateDeliveryAuthorization(a); err != nil {
			return err
		}
		if a.TaskID != rec.TaskID {
			return validationError("delivery authorization %q binds a different task", a.OperationID)
		}
		if a.Revoked == nil && i != len(rec.Authorizations)-1 {
			return validationError("delivery record has a superseded active authorization")
		}
	}
	seenTerminal := false
	for _, o := range rec.Outcomes {
		if err := validateDeliveryOutcome(o); err != nil {
			return err
		}
		if o.TaskID != rec.TaskID {
			return validationError("delivery outcome %q binds a different task", o.OperationID)
		}
		if seenTerminal {
			return validationError("delivery record has an outcome after a terminal outcome")
		}
		if o.Status.terminal() {
			seenTerminal = true
		}
	}
	return nil
}

// readDeliveryRecord reads and validates the per-task delivery ledger.
// Malformed state fails closed instead of being served.
func (c *Canonical) readDeliveryRecord(taskID string) (DeliveryRecord, bool, error) {
	data, ok, err := c.readDoc(deliveryKey(taskID))
	if err != nil || !ok {
		return DeliveryRecord{}, ok, err
	}
	var rec DeliveryRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return DeliveryRecord{}, true, internalError("decode delivery record for task %s: %v", taskID, err)
	}
	if err := validateDeliveryRecord(rec); err != nil {
		return DeliveryRecord{}, true, internalError("task %s has malformed delivery record: %v", taskID, err)
	}
	return rec, true, nil
}

// mutateDelivery runs one task-scoped delivery mutation: receipt idempotency
// first (replay reconstructs the committed record from the preserved ledger),
// then the exact generation/revision precondition, currentness, and transfer
// reservation fences, then one atomic home.Commit that writes the advanced
// task document, the updated delivery ledger, and the operation receipt
// together. Delivery mutations advance the Task revision exactly once, so the
// recorded post-issuance revision is the authoritative revision written by
// the issuance commit.
func (c *Canonical) mutateDelivery(op domain.Operation, taskID domain.TaskID, prec domain.Precondition, apply func(Aggregate, DeliveryRecord) (Aggregate, DeliveryRecord, error)) (Aggregate, DeliveryRecord, bool, error) {
	if err := op.Validate(); err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	if err := prec.Validate(); err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	lk, err := c.h.Lock(taskScope(taskID.Value()))
	if err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	defer lk.Release()

	if _, ok, err := c.checkedReceipt(op); err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	} else if ok {
		doc, exists, err := c.readDeliveryRecord(taskID.Value())
		if err != nil || !exists {
			return Aggregate{}, DeliveryRecord{}, false, internalError("replay of %s cannot reconstruct the delivery record", op.ID.Value())
		}
		return Aggregate{}, doc, true, nil
	}

	doc, exists, err := c.readTaskDoc(taskID.Value())
	if err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	if !exists {
		return Aggregate{}, DeliveryRecord{}, false, conflictError(ErrNotFound, "task %s not found", taskID.Value())
	}
	if err := verifyPrecondition(taskID, doc.Aggregate, prec); err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	if err := c.checkMutableCurrent(doc.Aggregate); err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	if err := c.checkReservationFence(doc.Aggregate, nil); err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	deliveryDoc, _, err := c.readDeliveryRecord(taskID.Value())
	if err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	nextAgg, nextRec, err := apply(doc.Aggregate, deliveryDoc)
	if err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}

	newDoc := taskDoc{HomeRevision: doc.HomeRevision + 1, Aggregate: nextAgg}
	rec := receiptFor(op, nextAgg)
	docData, err := json.Marshal(newDoc)
	if err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	deliveryData, err := json.Marshal(nextRec)
	if err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		return Aggregate{}, DeliveryRecord{}, false, err
	}
	items := []home.ChangeItem{
		{Root: canonicalRoot, Key: taskCurrentKey(taskID.Value()), Data: docData},
		{Root: canonicalRoot, Key: deliveryKey(taskID.Value()), Data: deliveryData},
		{Root: canonicalRoot, Key: receiptKey(rec.OperationID), Data: recData},
	}
	if _, err := c.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return Aggregate{}, DeliveryRecord{}, false, commitError(taskID, prec, err)
	}
	return nextAgg, nextRec, false, nil
}

// currentDeliveryAuthorization returns the active (unrevoked) authorization
// of the ledger. At most one exists, and it is the last entry.
func currentDeliveryAuthorization(rec DeliveryRecord) *DeliveryAuthorization {
	if len(rec.Authorizations) == 0 {
		return nil
	}
	a := rec.Authorizations[len(rec.Authorizations)-1]
	if a.Revoked != nil {
		return nil
	}
	return &a
}

func findAuthorization(rec DeliveryRecord, operationID string) (DeliveryAuthorization, bool) {
	for i := range rec.Authorizations {
		if rec.Authorizations[i].OperationID == operationID {
			return rec.Authorizations[i], true
		}
	}
	return DeliveryAuthorization{}, false
}

func findAuthorizationByRevocation(rec DeliveryRecord, operationID string) (DeliveryAuthorization, bool) {
	for i := range rec.Authorizations {
		a := rec.Authorizations[i]
		if a.Revoked != nil && a.Revoked.OperationID == operationID {
			return a, true
		}
	}
	return DeliveryAuthorization{}, false
}

func findOutcome(rec DeliveryRecord, operationID string) (DeliveryOutcome, bool) {
	for i := range rec.Outcomes {
		if rec.Outcomes[i].OperationID == operationID {
			return rec.Outcomes[i], true
		}
	}
	return DeliveryOutcome{}, false
}

func hasTerminalOutcome(rec DeliveryRecord) bool {
	for _, o := range rec.Outcomes {
		if o.Status.terminal() {
			return true
		}
	}
	return false
}

// uniqueDeliveryPreconditions sorts and de-duplicates the typed precondition
// set so digests and records are deterministic.
func uniqueDeliveryPreconditions(in []DeliveryPrecondition) []DeliveryPrecondition {
	out := append([]DeliveryPrecondition(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	result := out[:0]
	for _, p := range out {
		if len(result) == 0 || result[len(result)-1] != p {
			result = append(result, p)
		}
	}
	return result
}

// deliveryBindingDigest is the deterministic sha256 digest over the exact
// endpoint and worktree binding identities (lease/fence and repository
// identity/path/head fields). BoundAtUnix is bind-time state, not identity,
// and is excluded.
func deliveryBindingDigest(endpoint EndpointBinding, worktree WorktreeBinding) string {
	payload := struct {
		Endpoint struct {
			Backend      string `json:"backend"`
			Handle       string `json:"handle"`
			LeaseID      string `json:"lease_id"`
			FenceToken   string `json:"fence_token"`
			SessionOwner string `json:"session_owner,omitempty"`
			WorkspaceID  string `json:"workspace_id,omitempty"`
			TabID        string `json:"tab_id,omitempty"`
		} `json:"endpoint"`
		Worktree struct {
			RepositoryIdentity string `json:"repository_identity"`
			Path               string `json:"path"`
			GitDir             string `json:"git_dir"`
			CommonDir          string `json:"common_dir"`
			Head               string `json:"head"`
			LeaseID            string `json:"lease_id"`
			FenceToken         string `json:"fence_token"`
		} `json:"worktree"`
	}{}
	payload.Endpoint.Backend = endpoint.Backend
	payload.Endpoint.Handle = endpoint.Handle
	payload.Endpoint.LeaseID = endpoint.LeaseID
	payload.Endpoint.FenceToken = endpoint.FenceToken
	payload.Endpoint.SessionOwner = endpoint.SessionOwner
	payload.Endpoint.WorkspaceID = endpoint.WorkspaceID
	payload.Endpoint.TabID = endpoint.TabID
	payload.Worktree.RepositoryIdentity = worktree.RepositoryIdentity
	payload.Worktree.Path = worktree.Path
	payload.Worktree.GitDir = worktree.GitDir
	payload.Worktree.CommonDir = worktree.CommonDir
	payload.Worktree.Head = worktree.Head
	payload.Worktree.LeaseID = worktree.LeaseID
	payload.Worktree.FenceToken = worktree.FenceToken
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return sha256Hex(data)
}

// deliveryHoldDigestEntry is the deterministic digest content of one
// delivery-relevant hold document, including its release state.
type deliveryHoldDigestEntry struct {
	ID         string            `json:"id"`
	Scope      DispatchHoldScope `json:"scope,omitempty"`
	Actions    []DispatchAction  `json:"actions"`
	Reason     string            `json:"reason"`
	CreatedAt  int64             `json:"created_at"`
	ReleasedAt int64             `json:"released_at,omitempty"`
}

// deliveryHoldsDigest is the deterministic sha256 digest over the committed
// delivery-relevant hold documents of the task: every hold whose action set
// includes the delivery action and whose scope matches the task, including
// released holds (the release state is part of the digest, so a hold release
// invalidates currency independently of the Task revision). Unrelated holds
// (other actions or scopes) never enter the digest.
func deliveryHoldsDigest(holds []DispatchHold, agg Aggregate) string {
	var entries []deliveryHoldDigestEntry
	for _, hold := range holds {
		if !deliveryHoldRelevant(hold, agg) {
			continue
		}
		entries = append(entries, deliveryHoldDigestEntry{
			ID:         hold.ID,
			Scope:      hold.Scope.clone(),
			Actions:    append([]DispatchAction(nil), hold.Actions...),
			Reason:     hold.Reason,
			CreatedAt:  hold.CreatedAt,
			ReleasedAt: hold.ReleasedAt,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	data, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	return sha256Hex(data)
}

// deliveryHoldRelevant reports whether a hold document is delivery-relevant
// for the task: its action set includes the delivery action and its scope
// matches the task. Release state is deliberately NOT considered — the
// relevant set is stable across add/release so both invalidate the digest.
func deliveryHoldRelevant(hold DispatchHold, agg Aggregate) bool {
	if !containsAction(hold.Actions, DispatchActionDelivery) {
		return false
	}
	return holdScopeMatches(hold, agg)
}

// holdScopeMatches checks the hold scope against the task identity fields.
func holdScopeMatches(hold DispatchHold, agg Aggregate) bool {
	if len(hold.Scope.TaskIDs) > 0 && !containsString(hold.Scope.TaskIDs, agg.TaskID) {
		return false
	}
	if len(hold.Scope.ProjectIDs) > 0 && !containsString(hold.Scope.ProjectIDs, agg.Definition.Project) {
		return false
	}
	if len(hold.Scope.Generations) > 0 && !containsString(hold.Scope.Generations, agg.Generation.String()) {
		return false
	}
	if len(hold.Scope.ParentIDs) > 0 && !containsString(hold.Scope.ParentIDs, agg.Definition.ParentTaskID) {
		return false
	}
	return true
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// AuthorizeDelivery issues one canonical delivery authorization for the exact
// #414 irreversible operation kind. Issuance requires a current working task
// with owner and the exact bindings required by the kind, no matching active
// delivery hold, no active transfer reservation, no terminal committed
// outcome, no already-active authorization, a valid typed identity whose head
// matches the bound worktree head, and valid preconditions; it fails closed
// otherwise. Repeating the same Operation ID with the same digest replays the
// durable prior record; reusing the Operation ID with a different intent
// conflicts.
func (c *Canonical) AuthorizeDelivery(op domain.Operation, req CanonicalDeliveryAuthorizationRequest) (DeliveryAuthorizationResult, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return DeliveryAuthorizationResult{}, err
	}
	if err := validateDeliveryAuthorizationRequest(req); err != nil {
		return DeliveryAuthorizationResult{}, err
	}
	_, rec, replayed, err := c.mutateDelivery(op, req.TaskID, req.Precondition, func(cur Aggregate, rec DeliveryRecord) (Aggregate, DeliveryRecord, error) {
		if cur.Phase != PhaseWorking {
			return Aggregate{}, DeliveryRecord{}, preconditionError("delivery authorization requires a working task; task %s is %s", cur.TaskID, cur.Phase)
		}
		if strings.TrimSpace(cur.Definition.Owner) == "" {
			return Aggregate{}, DeliveryRecord{}, preconditionError("delivery authorization requires an owner for task %s", cur.TaskID)
		}
		if cur.Worktree == nil || cur.Endpoint == nil {
			return Aggregate{}, DeliveryRecord{}, preconditionError("delivery authorization requires the bound worktree and endpoint of task %s", cur.TaskID)
		}
		if cur.Worktree.Head != req.Identity.HeadSHA {
			return Aggregate{}, DeliveryRecord{}, preconditionError("delivery authorization identity head %q does not match the bound worktree head %q", req.Identity.HeadSHA, cur.Worktree.Head)
		}
		holds, err := c.listHolds()
		if err != nil {
			return Aggregate{}, DeliveryRecord{}, err
		}
		if holdsBlockAction(holds, DispatchActionDelivery, cur) {
			return Aggregate{}, DeliveryRecord{}, conflictError(ErrDispatchHeld, "%s: delivery is held for task %s", ErrDispatchHeld, cur.TaskID)
		}
		if hasTerminalOutcome(rec) {
			return Aggregate{}, DeliveryRecord{}, conflictError(ErrConflict, "task %s already committed terminal delivery outcome %q; a new delivery authorization conflicts", cur.TaskID, rec.Outcomes[len(rec.Outcomes)-1].Status)
		}
		if currentDeliveryAuthorization(rec) != nil {
			return Aggregate{}, DeliveryRecord{}, conflictError(ErrConflict, "task %s already has an active delivery authorization; revoke it before issuing a new one", cur.TaskID)
		}
		next := cur.clone()
		next.Revision++
		auth := DeliveryAuthorization{
			TaskID:        cur.TaskID,
			Generation:    cur.Generation,
			Revision:      next.Revision,
			Phase:         cur.Phase,
			Owner:         cur.Definition.Owner,
			Kind:          req.Kind,
			Identity:      req.Identity,
			BindingDigest: deliveryBindingDigest(*cur.Endpoint, *cur.Worktree),
			HoldsDigest:   deliveryHoldsDigest(holds, cur),
			Preconditions: uniqueDeliveryPreconditions(req.Preconditions),
			OperationID:   op.ID.Value(),
			Digest:        op.Digest,
			IssuedAt:      c.now().UnixNano(),
		}
		if req.ExpectedState != nil {
			es := *req.ExpectedState
			auth.ExpectedState = &es
		}
		if err := validateDeliveryAuthorization(auth); err != nil {
			return Aggregate{}, DeliveryRecord{}, err
		}
		rec2 := rec
		if rec2.SchemaVersion == "" {
			rec2.SchemaVersion = TaskAuthoritySchema
			rec2.TaskID = cur.TaskID
		}
		rec2.Authorizations = append(rec2.Authorizations, auth)
		if err := validateDeliveryRecord(rec2); err != nil {
			return Aggregate{}, DeliveryRecord{}, err
		}
		return next, rec2, nil
	})
	if err != nil {
		return DeliveryAuthorizationResult{}, err
	}
	if replayed {
		auth, ok := findAuthorization(rec, op.ID.Value())
		if !ok {
			return DeliveryAuthorizationResult{}, internalError("replay of delivery authorization %s cannot reconstruct the committed record", op.ID.Value())
		}
		return DeliveryAuthorizationResult{Authorization: auth, Replayed: true}, nil
	}
	return DeliveryAuthorizationResult{Authorization: rec.Authorizations[len(rec.Authorizations)-1]}, nil
}

// RevokeDeliveryAuthorization revokes the active authorization under its
// exact operation identity, fenced to the exact generation/revision
// precondition. Same Operation ID + digest replay is idempotent; a changed
// intent conflicts. Revocation preserves the prior authorization evidence and
// permits a later distinct authorization only through a new canonical
// issuance, never by mutating the old record.
func (c *Canonical) RevokeDeliveryAuthorization(op domain.Operation, req CanonicalRevokeDeliveryRequest) (DeliveryAuthorizationResult, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return DeliveryAuthorizationResult{}, err
	}
	if req.AuthorizationOperationID == "" || strings.ContainsAny(req.AuthorizationOperationID, `/\\`) {
		return DeliveryAuthorizationResult{}, validationError("revocation requires the exact authorization operation identity")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return DeliveryAuthorizationResult{}, validationError("revocation requires a reason")
	}
	_, rec, replayed, err := c.mutateDelivery(op, req.TaskID, req.Precondition, func(cur Aggregate, rec DeliveryRecord) (Aggregate, DeliveryRecord, error) {
		auth := currentDeliveryAuthorization(rec)
		if auth == nil {
			return Aggregate{}, DeliveryRecord{}, conflictError(ErrConflict, "task %s has no active delivery authorization to revoke", cur.TaskID)
		}
		if auth.OperationID != req.AuthorizationOperationID {
			return Aggregate{}, DeliveryRecord{}, conflictError(ErrConflict, "delivery authorization identity mismatch: current %q vs requested %q", auth.OperationID, req.AuthorizationOperationID)
		}
		next := cur.clone()
		next.Revision++
		revoked := auth.clone()
		revoked.Revoked = &DeliveryRevocation{
			OperationID: op.ID.Value(),
			Digest:      op.Digest,
			RevokedAt:   c.now().UnixNano(),
			Reason:      req.Reason,
		}
		if err := validateDeliveryAuthorization(revoked); err != nil {
			return Aggregate{}, DeliveryRecord{}, err
		}
		rec2 := rec.clone()
		rec2.Authorizations[len(rec2.Authorizations)-1] = revoked
		if err := validateDeliveryRecord(rec2); err != nil {
			return Aggregate{}, DeliveryRecord{}, err
		}
		return next, rec2, nil
	})
	if err != nil {
		return DeliveryAuthorizationResult{}, err
	}
	if replayed {
		auth, ok := findAuthorizationByRevocation(rec, op.ID.Value())
		if !ok {
			return DeliveryAuthorizationResult{}, internalError("replay of delivery revocation %s cannot reconstruct the committed record", op.ID.Value())
		}
		return DeliveryAuthorizationResult{Authorization: auth, Replayed: true}, nil
	}
	return DeliveryAuthorizationResult{Authorization: rec.Authorizations[len(rec.Authorizations)-1]}, nil
}

// CommitDeliveryOutcome commits a closed-set truthful delivery outcome bound
// to the exact journal operation, the current authorization identity, the
// task generation, the provider/repository evidence, the detail
// classification, and the commit time. The mutation is
// exact-generation/revision fenced and verifies the authorization identity and
// currency prerequisites appropriate at commit (the bound worktree head may
// have legitimately moved during execution, so the identity-head check is not
// part of the commit prerequisite). Same Operation ID + digest replays;
// a distinct incompatible outcome conflicts; prior outcome evidence is
// preserved.
func (c *Canonical) CommitDeliveryOutcome(op domain.Operation, req CanonicalDeliveryOutcomeRequest) (DeliveryOutcomeResult, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return DeliveryOutcomeResult{}, err
	}
	if !req.Status.Valid() {
		return DeliveryOutcomeResult{}, validationError("invalid delivery outcome status %q", req.Status)
	}
	if strings.TrimSpace(req.Detail) == "" {
		return DeliveryOutcomeResult{}, validationError("delivery outcome requires a detail classification")
	}
	if req.HeadSHA != "" && !safeSHAValue(req.HeadSHA) {
		return DeliveryOutcomeResult{}, validationError("delivery outcome head SHA must be a safe value")
	}
	if req.MergedSHA != "" && !safeSHAValue(req.MergedSHA) {
		return DeliveryOutcomeResult{}, validationError("delivery outcome merged SHA must be a safe value")
	}
	if req.AuthorizationOperationID == "" || strings.ContainsAny(req.AuthorizationOperationID, `/\\`) {
		return DeliveryOutcomeResult{}, validationError("delivery outcome requires the exact authorization operation identity")
	}
	_, rec, replayed, err := c.mutateDelivery(op, req.TaskID, req.Precondition, func(cur Aggregate, rec DeliveryRecord) (Aggregate, DeliveryRecord, error) {
		auth := currentDeliveryAuthorization(rec)
		if auth == nil {
			return Aggregate{}, DeliveryRecord{}, conflictError(ErrConflict, "task %s has no active delivery authorization to commit an outcome against", cur.TaskID)
		}
		if auth.OperationID != req.AuthorizationOperationID {
			return Aggregate{}, DeliveryRecord{}, conflictError(ErrConflict, "delivery outcome authorization identity mismatch: current %q vs requested %q", auth.OperationID, req.AuthorizationOperationID)
		}
		holds, err := c.listHolds()
		if err != nil {
			return Aggregate{}, DeliveryRecord{}, err
		}
		for _, o := range rec.Outcomes {
			if o.Status.terminal() {
				return Aggregate{}, DeliveryRecord{}, conflictError(ErrConflict, "task %s already committed terminal delivery outcome %q; a distinct outcome conflicts", cur.TaskID, o.Status)
			}
		}
		if reasons := c.authorizationCurrencyReasons(cur, *auth, holds, false); len(reasons) > 0 {
			return Aggregate{}, DeliveryRecord{}, preconditionError("delivery authorization is not current for task %s: %v", cur.TaskID, reasons)
		}
		next := cur.clone()
		next.Revision++
		out := DeliveryOutcome{
			TaskID:                   cur.TaskID,
			Generation:               cur.Generation,
			AuthorizationOperationID: auth.OperationID,
			OperationID:              op.ID.Value(),
			Digest:                   op.Digest,
			Status:                   req.Status,
			Detail:                   strings.TrimSpace(req.Detail),
			HeadSHA:                  req.HeadSHA,
			MergedSHA:                req.MergedSHA,
			CommittedAt:              c.now().UnixNano(),
		}
		if err := validateDeliveryOutcome(out); err != nil {
			return Aggregate{}, DeliveryRecord{}, err
		}
		rec2 := rec.clone()
		rec2.Outcomes = append(rec2.Outcomes, out)
		if err := validateDeliveryRecord(rec2); err != nil {
			return Aggregate{}, DeliveryRecord{}, err
		}
		return next, rec2, nil
	})
	if err != nil {
		return DeliveryOutcomeResult{}, err
	}
	if replayed {
		out, ok := findOutcome(rec, op.ID.Value())
		if !ok {
			return DeliveryOutcomeResult{}, internalError("replay of delivery outcome %s cannot reconstruct the committed record", op.ID.Value())
		}
		return DeliveryOutcomeResult{Outcome: out, Replayed: true}, nil
	}
	return DeliveryOutcomeResult{Outcome: rec.Outcomes[len(rec.Outcomes)-1]}, nil
}

// authorizationCurrencyReasons re-derives the current validity of one issued
// authorization against current task state. checkHead=false is used by the
// outcome commit, where the delivery execution has legitimately moved the
// bound repository head (a successful mutation is never uncommittable because
// the head advanced); the currency read uses checkHead=true.
func (c *Canonical) authorizationCurrencyReasons(agg Aggregate, auth DeliveryAuthorization, holds []DispatchHold, checkHead bool) []DeliveryCurrencyReason {
	var reasons []DeliveryCurrencyReason
	if auth.Generation != agg.Generation {
		reasons = append(reasons, DeliveryCurrencyGeneration)
	}
	if auth.Revision != agg.Revision {
		reasons = append(reasons, DeliveryCurrencyRevision)
	}
	if auth.Phase != agg.Phase {
		reasons = append(reasons, DeliveryCurrencyPhase)
	}
	if strings.TrimSpace(agg.Definition.Owner) == "" || agg.Definition.Owner != auth.Owner {
		reasons = append(reasons, DeliveryCurrencyMissingOwner)
	}
	if agg.Endpoint == nil || agg.Worktree == nil {
		reasons = append(reasons, DeliveryCurrencyMissingBindings)
	} else if deliveryBindingDigest(*agg.Endpoint, *agg.Worktree) != auth.BindingDigest {
		reasons = append(reasons, DeliveryCurrencyBindingDigest)
	}
	if activeReservation(agg.Transfer) {
		reasons = append(reasons, DeliveryCurrencyReservation)
	}
	if holdsBlockAction(holds, DispatchActionDelivery, agg) {
		reasons = append(reasons, DeliveryCurrencyMatchingHold)
	}
	if deliveryHoldsDigest(holds, agg) != auth.HoldsDigest {
		reasons = append(reasons, DeliveryCurrencyHoldsDigest)
	}
	if checkHead && agg.Worktree != nil && agg.Worktree.Head != auth.Identity.HeadSHA {
		reasons = append(reasons, DeliveryCurrencyIdentityHead)
	}
	return reasons
}

// evaluateDeliveryCurrency composes the currency report for one task's
// current delivery authorization. It is a pure read: it never mutates state
// and never creates receipts.
func (c *Canonical) evaluateDeliveryCurrency(agg Aggregate, rec DeliveryRecord, holds []DispatchHold) DeliveryCurrency {
	cur := DeliveryCurrency{
		TaskID:           agg.TaskID,
		Generation:       agg.Generation,
		Revision:         agg.Revision,
		Phase:            agg.Phase,
		Current:          agg.Current,
		TransferReserved: activeReservation(agg.Transfer),
		HoldsDigest:      deliveryHoldsDigest(holds, agg),
	}
	if agg.Endpoint != nil && agg.Worktree != nil {
		cur.BindingDigest = deliveryBindingDigest(*agg.Endpoint, *agg.Worktree)
	}
	if len(rec.Outcomes) > 0 {
		outcome := rec.Outcomes[len(rec.Outcomes)-1].clone()
		cur.Outcome = &outcome
	}
	if !agg.Current {
		cur.Reasons = []DeliveryCurrencyReason{DeliveryCurrencyNotCurrent}
		return cur
	}
	if len(rec.Authorizations) == 0 {
		cur.Reasons = []DeliveryCurrencyReason{DeliveryCurrencyNoAuthorization}
		return cur
	}
	a := rec.Authorizations[len(rec.Authorizations)-1].clone()
	cur.Authorization = &a
	identity := a.Identity
	cur.Identity = &identity
	if a.Revoked != nil {
		cur.Reasons = []DeliveryCurrencyReason{DeliveryCurrencyRevoked}
		return cur
	}
	cur.Reasons = c.authorizationCurrencyReasons(agg, a, holds, true)
	cur.Valid = len(cur.Reasons) == 0
	return cur
}

// DeliveryAuthorization returns the current (last) delivery authorization of
// the task, revoked or active. It fails closed with ErrNotFound when the task
// has no authorization record.
func (c *Canonical) DeliveryAuthorization(taskID domain.TaskID) (DeliveryAuthorization, error) {
	if err := taskID.Validate(); err != nil {
		return DeliveryAuthorization{}, err
	}
	rec, exists, err := c.readDeliveryRecord(taskID.Value())
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	if !exists || len(rec.Authorizations) == 0 {
		return DeliveryAuthorization{}, conflictError(ErrNotFound, "task %s has no delivery authorization", taskID.Value())
	}
	return rec.Authorizations[len(rec.Authorizations)-1].clone(), nil
}

// DeliveryAuthorizationByOperation returns the identified delivery
// authorization by its operation identity (active or revoked), preserving the
// auditable prior record across revoke/re-authorize flows.
func (c *Canonical) DeliveryAuthorizationByOperation(taskID domain.TaskID, operationID string) (DeliveryAuthorization, error) {
	if err := taskID.Validate(); err != nil {
		return DeliveryAuthorization{}, err
	}
	if operationID == "" || strings.ContainsAny(operationID, `/\\`) {
		return DeliveryAuthorization{}, validationError("authorization operation identity must be a safe non-empty value")
	}
	rec, exists, err := c.readDeliveryRecord(taskID.Value())
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	if !exists {
		return DeliveryAuthorization{}, conflictError(ErrNotFound, "task %s has no delivery authorization", taskID.Value())
	}
	for i := range rec.Authorizations {
		if rec.Authorizations[i].OperationID == operationID {
			return rec.Authorizations[i].clone(), nil
		}
	}
	return DeliveryAuthorization{}, conflictError(ErrNotFound, "task %s has no delivery authorization %s", taskID.Value(), operationID)
}

// DeliveryOutcome returns the current (last) committed delivery outcome of
// the task. It fails closed with ErrNotFound when the task has no outcome.
func (c *Canonical) DeliveryOutcome(taskID domain.TaskID) (DeliveryOutcome, error) {
	if err := taskID.Validate(); err != nil {
		return DeliveryOutcome{}, err
	}
	rec, exists, err := c.readDeliveryRecord(taskID.Value())
	if err != nil {
		return DeliveryOutcome{}, err
	}
	if !exists || len(rec.Outcomes) == 0 {
		return DeliveryOutcome{}, conflictError(ErrNotFound, "task %s has no delivery outcome", taskID.Value())
	}
	return rec.Outcomes[len(rec.Outcomes)-1].clone(), nil
}

// DeliveryCurrency evaluates the currency of the task's current delivery
// authorization against current state. It is a narrow read-only method: it
// recomputes generation/revision/phase/currentness, transfer reservation,
// delivery-holds digest, binding digest, authorization status, and
// identity/head, and returns typed valid/invalid reasons. It never mutates
// state and never creates receipts.
func (c *Canonical) DeliveryCurrency(taskID domain.TaskID) (DeliveryCurrency, error) {
	if err := taskID.Validate(); err != nil {
		return DeliveryCurrency{}, err
	}
	doc, exists, err := c.readTaskDoc(taskID.Value())
	if err != nil {
		return DeliveryCurrency{}, err
	}
	if !exists {
		return DeliveryCurrency{TaskID: taskID.Value(), Reasons: []DeliveryCurrencyReason{DeliveryCurrencyNotFound}}, nil
	}
	holds, err := c.listHolds()
	if err != nil {
		return DeliveryCurrency{}, err
	}
	rec, _, err := c.readDeliveryRecord(taskID.Value())
	if err != nil {
		return DeliveryCurrency{}, err
	}
	return c.evaluateDeliveryCurrency(doc.Aggregate, rec, holds), nil
}
