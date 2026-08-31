package taskauthority

import (
	"bytes"
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
// Task Authority.
//
// Storage is the deep-module evidence pattern: every authorization issuance,
// revocation, and outcome is an immutable canonical document keyed by the
// exact operation identity (Task ID + operation ID), and a bounded per-task
// delivery index document holds only the current pointers/status needed for
// narrow current reads (active authorization identity, latest revocation and
// outcome identities, the terminal marker, and the schema/task identity).
// Retryable -> revoke -> reauthorize cycles and generation changes therefore
// grow only the immutable evidence directories, never one history document.
//
// Every delivery mutation is an exact-generation/revision-fenced task-scoped
// mutation that commits the advanced Task revision, the immutable evidence
// document(s), the bounded index update, and the operation receipt atomically
// through home.Commit. Same Operation ID + digest replay reconstructs the
// exact original result from the immutable evidence; a changed digest
// conflicts. Missing, substituted, or malformed evidence fails closed.

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

// DeliveryAuthorization is the immutable issuance evidence document for one
// authorization, keyed by Task ID + authorization Operation ID. It durably
// binds:
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
// The document is immutable once committed; revocation evidence is a separate
// immutable document bound to the authorization Operation identity.
type DeliveryAuthorization struct {
	SchemaVersion string                    `json:"schema_version"`
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
}

// clone deep-copies the record so committed evidence is never aliased.
func (a DeliveryAuthorization) clone() DeliveryAuthorization {
	out := a
	if a.ExpectedState != nil {
		es := *a.ExpectedState
		out.ExpectedState = &es
	}
	out.Preconditions = append([]DeliveryPrecondition(nil), a.Preconditions...)
	return out
}

// DeliveryRevocation is the immutable revocation evidence document for one
// authorization, keyed by Task ID + revocation Operation ID. It preserves the
// revoking canonical mutation Operation identity, the exact authorization it
// revokes, and the revocation timestamp; the authorization issuance document
// is never rewritten.
type DeliveryRevocation struct {
	SchemaVersion            string `json:"schema_version"`
	TaskID                   string `json:"task_id"`
	AuthorizationOperationID string `json:"authorization_operation_id"`
	OperationID              string `json:"operation_id"`
	Digest                   string `json:"digest"`
	RevokedAt                int64  `json:"revoked_at"`
	Reason                   string `json:"reason"`
}

// clone returns a deep copy of the revocation evidence.
func (r DeliveryRevocation) clone() DeliveryRevocation { return r }

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

// DeliveryOutcome is the immutable outcome evidence document for one delivery
// execution, keyed by Task ID + outcome Operation ID. It binds the exact
// journal operation, the authorization identity the delivery executed under,
// the task generation, the provider/repository evidence (head/merged SHA as
// applicable), the detail classification, and the commit time.
type DeliveryOutcome struct {
	SchemaVersion            string                `json:"schema_version"`
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

// clone returns a deep copy of the outcome evidence.
func (o DeliveryOutcome) clone() DeliveryOutcome { return o }

// DeliveryIndex is the bounded per-task delivery current document. It is
// finite-size by construction (no history slices): it carries only the schema
// and task identity, the latest issued authorization operation identity, the
// latest revocation operation identity, the latest outcome operation
// identity, and the terminal marker. Current reads resolve these pointers to
// the immutable evidence documents; the index never grows with history.
type DeliveryIndex struct {
	SchemaVersion     string `json:"schema_version"`
	TaskID            string `json:"task_id"`
	AuthorizationOpID string `json:"authorization_op_id,omitempty"`
	RevocationOpID    string `json:"revocation_op_id,omitempty"`
	OutcomeOpID       string `json:"outcome_op_id,omitempty"`
	Terminal          bool   `json:"terminal,omitempty"`
}

// Canonical storage layout: the bounded index and the immutable evidence
// documents live under the canonical state root beside task documents and
// holds, keyed per task and per exact operation identity.
const deliveryDir = "task-authority/delivery"

func deliveryCurrentKey(taskID string) string {
	return deliveryDir + "/" + taskID + "/current.json"
}
func deliveryAuthorizationKey(taskID, opID string) string {
	return deliveryDir + "/" + taskID + "/authorizations/" + opID + ".json"
}
func deliveryRevocationKey(taskID, opID string) string {
	return deliveryDir + "/" + taskID + "/revocations/" + opID + ".json"
}
func deliveryOutcomeKey(taskID, opID string) string {
	return deliveryDir + "/" + taskID + "/outcomes/" + opID + ".json"
}

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
// issuance.
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

// DeliveryRevocationResult is the committed outcome of a revocation mutation.
type DeliveryRevocationResult struct {
	Revocation DeliveryRevocation
	Replayed   bool
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

// safeIdentityValue accepts a safe non-empty identity value (no path
// separators). The canonical Git fixtures carry full 40-hex object IDs;
// validation follows the existing domain delivery identity/SHA rules and
// never invents a hex-only rule.
func safeIdentityValue(s string) bool {
	return s != "" && s == strings.TrimSpace(s) && !strings.ContainsAny(s, `/\\`)
}

// safeSHAValue is safeIdentityValue for SHA evidence values.
func safeSHAValue(s string) bool { return safeIdentityValue(s) }

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

// validateDeliveryRevocation checks the immutable revocation evidence shape:
// the current schema identity, the task and revoked authorization operation
// identities, the revoking operation identity and digest, the timestamp, and
// the reason.
func validateDeliveryRevocation(r DeliveryRevocation) error {
	if r.SchemaVersion != TaskAuthoritySchema {
		return validationError("invalid delivery revocation schema %q", r.SchemaVersion)
	}
	if r.TaskID == "" || strings.ContainsAny(r.TaskID, `/\\`) {
		return validationError("delivery revocation missing safe task id")
	}
	if r.AuthorizationOperationID == "" || strings.ContainsAny(r.AuthorizationOperationID, `/\\`) {
		return validationError("delivery revocation missing safe authorization operation id")
	}
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

// validateDeliveryAuthorization checks one committed issuance evidence
// document shape and the kind-appropriate expected state.
func validateDeliveryAuthorization(a DeliveryAuthorization) error {
	if a.SchemaVersion != TaskAuthoritySchema {
		return validationError("invalid delivery authorization schema %q", a.SchemaVersion)
	}
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
	return nil
}

// validateDeliveryOutcome checks one committed outcome evidence document
// shape: the closed-set status, the bound journal and authorization
// identities, the detail classification, safe evidence SHAs when present, and
// the commit time.
func validateDeliveryOutcome(o DeliveryOutcome) error {
	if o.SchemaVersion != TaskAuthoritySchema {
		return validationError("invalid delivery outcome schema %q", o.SchemaVersion)
	}
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

// validateDeliveryIndex checks the bounded current document: the current
// schema identity, the task identity, safe operation pointers, and pointer
// coherence (a revocation or outcome pointer implies a current authorization
// pointer).
func validateDeliveryIndex(index DeliveryIndex) error {
	if index.SchemaVersion != TaskAuthoritySchema {
		return validationError("invalid delivery index schema %q", index.SchemaVersion)
	}
	if index.TaskID == "" || strings.ContainsAny(index.TaskID, `/\\`) {
		return validationError("delivery index missing safe task id")
	}
	if index.AuthorizationOpID != "" && !safeIdentityValue(index.AuthorizationOpID) {
		return validationError("delivery index has an unsafe authorization pointer")
	}
	if index.RevocationOpID != "" && !safeIdentityValue(index.RevocationOpID) {
		return validationError("delivery index has an unsafe revocation pointer")
	}
	if index.OutcomeOpID != "" && !safeIdentityValue(index.OutcomeOpID) {
		return validationError("delivery index has an unsafe outcome pointer")
	}
	if index.RevocationOpID != "" && index.AuthorizationOpID == "" {
		return validationError("delivery index has a revocation pointer without an authorization pointer")
	}
	if index.OutcomeOpID != "" && index.AuthorizationOpID == "" {
		return validationError("delivery index has an outcome pointer without an authorization pointer")
	}
	return nil
}

// readDeliveryIndex reads and strictly decodes the bounded current document.
// The decode is strict (unknown fields are rejected) so an unbounded or
// legacy-shaped current document fails closed with no compatibility fallback.
// Absence reports false; malformed state fails closed.
func (c *Canonical) readDeliveryIndex(taskID string) (DeliveryIndex, bool, error) {
	data, ok, err := c.readDoc(deliveryCurrentKey(taskID))
	if err != nil || !ok {
		return DeliveryIndex{}, ok, err
	}
	var index DeliveryIndex
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&index); err != nil {
		return DeliveryIndex{}, true, internalError("decode delivery index for task %s: %v", taskID, err)
	}
	if err := validateDeliveryIndex(index); err != nil {
		return DeliveryIndex{}, true, internalError("task %s has malformed delivery index: %v", taskID, err)
	}
	return index, true, nil
}

// readDeliveryAuthorization resolves one immutable issuance evidence document
// by its exact operation identity, validating task binding and shape. Missing
// or malformed evidence fails closed.
func (c *Canonical) readDeliveryAuthorization(taskID, opID string) (DeliveryAuthorization, bool, error) {
	data, ok, err := c.readDoc(deliveryAuthorizationKey(taskID, opID))
	if err != nil || !ok {
		return DeliveryAuthorization{}, ok, err
	}
	var auth DeliveryAuthorization
	if err := json.Unmarshal(data, &auth); err != nil {
		return DeliveryAuthorization{}, true, internalError("decode delivery authorization %s for task %s: %v", opID, taskID, err)
	}
	if err := validateDeliveryAuthorization(auth); err != nil {
		return DeliveryAuthorization{}, true, internalError("task %s has malformed delivery authorization %s: %v", taskID, opID, err)
	}
	if auth.TaskID != taskID {
		return DeliveryAuthorization{}, true, internalError("task %s delivery authorization %s is bound to a different task", taskID, opID)
	}
	return auth, true, nil
}

// readDeliveryRevocation resolves one immutable revocation evidence document
// by its exact operation identity, validating task binding and shape.
func (c *Canonical) readDeliveryRevocation(taskID, opID string) (DeliveryRevocation, bool, error) {
	data, ok, err := c.readDoc(deliveryRevocationKey(taskID, opID))
	if err != nil || !ok {
		return DeliveryRevocation{}, ok, err
	}
	var rev DeliveryRevocation
	if err := json.Unmarshal(data, &rev); err != nil {
		return DeliveryRevocation{}, true, internalError("decode delivery revocation %s for task %s: %v", opID, taskID, err)
	}
	if err := validateDeliveryRevocation(rev); err != nil {
		return DeliveryRevocation{}, true, internalError("task %s has malformed delivery revocation %s: %v", taskID, opID, err)
	}
	if rev.TaskID != taskID {
		return DeliveryRevocation{}, true, internalError("task %s delivery revocation %s is bound to a different task", taskID, opID)
	}
	return rev, true, nil
}

// readDeliveryOutcome resolves one immutable outcome evidence document by its
// exact operation identity, validating task binding and shape.
func (c *Canonical) readDeliveryOutcome(taskID, opID string) (DeliveryOutcome, bool, error) {
	data, ok, err := c.readDoc(deliveryOutcomeKey(taskID, opID))
	if err != nil || !ok {
		return DeliveryOutcome{}, ok, err
	}
	var out DeliveryOutcome
	if err := json.Unmarshal(data, &out); err != nil {
		return DeliveryOutcome{}, true, internalError("decode delivery outcome %s for task %s: %v", opID, taskID, err)
	}
	if err := validateDeliveryOutcome(out); err != nil {
		return DeliveryOutcome{}, true, internalError("task %s has malformed delivery outcome %s: %v", taskID, opID, err)
	}
	if out.TaskID != taskID {
		return DeliveryOutcome{}, true, internalError("task %s delivery outcome %s is bound to a different task", taskID, opID)
	}
	return out, true, nil
}

// mutateDelivery runs one task-scoped delivery mutation: receipt idempotency
// first (replay reconstructs the exact original result from the immutable
// evidence), then the exact generation/revision precondition, currentness,
// and transfer reservation fences, then one atomic home.Commit that writes the
// advanced task document, the immutable evidence document(s), the bounded
// index update, and the operation receipt together. Delivery mutations
// advance the Task revision exactly once, so the recorded post-issuance
// revision is the authoritative revision written by the issuance commit.
func (c *Canonical) mutateDelivery(op domain.Operation, taskID domain.TaskID, prec domain.Precondition, apply func(Aggregate, DeliveryIndex) (Aggregate, DeliveryIndex, []home.ChangeItem, error)) (DeliveryIndex, bool, error) {
	if err := op.Validate(); err != nil {
		return DeliveryIndex{}, false, err
	}
	if err := prec.Validate(); err != nil {
		return DeliveryIndex{}, false, err
	}
	lk, err := c.h.Lock(taskScope(taskID.Value()))
	if err != nil {
		return DeliveryIndex{}, false, err
	}
	defer lk.Release()

	if _, ok, err := c.checkedReceipt(op); err != nil {
		return DeliveryIndex{}, false, err
	} else if ok {
		index, exists, err := c.readDeliveryIndex(taskID.Value())
		if err != nil || !exists {
			return DeliveryIndex{}, false, internalError("replay of %s cannot reconstruct the delivery index", op.ID.Value())
		}
		return index, true, nil
	}

	doc, exists, err := c.readTaskDoc(taskID.Value())
	if err != nil {
		return DeliveryIndex{}, false, err
	}
	if !exists {
		return DeliveryIndex{}, false, conflictError(ErrNotFound, "task %s not found", taskID.Value())
	}
	if err := verifyPrecondition(taskID, doc.Aggregate, prec); err != nil {
		return DeliveryIndex{}, false, err
	}
	if err := c.checkMutableCurrent(doc.Aggregate); err != nil {
		return DeliveryIndex{}, false, err
	}
	if err := c.checkReservationFence(doc.Aggregate, nil); err != nil {
		return DeliveryIndex{}, false, err
	}
	// Delivery state (authorization/revocation/outcome) is task-scoped
	// lifecycle state and must not mutate while a cleanup claim is active
	// (BEO-16/P1a): the claim's promised serialization covers delivery too,
	// so the revision snapshot cleanup revalidates against cannot move.
	if err := c.checkCleanupFence(doc.Aggregate, nil); err != nil {
		return DeliveryIndex{}, false, err
	}
	index, _, err := c.readDeliveryIndex(taskID.Value())
	if err != nil {
		return DeliveryIndex{}, false, err
	}
	nextAgg, nextIndex, evidence, err := apply(doc.Aggregate, index)
	if err != nil {
		return DeliveryIndex{}, false, err
	}
	if nextIndex.SchemaVersion == "" {
		nextIndex.SchemaVersion = TaskAuthoritySchema
		nextIndex.TaskID = taskID.Value()
	}
	if err := validateDeliveryIndex(nextIndex); err != nil {
		return DeliveryIndex{}, false, err
	}

	newDoc := taskDoc{HomeRevision: doc.HomeRevision + 1, Aggregate: nextAgg}
	rec := receiptFor(op, nextAgg)
	docData, err := json.Marshal(newDoc)
	if err != nil {
		return DeliveryIndex{}, false, err
	}
	indexData, err := json.Marshal(nextIndex)
	if err != nil {
		return DeliveryIndex{}, false, err
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		return DeliveryIndex{}, false, err
	}
	items := []home.ChangeItem{
		{Root: canonicalRoot, Key: taskCurrentKey(taskID.Value()), Data: docData},
		{Root: canonicalRoot, Key: deliveryCurrentKey(taskID.Value()), Data: indexData},
		{Root: canonicalRoot, Key: receiptKey(rec.OperationID), Data: recData},
	}
	items = append(items, evidence...)
	if _, err := c.h.Commit(lk, op.ID.Value(), doc.HomeRevision, items); err != nil {
		return DeliveryIndex{}, false, commitError(taskID, prec, err)
	}
	return nextIndex, false, nil
}

// deliveryRevoked reports whether the latest issued authorization (per the
// bounded index pointer) carries a committed revocation evidence document.
func (c *Canonical) deliveryRevoked(taskID string, index DeliveryIndex) (bool, error) {
	if index.RevocationOpID == "" {
		return false, nil
	}
	rev, ok, err := c.readDeliveryRevocation(taskID, index.RevocationOpID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, internalError("task %s delivery index points at missing revocation %s", taskID, index.RevocationOpID)
	}
	return rev.AuthorizationOperationID == index.AuthorizationOpID, nil
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
// #414 irreversible operation kind, committing an immutable issuance evidence
// document keyed by Task ID + authorization Operation ID and updating the
// bounded index pointer. Issuance requires a current working task with owner
// and the exact bindings required by the kind, no matching active delivery
// hold, no active transfer reservation, no terminal committed outcome, no
// already-active authorization, a valid typed identity whose head matches the
// bound worktree head, and valid preconditions; it fails closed otherwise.
// Repeating the same Operation ID with the same digest replays the durable
// prior record; reusing the Operation ID with a different intent conflicts.
func (c *Canonical) AuthorizeDelivery(op domain.Operation, req CanonicalDeliveryAuthorizationRequest) (DeliveryAuthorizationResult, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return DeliveryAuthorizationResult{}, err
	}
	if err := validateDeliveryAuthorizationRequest(req); err != nil {
		return DeliveryAuthorizationResult{}, err
	}
	var committed DeliveryAuthorization
	_, replayed, err := c.mutateDelivery(op, req.TaskID, req.Precondition, func(cur Aggregate, index DeliveryIndex) (Aggregate, DeliveryIndex, []home.ChangeItem, error) {
		if cur.Phase != PhaseWorking {
			return Aggregate{}, DeliveryIndex{}, nil, preconditionError("delivery authorization requires a working task; task %s is %s", cur.TaskID, cur.Phase)
		}
		if strings.TrimSpace(cur.Definition.Owner) == "" {
			return Aggregate{}, DeliveryIndex{}, nil, preconditionError("delivery authorization requires an owner for task %s", cur.TaskID)
		}
		if cur.Worktree == nil || cur.Endpoint == nil {
			return Aggregate{}, DeliveryIndex{}, nil, preconditionError("delivery authorization requires the bound worktree and endpoint of task %s", cur.TaskID)
		}
		if cur.Worktree.Head != req.Identity.HeadSHA {
			return Aggregate{}, DeliveryIndex{}, nil, preconditionError("delivery authorization identity head %q does not match the bound worktree head %q", req.Identity.HeadSHA, cur.Worktree.Head)
		}
		holds, err := c.listHolds()
		if err != nil {
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		if holdsBlockAction(holds, DispatchActionDelivery, cur) {
			return Aggregate{}, DeliveryIndex{}, nil, conflictError(ErrDispatchHeld, "%s: delivery is held for task %s", ErrDispatchHeld, cur.TaskID)
		}
		if index.Terminal {
			return Aggregate{}, DeliveryIndex{}, nil, conflictError(ErrConflict, "task %s already committed a terminal delivery outcome; a new delivery authorization conflicts", cur.TaskID)
		}
		if index.AuthorizationOpID != "" {
			revoked, err := c.deliveryRevoked(cur.TaskID, index)
			if err != nil {
				return Aggregate{}, DeliveryIndex{}, nil, err
			}
			if !revoked {
				return Aggregate{}, DeliveryIndex{}, nil, conflictError(ErrConflict, "task %s already has an active delivery authorization; revoke it before issuing a new one", cur.TaskID)
			}
		}
		next := cur.clone()
		next.Revision++
		auth := DeliveryAuthorization{
			SchemaVersion: TaskAuthoritySchema,
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
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		committed = auth
		data, err := json.Marshal(auth)
		if err != nil {
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		nextIndex := index
		nextIndex.AuthorizationOpID = op.ID.Value()
		evidence := []home.ChangeItem{{Root: canonicalRoot, Key: deliveryAuthorizationKey(cur.TaskID, op.ID.Value()), Data: data}}
		return next, nextIndex, evidence, nil
	})
	if err != nil {
		return DeliveryAuthorizationResult{}, err
	}
	if replayed {
		auth, ok, err := c.readDeliveryAuthorization(req.TaskID.Value(), op.ID.Value())
		if err != nil {
			return DeliveryAuthorizationResult{}, err
		}
		if !ok {
			return DeliveryAuthorizationResult{}, internalError("replay of delivery authorization %s cannot reconstruct the committed evidence", op.ID.Value())
		}
		return DeliveryAuthorizationResult{Authorization: auth, Replayed: true}, nil
	}
	return DeliveryAuthorizationResult{Authorization: committed}, nil
}

// RevokeDeliveryAuthorization revokes the active authorization under its
// exact operation identity, fenced to the exact generation/revision
// precondition. It commits an immutable revocation evidence document keyed by
// Task ID + revocation Operation ID and updates the bounded index revocation
// pointer; the issuance evidence document is never rewritten. Same Operation
// ID + digest replay is idempotent; a changed intent conflicts. Revocation
// preserves the prior authorization evidence and permits a later distinct
// authorization only through a new canonical issuance.
func (c *Canonical) RevokeDeliveryAuthorization(op domain.Operation, req CanonicalRevokeDeliveryRequest) (DeliveryRevocationResult, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return DeliveryRevocationResult{}, err
	}
	if !safeIdentityValue(req.AuthorizationOperationID) {
		return DeliveryRevocationResult{}, validationError("revocation requires the exact authorization operation identity")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return DeliveryRevocationResult{}, validationError("revocation requires a reason")
	}
	var committed DeliveryRevocation
	_, replayed, err := c.mutateDelivery(op, req.TaskID, req.Precondition, func(cur Aggregate, index DeliveryIndex) (Aggregate, DeliveryIndex, []home.ChangeItem, error) {
		if index.AuthorizationOpID == "" {
			return Aggregate{}, DeliveryIndex{}, nil, conflictError(ErrConflict, "task %s has no active delivery authorization to revoke", cur.TaskID)
		}
		if index.AuthorizationOpID != req.AuthorizationOperationID {
			return Aggregate{}, DeliveryIndex{}, nil, conflictError(ErrConflict, "delivery authorization identity mismatch: current %q vs requested %q", index.AuthorizationOpID, req.AuthorizationOperationID)
		}
		revoked, err := c.deliveryRevoked(cur.TaskID, index)
		if err != nil {
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		if revoked {
			return Aggregate{}, DeliveryIndex{}, nil, conflictError(ErrConflict, "delivery authorization %q is already revoked", index.AuthorizationOpID)
		}
		next := cur.clone()
		next.Revision++
		revocation := DeliveryRevocation{
			SchemaVersion:            TaskAuthoritySchema,
			TaskID:                   cur.TaskID,
			AuthorizationOperationID: index.AuthorizationOpID,
			OperationID:              op.ID.Value(),
			Digest:                   op.Digest,
			RevokedAt:                c.now().UnixNano(),
			Reason:                   req.Reason,
		}
		if err := validateDeliveryRevocation(revocation); err != nil {
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		committed = revocation
		data, err := json.Marshal(revocation)
		if err != nil {
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		nextIndex := index
		nextIndex.RevocationOpID = op.ID.Value()
		evidence := []home.ChangeItem{{Root: canonicalRoot, Key: deliveryRevocationKey(cur.TaskID, op.ID.Value()), Data: data}}
		return next, nextIndex, evidence, nil
	})
	if err != nil {
		return DeliveryRevocationResult{}, err
	}
	if replayed {
		revocation, ok, err := c.readDeliveryRevocation(req.TaskID.Value(), op.ID.Value())
		if err != nil {
			return DeliveryRevocationResult{}, err
		}
		if !ok {
			return DeliveryRevocationResult{}, internalError("replay of delivery revocation %s cannot reconstruct the committed evidence", op.ID.Value())
		}
		return DeliveryRevocationResult{Revocation: revocation, Replayed: true}, nil
	}
	return DeliveryRevocationResult{Revocation: committed}, nil
}

// CommitDeliveryOutcome commits a closed-set truthful delivery outcome as an
// immutable evidence document keyed by Task ID + outcome Operation ID, bound
// to the exact journal operation and the current authorization identity, and
// updates the bounded index outcome pointer and terminal marker. The mutation
// is exact-generation/revision fenced and verifies the authorization identity
// and currency prerequisites appropriate at commit (the bound worktree head
// may have legitimately moved during execution, so the identity-head check is
// not part of the commit prerequisite). Same Operation ID + digest replays;
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
	if !safeIdentityValue(req.AuthorizationOperationID) {
		return DeliveryOutcomeResult{}, validationError("delivery outcome requires the exact authorization operation identity")
	}
	var committed DeliveryOutcome
	_, replayed, err := c.mutateDelivery(op, req.TaskID, req.Precondition, func(cur Aggregate, index DeliveryIndex) (Aggregate, DeliveryIndex, []home.ChangeItem, error) {
		if index.AuthorizationOpID == "" {
			return Aggregate{}, DeliveryIndex{}, nil, conflictError(ErrConflict, "task %s has no active delivery authorization to commit an outcome against", cur.TaskID)
		}
		if index.AuthorizationOpID != req.AuthorizationOperationID {
			return Aggregate{}, DeliveryIndex{}, nil, conflictError(ErrConflict, "delivery outcome authorization identity mismatch: current %q vs requested %q", index.AuthorizationOpID, req.AuthorizationOperationID)
		}
		revoked, err := c.deliveryRevoked(cur.TaskID, index)
		if err != nil {
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		if revoked {
			return Aggregate{}, DeliveryIndex{}, nil, conflictError(ErrConflict, "task %s delivery authorization is revoked; no outcome can be committed against it", cur.TaskID)
		}
		if index.Terminal {
			return Aggregate{}, DeliveryIndex{}, nil, conflictError(ErrConflict, "task %s already committed a terminal delivery outcome; a distinct outcome conflicts", cur.TaskID)
		}
		auth, ok, err := c.readDeliveryAuthorization(cur.TaskID, index.AuthorizationOpID)
		if err != nil {
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		if !ok {
			return Aggregate{}, DeliveryIndex{}, nil, internalError("task %s delivery index points at missing authorization %s", cur.TaskID, index.AuthorizationOpID)
		}
		holds, err := c.listHolds()
		if err != nil {
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		if reasons := c.authorizationCurrencyReasons(cur, auth, holds, false); len(reasons) > 0 {
			return Aggregate{}, DeliveryIndex{}, nil, preconditionError("delivery authorization is not current for task %s: %v", cur.TaskID, reasons)
		}
		next := cur.clone()
		next.Revision++
		out := DeliveryOutcome{
			SchemaVersion:            TaskAuthoritySchema,
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
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		committed = out
		data, err := json.Marshal(out)
		if err != nil {
			return Aggregate{}, DeliveryIndex{}, nil, err
		}
		nextIndex := index
		nextIndex.OutcomeOpID = op.ID.Value()
		nextIndex.Terminal = out.Status.terminal()
		evidence := []home.ChangeItem{{Root: canonicalRoot, Key: deliveryOutcomeKey(cur.TaskID, op.ID.Value()), Data: data}}
		return next, nextIndex, evidence, nil
	})
	if err != nil {
		return DeliveryOutcomeResult{}, err
	}
	if replayed {
		out, ok, err := c.readDeliveryOutcome(req.TaskID.Value(), op.ID.Value())
		if err != nil {
			return DeliveryOutcomeResult{}, err
		}
		if !ok {
			return DeliveryOutcomeResult{}, internalError("replay of delivery outcome %s cannot reconstruct the committed evidence", op.ID.Value())
		}
		return DeliveryOutcomeResult{Outcome: out, Replayed: true}, nil
	}
	return DeliveryOutcomeResult{Outcome: committed}, nil
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

// DeliveryAuthorization returns the current delivery authorization of the
// task by resolving the bounded index pointer to the immutable issuance
// evidence. It fails closed with ErrNotFound when the task has no
// authorization, and fails closed on missing, substituted, or malformed
// evidence.
func (c *Canonical) DeliveryAuthorization(taskID domain.TaskID) (DeliveryAuthorization, error) {
	if err := taskID.Validate(); err != nil {
		return DeliveryAuthorization{}, err
	}
	lk, err := c.h.Lock(taskScope(taskID.Value()))
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	defer lk.Release()
	if err := c.h.RecoverPending(lk); err != nil {
		return DeliveryAuthorization{}, err
	}
	index, _, err := c.readDeliveryIndex(taskID.Value())
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	if index.AuthorizationOpID == "" {
		return DeliveryAuthorization{}, conflictError(ErrNotFound, "task %s has no delivery authorization", taskID.Value())
	}
	auth, ok, err := c.readDeliveryAuthorization(taskID.Value(), index.AuthorizationOpID)
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	if !ok {
		return DeliveryAuthorization{}, internalError("task %s delivery index points at missing authorization %s", taskID.Value(), index.AuthorizationOpID)
	}
	return auth.clone(), nil
}

// DeliveryAuthorizationByOperation returns the immutable issuance evidence
// document identified by its exact operation identity (active or revoked),
// preserving the auditable prior record across revoke/re-authorize flows.
func (c *Canonical) DeliveryAuthorizationByOperation(taskID domain.TaskID, operationID string) (DeliveryAuthorization, error) {
	if err := taskID.Validate(); err != nil {
		return DeliveryAuthorization{}, err
	}
	if !safeIdentityValue(operationID) {
		return DeliveryAuthorization{}, validationError("authorization operation identity must be a safe non-empty value")
	}
	auth, ok, err := c.readDeliveryAuthorization(taskID.Value(), operationID)
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	if !ok {
		return DeliveryAuthorization{}, conflictError(ErrNotFound, "task %s has no delivery authorization %s", taskID.Value(), operationID)
	}
	return auth.clone(), nil
}

// DeliveryRevocationByOperation returns the immutable revocation evidence
// document identified by its exact operation identity.
func (c *Canonical) DeliveryRevocationByOperation(taskID domain.TaskID, operationID string) (DeliveryRevocation, error) {
	if err := taskID.Validate(); err != nil {
		return DeliveryRevocation{}, err
	}
	if !safeIdentityValue(operationID) {
		return DeliveryRevocation{}, validationError("revocation operation identity must be a safe non-empty value")
	}
	revocation, ok, err := c.readDeliveryRevocation(taskID.Value(), operationID)
	if err != nil {
		return DeliveryRevocation{}, err
	}
	if !ok {
		return DeliveryRevocation{}, conflictError(ErrNotFound, "task %s has no delivery revocation %s", taskID.Value(), operationID)
	}
	return revocation.clone(), nil
}

// DeliveryOutcome returns the current committed delivery outcome of the task
// by resolving the bounded index pointer to the immutable outcome evidence.
// It fails closed with ErrNotFound when the task has no outcome, and fails
// closed on missing, substituted, or malformed evidence.
func (c *Canonical) DeliveryOutcome(taskID domain.TaskID) (DeliveryOutcome, error) {
	if err := taskID.Validate(); err != nil {
		return DeliveryOutcome{}, err
	}
	lk, err := c.h.Lock(taskScope(taskID.Value()))
	if err != nil {
		return DeliveryOutcome{}, err
	}
	defer lk.Release()
	if err := c.h.RecoverPending(lk); err != nil {
		return DeliveryOutcome{}, err
	}
	index, _, err := c.readDeliveryIndex(taskID.Value())
	if err != nil {
		return DeliveryOutcome{}, err
	}
	if index.OutcomeOpID == "" {
		return DeliveryOutcome{}, conflictError(ErrNotFound, "task %s has no delivery outcome", taskID.Value())
	}
	out, ok, err := c.readDeliveryOutcome(taskID.Value(), index.OutcomeOpID)
	if err != nil {
		return DeliveryOutcome{}, err
	}
	if !ok {
		return DeliveryOutcome{}, internalError("task %s delivery index points at missing outcome %s", taskID.Value(), index.OutcomeOpID)
	}
	if index.Terminal != out.Status.terminal() {
		return DeliveryOutcome{}, internalError("task %s delivery index terminal marker is incoherent with outcome %s", taskID.Value(), index.OutcomeOpID)
	}
	return out.clone(), nil
}

// DeliveryOutcomeByOperation returns the immutable outcome evidence document
// identified by its exact operation identity.
func (c *Canonical) DeliveryOutcomeByOperation(taskID domain.TaskID, operationID string) (DeliveryOutcome, error) {
	if err := taskID.Validate(); err != nil {
		return DeliveryOutcome{}, err
	}
	if !safeIdentityValue(operationID) {
		return DeliveryOutcome{}, validationError("outcome operation identity must be a safe non-empty value")
	}
	out, ok, err := c.readDeliveryOutcome(taskID.Value(), operationID)
	if err != nil {
		return DeliveryOutcome{}, err
	}
	if !ok {
		return DeliveryOutcome{}, conflictError(ErrNotFound, "task %s has no delivery outcome %s", taskID.Value(), operationID)
	}
	return out.clone(), nil
}

// DeliveryCurrency evaluates the currency of the task's current delivery
// authorization against current state. It is a narrow read-only method: it
// resolves the bounded index pointers to the immutable evidence documents and
// recomputes generation/revision/phase/currentness, transfer reservation,
// delivery-holds digest, binding digest, authorization status, and
// identity/head, returning typed valid/invalid reasons. It never mutates
// state and never creates receipts. Missing, substituted, or malformed
// evidence fails closed.
func (c *Canonical) DeliveryCurrency(taskID domain.TaskID) (DeliveryCurrency, error) {
	if err := taskID.Validate(); err != nil {
		return DeliveryCurrency{}, err
	}
	lk, err := c.h.Lock(taskScope(taskID.Value()))
	if err != nil {
		return DeliveryCurrency{}, err
	}
	defer lk.Release()
	if err := c.h.RecoverPending(lk); err != nil {
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
	index, _, err := c.readDeliveryIndex(taskID.Value())
	if err != nil {
		return DeliveryCurrency{}, err
	}
	cur := DeliveryCurrency{
		TaskID:           doc.Aggregate.TaskID,
		Generation:       doc.Aggregate.Generation,
		Revision:         doc.Aggregate.Revision,
		Phase:            doc.Aggregate.Phase,
		Current:          doc.Aggregate.Current,
		TransferReserved: activeReservation(doc.Aggregate.Transfer),
		HoldsDigest:      deliveryHoldsDigest(holds, doc.Aggregate),
	}
	if doc.Aggregate.Endpoint != nil && doc.Aggregate.Worktree != nil {
		cur.BindingDigest = deliveryBindingDigest(*doc.Aggregate.Endpoint, *doc.Aggregate.Worktree)
	}
	if index.OutcomeOpID != "" {
		out, ok, err := c.readDeliveryOutcome(taskID.Value(), index.OutcomeOpID)
		if err != nil {
			return DeliveryCurrency{}, err
		}
		if !ok {
			return DeliveryCurrency{}, internalError("task %s delivery index points at missing outcome %s", taskID.Value(), index.OutcomeOpID)
		}
		if index.Terminal != out.Status.terminal() {
			return DeliveryCurrency{}, internalError("task %s delivery index terminal marker is incoherent with outcome %s", taskID.Value(), index.OutcomeOpID)
		}
		outcome := out.clone()
		cur.Outcome = &outcome
	}
	if !doc.Aggregate.Current {
		cur.Reasons = []DeliveryCurrencyReason{DeliveryCurrencyNotCurrent}
		return cur, nil
	}
	if index.AuthorizationOpID == "" {
		cur.Reasons = []DeliveryCurrencyReason{DeliveryCurrencyNoAuthorization}
		return cur, nil
	}
	auth, ok, err := c.readDeliveryAuthorization(taskID.Value(), index.AuthorizationOpID)
	if err != nil {
		return DeliveryCurrency{}, err
	}
	if !ok {
		return DeliveryCurrency{}, internalError("task %s delivery index points at missing authorization %s", taskID.Value(), index.AuthorizationOpID)
	}
	a := auth.clone()
	cur.Authorization = &a
	identity := a.Identity
	cur.Identity = &identity
	revoked, err := c.deliveryRevoked(taskID.Value(), index)
	if err != nil {
		return DeliveryCurrency{}, err
	}
	if revoked {
		cur.Reasons = []DeliveryCurrencyReason{DeliveryCurrencyRevoked}
		return cur, nil
	}
	cur.Reasons = c.authorizationCurrencyReasons(doc.Aggregate, a, holds, true)
	cur.Valid = len(cur.Reasons) == 0
	return cur, nil
}
