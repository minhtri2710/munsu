package taskauthority

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// The refusal branches of the delivery evidence validators. Same discipline as
// model_guards_test.go: the accepted fixture is asserted once per validator,
// each case breaks exactly one field, and every case asserts its own message —
// these validators refuse a dozen ways with one error type, so `err != nil`
// would not say which guard spoke.

// validAuthorization is a committed provider-merge issuance record the
// validator accepts.
func validAuthorization() DeliveryAuthorization {
	return DeliveryAuthorization{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        "t1",
		Generation:    1,
		Revision:      2,
		Phase:         PhaseWorking,
		Owner:         "captain-1",
		Kind:          DeliveryAuthorizationProviderMerge,
		Identity:      deliveryIdentity(),
		BindingDigest: testSHA256Hex,
		HoldsDigest:   testSHA256Hex,
		Preconditions: []DeliveryPrecondition{DeliveryPreconditionPRMergeable, DeliveryPreconditionPRHeadCurrent},
		OperationID:   "op-authorize-1",
		Digest:        testSHA256Hex,
		IssuedAt:      1700000000,
	}
}

// The issuance record binds a task generation at an exact revision and phase.
// Every one of those identity fields is what a later revocation or outcome is
// matched against.
func TestValidateDeliveryAuthorizationRefusesMalformedIdentity(t *testing.T) {
	runGuardCases(t, validAuthorization, validateDeliveryAuthorization, []guardCase[DeliveryAuthorization]{
		{"another schema version", func(a *DeliveryAuthorization) { a.SchemaVersion = "munsu.task-authority/v2" }, "invalid delivery authorization schema"},
		{"no task id", func(a *DeliveryAuthorization) { a.TaskID = "" }, "delivery authorization missing safe task id"},
		{"path-separating task id", func(a *DeliveryAuthorization) { a.TaskID = "../t1" }, "delivery authorization missing safe task id"},
		{"no revision", func(a *DeliveryAuthorization) { a.Revision = 0 }, "delivery authorization missing revision"},
		{"non-working phase", func(a *DeliveryAuthorization) { a.Phase = PhaseQueued }, "binds a non-working phase"},
		{"invalid phase", func(a *DeliveryAuthorization) { a.Phase = Phase("in-flight") }, "binds a non-working phase"},
		{"no owner", func(a *DeliveryAuthorization) { a.Owner = "  " }, "delivery authorization missing owner"},
		{"unknown kind", func(a *DeliveryAuthorization) { a.Kind = DeliveryAuthorizationKind("force-push") }, "has invalid kind"},
		{"unsafe head sha", func(a *DeliveryAuthorization) { a.Identity.HeadSHA = "refs/heads/main" }, "head SHA must be a safe non-empty value"},
		{"no operation id", func(a *DeliveryAuthorization) { a.OperationID = "" }, "delivery authorization missing operation id"},
		{"path-separating operation id", func(a *DeliveryAuthorization) { a.OperationID = "op/authorize" }, "delivery authorization missing operation id"},
		{"no issued timestamp", func(a *DeliveryAuthorization) { a.IssuedAt = 0 }, "delivery authorization missing issued timestamp"},
		{"negative issued timestamp", func(a *DeliveryAuthorization) { a.IssuedAt = -1 }, "delivery authorization missing issued timestamp"},
	})
}

// The three digests are what make the record evidence rather than a claim: the
// binding digest pins the endpoint/worktree lease, the holds digest the
// delivery holds, and the record digest the document itself.
func TestValidateDeliveryAuthorizationRefusesMalformedDigests(t *testing.T) {
	runGuardCases(t, validAuthorization, validateDeliveryAuthorization, []guardCase[DeliveryAuthorization]{
		{"binding digest is not sha256", func(a *DeliveryAuthorization) { a.BindingDigest = "binding" }, "binding digest must be a 64-hex sha256 digest"},
		{"binding digest is short hex", func(a *DeliveryAuthorization) { a.BindingDigest = testSHA256Hex[:63] }, "binding digest must be a 64-hex sha256 digest"},
		{"holds digest is not sha256", func(a *DeliveryAuthorization) { a.HoldsDigest = "holds" }, "holds digest must be a 64-hex sha256 digest"},
		{"record digest is not sha256", func(a *DeliveryAuthorization) { a.Digest = "digest" }, "delivery authorization digest must be a 64-hex sha256 digest"},
	})
}

// The preconditions are the closed set Fleet asserts it verified. An empty
// set, an unknown member, or a duplicate would all make the assertion
// unreadable.
func TestValidateDeliveryAuthorizationRefusesMalformedPreconditions(t *testing.T) {
	runGuardCases(t, validAuthorization, validateDeliveryAuthorization, []guardCase[DeliveryAuthorization]{
		{"no preconditions", func(a *DeliveryAuthorization) { a.Preconditions = nil }, "delivery authorization missing preconditions"},
		{"empty precondition slice", func(a *DeliveryAuthorization) { a.Preconditions = []DeliveryPrecondition{} }, "delivery authorization missing preconditions"},
		{"unknown precondition", func(a *DeliveryAuthorization) {
			a.Preconditions = []DeliveryPrecondition{DeliveryPrecondition("ci-green")}
		}, "has invalid precondition"},
		{"duplicate precondition", func(a *DeliveryAuthorization) {
			a.Preconditions = []DeliveryPrecondition{DeliveryPreconditionPRMergeable, DeliveryPreconditionPRMergeable}
		}, "has duplicate precondition"},
	})
}

// The issuance INTENT is validated before anything is committed, so the same
// closed-set and safe-identity rules apply to the request shape.
func TestValidateDeliveryAuthorizationRequestRefusesMalformedIntent(t *testing.T) {
	taskID, err := domain.NewTaskID("t1")
	if err != nil {
		t.Fatalf("NewTaskID: %v", err)
	}
	runGuardCases(t,
		func() CanonicalDeliveryAuthorizationRequest {
			return CanonicalDeliveryAuthorizationRequest{
				TaskID:        taskID,
				Precondition:  domain.Of(1, 2),
				Kind:          DeliveryAuthorizationProviderMerge,
				Identity:      deliveryIdentity(),
				Preconditions: []DeliveryPrecondition{DeliveryPreconditionPRMergeable},
			}
		},
		validateDeliveryAuthorizationRequest,
		[]guardCase[CanonicalDeliveryAuthorizationRequest]{
			{"unsafe head sha", func(req *CanonicalDeliveryAuthorizationRequest) { req.Identity.HeadSHA = "refs/heads/main" },
				"head SHA must be a safe non-empty value"},
			{"unknown precondition", func(req *CanonicalDeliveryAuthorizationRequest) {
				req.Preconditions = []DeliveryPrecondition{DeliveryPrecondition("ci-green")}
			}, "invalid delivery precondition"},
		})
}

// Revocation evidence is bound to the authorization it revokes. Without that
// binding, the reason, or the revoking operation's own identity, it cannot be
// read back as the authoritative reason an authorization stopped applying.
func TestValidateDeliveryRevocationRefusesMalformedEvidence(t *testing.T) {
	runGuardCases(t,
		func() DeliveryRevocation {
			return DeliveryRevocation{
				SchemaVersion:            TaskAuthoritySchema,
				TaskID:                   "t1",
				AuthorizationOperationID: "op-authorize-1",
				OperationID:              "op-revoke-1",
				Digest:                   testSHA256Hex,
				RevokedAt:                1700000000,
				Reason:                   "head moved",
			}
		},
		validateDeliveryRevocation,
		[]guardCase[DeliveryRevocation]{
			{"another schema version", func(r *DeliveryRevocation) { r.SchemaVersion = "munsu.task-authority/v2" }, "invalid delivery revocation schema"},
			{"no task id", func(r *DeliveryRevocation) { r.TaskID = "" }, "delivery revocation missing safe task id"},
			{"path-separating task id", func(r *DeliveryRevocation) { r.TaskID = "../t1" }, "delivery revocation missing safe task id"},
			{"no authorization operation id", func(r *DeliveryRevocation) { r.AuthorizationOperationID = "" }, "missing safe authorization operation id"},
			{"path-separating authorization operation id", func(r *DeliveryRevocation) { r.AuthorizationOperationID = "op/authorize" }, "missing safe authorization operation id"},
			{"no operation id", func(r *DeliveryRevocation) { r.OperationID = "" }, "delivery revocation missing operation id"},
			{"path-separating operation id", func(r *DeliveryRevocation) { r.OperationID = `op\revoke` }, "delivery revocation missing operation id"},
			{"digest is not sha256", func(r *DeliveryRevocation) { r.Digest = "digest" }, "delivery revocation digest must be a 64-hex sha256 digest"},
			{"no revoked timestamp", func(r *DeliveryRevocation) { r.RevokedAt = 0 }, "delivery revocation missing revoked timestamp"},
			{"negative revoked timestamp", func(r *DeliveryRevocation) { r.RevokedAt = -1 }, "delivery revocation missing revoked timestamp"},
			{"no reason", func(r *DeliveryRevocation) { r.Reason = "  " }, "delivery revocation missing reason"},
		})
}

// Outcome evidence records what a delivery operation actually did. Its status
// is a closed set, its detail is the classification a human reads, and its
// SHAs are optional but must be safe when present.
func TestValidateDeliveryOutcomeRefusesMalformedEvidence(t *testing.T) {
	runGuardCases(t,
		func() DeliveryOutcome {
			return DeliveryOutcome{
				SchemaVersion:            TaskAuthoritySchema,
				TaskID:                   "t1",
				Generation:               1,
				AuthorizationOperationID: "op-authorize-1",
				OperationID:              "op-outcome-1",
				Digest:                   testSHA256Hex,
				Status:                   DeliveryOutcomeCompleted,
				Detail:                   "merged by provider",
				HeadSHA:                  deliveryHead,
				MergedSHA:                deliveryHead,
				CommittedAt:              1700000000,
			}
		},
		validateDeliveryOutcome,
		[]guardCase[DeliveryOutcome]{
			{"another schema version", func(o *DeliveryOutcome) { o.SchemaVersion = "munsu.task-authority/v2" }, "invalid delivery outcome schema"},
			{"no task id", func(o *DeliveryOutcome) { o.TaskID = "" }, "delivery outcome missing safe task id"},
			{"path-separating task id", func(o *DeliveryOutcome) { o.TaskID = "../t1" }, "delivery outcome missing safe task id"},
			{"no authorization operation id", func(o *DeliveryOutcome) { o.AuthorizationOperationID = "" }, "delivery outcome missing authorization operation id"},
			{"path-separating authorization operation id", func(o *DeliveryOutcome) { o.AuthorizationOperationID = "op/authorize" }, "delivery outcome missing authorization operation id"},
			{"no operation id", func(o *DeliveryOutcome) { o.OperationID = "" }, "delivery outcome missing operation id"},
			{"path-separating operation id", func(o *DeliveryOutcome) { o.OperationID = `op\outcome` }, "delivery outcome missing operation id"},
			{"digest is not sha256", func(o *DeliveryOutcome) { o.Digest = "digest" }, "delivery outcome digest must be a 64-hex sha256 digest"},
			{"unknown status", func(o *DeliveryOutcome) { o.Status = DeliveryOutcomeStatus("half-merged") }, "delivery outcome has invalid status"},
			{"no detail", func(o *DeliveryOutcome) { o.Detail = "  " }, "delivery outcome missing detail classification"},
			{"unsafe head sha", func(o *DeliveryOutcome) { o.HeadSHA = "refs/heads/main" }, "delivery outcome head SHA must be a safe value"},
			{"unsafe merged sha", func(o *DeliveryOutcome) { o.MergedSHA = `refs\heads\main` }, "delivery outcome merged SHA must be a safe value"},
			{"no committed timestamp", func(o *DeliveryOutcome) { o.CommittedAt = 0 }, "delivery outcome missing committed timestamp"},
			{"negative committed timestamp", func(o *DeliveryOutcome) { o.CommittedAt = -1 }, "delivery outcome missing committed timestamp"},
		})
}

// The index is the bounded current document: three pointers into the immutable
// evidence. A revocation or outcome pointer without an authorization pointer
// points at evidence for an authorization the index does not know about.
//
// The last two cases clear a second pointer as well: with all three set, the
// revocation coherence guard fires before the outcome one, so isolating either
// guard means leaving only its own pointer dangling.
func TestValidateDeliveryIndexRefusesIncoherentPointers(t *testing.T) {
	runGuardCases(t,
		func() DeliveryIndex {
			return DeliveryIndex{
				SchemaVersion:     TaskAuthoritySchema,
				TaskID:            "t1",
				AuthorizationOpID: "op-authorize-1",
				RevocationOpID:    "op-revoke-1",
				OutcomeOpID:       "op-outcome-1",
			}
		},
		validateDeliveryIndex,
		[]guardCase[DeliveryIndex]{
			{"another schema version", func(i *DeliveryIndex) { i.SchemaVersion = "munsu.task-authority/v2" }, "invalid delivery index schema"},
			{"no task id", func(i *DeliveryIndex) { i.TaskID = "" }, "delivery index missing safe task id"},
			{"path-separating task id", func(i *DeliveryIndex) { i.TaskID = "../t1" }, "delivery index missing safe task id"},
			{"unsafe authorization pointer", func(i *DeliveryIndex) { i.AuthorizationOpID = "op/authorize" }, "unsafe authorization pointer"},
			{"unsafe revocation pointer", func(i *DeliveryIndex) { i.RevocationOpID = "op/revoke" }, "unsafe revocation pointer"},
			{"unsafe outcome pointer", func(i *DeliveryIndex) { i.OutcomeOpID = "op/outcome" }, "unsafe outcome pointer"},
			{"revocation pointer without authorization pointer", func(i *DeliveryIndex) {
				i.AuthorizationOpID = ""
				i.OutcomeOpID = ""
			}, "revocation pointer without an authorization pointer"},
			{"outcome pointer without authorization pointer", func(i *DeliveryIndex) {
				i.AuthorizationOpID = ""
				i.RevocationOpID = ""
			}, "outcome pointer without an authorization pointer"},
		})
}
