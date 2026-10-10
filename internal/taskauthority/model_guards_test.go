package taskauthority

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// The refusal branches of the canonical record validators.
//
// Each case takes a record the validator ACCEPTS and breaks exactly one field.
// The accepted fixture is asserted first, once per validator, so a case that
// refuses can only be refusing because of the field it broke: if an earlier
// guard were what fired, the untouched fixture would already be rejected. That
// is the check BEO-87 found missing — two tests asserted a refusal message
// while an earlier guard was what produced it.
//
// Messages are asserted individually rather than `err != nil`, because every
// validator here refuses many different ways with the same error type.

// guardCase breaks one field of an otherwise-accepted record.
type guardCase[T any] struct {
	name    string
	corrupt func(*T)
	wantSub string
}

// runGuardCases asserts the fixture is accepted, then that each single-field
// corruption is refused with its own message.
func runGuardCases[T any](t *testing.T, valid func() T, validate func(T) error, cases []guardCase[T]) {
	t.Helper()
	if err := validate(valid()); err != nil {
		t.Fatalf("fixture is not accepted, so no case below can attribute its refusal: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := valid()
			tc.corrupt(&rec)
			err := validate(rec)
			if err == nil {
				t.Fatalf("validator accepted a record with %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want the %q refusal", err, tc.wantSub)
			}
		})
	}
}

// testSHA256Hex is a shape-valid 64-hex digest for fixtures that require one.
const testSHA256Hex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// A ship task carrying scout-only fields has a contract its kind does not
// define, so the definition is refused rather than silently ignoring them.
func TestValidateScoutContractRefusesScoutFieldsOnShipTask(t *testing.T) {
	runGuardCases(t,
		func() TaskDefinition {
			return TaskDefinition{Owner: "owner", Kind: "ship", Description: "work"}
		},
		validateScoutContract,
		[]guardCase[TaskDefinition]{
			{"scout scope on a ship task", func(d *TaskDefinition) { d.ScoutScope = "map the lane" }, "scout-only fields are not valid for ship tasks"},
			{"scout budget on a ship task", func(d *TaskDefinition) { d.ScoutRuntimeBudgetSecs = 900 }, "scout-only fields are not valid for ship tasks"},
		})
}

// The revision is the aggregate's optimistic-concurrency identity. A record
// with no revision cannot be a precondition for anything.
func TestValidateAggregateRefusesMissingRevision(t *testing.T) {
	runGuardCases(t,
		func() Aggregate {
			agg, err := NewAggregate("t1", "owner", "work", "ship", "", "")
			if err != nil {
				t.Fatalf("NewAggregate: %v", err)
			}
			return agg
		},
		validateAggregate,
		[]guardCase[Aggregate]{
			{"no revision", func(a *Aggregate) { a.Revision = 0 }, "missing revision"},
		})
}

func TestValidateAggregateRefusesUnsafeIdentityAndPhase(t *testing.T) {
	runGuardCases(t,
		func() Aggregate {
			agg, err := NewAggregate("t1", "owner", "work", "ship", "", "")
			if err != nil {
				t.Fatalf("NewAggregate: %v", err)
			}
			return agg
		},
		validateAggregate,
		[]guardCase[Aggregate]{
			{"path-traversing task id", func(a *Aggregate) { a.TaskID = "../escape" }, "invalid task ID"},
			{"invalid phase", func(a *Aggregate) { a.Phase = Phase("in-flight") }, "has invalid phase"},
			{"blank owner", func(a *Aggregate) { a.Definition.Owner = "  " }, "missing owner"},
			{"incomplete worktree binding", func(a *Aggregate) {
				a.Worktree = &WorktreeBinding{
					RepositoryIdentity: "repo-1",
					Path:               "/tmp/wt",
					GitDir:             "/tmp/wt/.git",
					CommonDir:          "/tmp/repo/.git",
					BaseHead:           "abc123",
					FenceToken:         "wt-fence-1",
					BoundAtUnix:        1700000000,
				}
			}, "worktree binding missing lease id"},
		})
}

func TestValidateDispatchHoldRefusesMalformedScope(t *testing.T) {
	runGuardCases(t,
		func() DispatchHold {
			return DispatchHold{
				SchemaVersion: TaskAuthoritySchema,
				ID:            "hold-1",
				Scope:         DispatchHoldScope{},
				Actions:       []DispatchAction{DispatchActionStart},
				Reason:        "reason",
				CreatedAt:     1,
			}
		},
		validateHold,
		[]guardCase[DispatchHold]{
			{"malformed generation scope", func(h *DispatchHold) { h.Scope.Generations = []string{"not-a-generation"} }, "invalid task generation"},
			{"unknown dispatch action", func(h *DispatchHold) { h.Actions = []DispatchAction{"fly"} }, "unknown action"},
		})
}

// Retirement evidence pins which resources a retired generation released. A
// record with no owning operation or no retirement time cannot be attributed.
func TestValidateRetirementEvidenceRefusesUnattributableRecord(t *testing.T) {
	runGuardCases(t,
		func() RetirementEvidence {
			return RetirementEvidence{OperationID: "op-retire-1", Generation: 1, RetiredAt: 1700000000}
		},
		validateRetirementEvidence,
		[]guardCase[RetirementEvidence]{
			{"no operation id", func(e *RetirementEvidence) { e.OperationID = "" }, "retirement evidence missing operation id"},
			{"path-separating operation id", func(e *RetirementEvidence) { e.OperationID = "op/retire" }, "retirement evidence missing operation id"},
			{"no retired timestamp", func(e *RetirementEvidence) { e.RetiredAt = 0 }, "retirement evidence missing retired timestamp"},
			{"negative retired timestamp", func(e *RetirementEvidence) { e.RetiredAt = -1 }, "retirement evidence missing retired timestamp"},
		})
}

// The cleanup claim serializes when a retired generation's resources are
// released. Without a safe owning operation id or a claim time it cannot be
// reconciled by the continuation operations that carry that identity.
func TestValidateCleanupClaimRefusesUnattributableClaim(t *testing.T) {
	runGuardCases(t,
		func() CleanupClaim {
			return CleanupClaim{OperationID: "op-retire-1", Generation: 1, Status: CleanupActive, ClaimedAt: 1700000000}
		},
		validateCleanupClaim,
		[]guardCase[CleanupClaim]{
			{"no operation id", func(c *CleanupClaim) { c.OperationID = "" }, "cleanup claim missing operation id"},
			{"path-separating operation id", func(c *CleanupClaim) { c.OperationID = `op\retire` }, "cleanup claim missing operation id"},
			{"invalid status", func(c *CleanupClaim) { c.Status = CleanupStatus("invalid") }, "cleanup claim has invalid status"},
			{"no claimed timestamp", func(c *CleanupClaim) { c.ClaimedAt = 0 }, "cleanup claim missing claimed timestamp"},
			{"negative claimed timestamp", func(c *CleanupClaim) { c.ClaimedAt = -1 }, "cleanup claim missing claimed timestamp"},
		})
}

// validLaunchIntent is a committed launch intent the validator accepts. The
// launch-identity cases below break one field each; they run through
// validateLaunchIntent because that is the production entry point.
func validLaunchIntent() LaunchIntent {
	return LaunchIntent{
		OperationID:           "op-begin-1",
		SnapshotDigest:        testSHA256Hex,
		Backend:               "tmux",
		Harness:               "pi",
		Model:                 "gpt-5",
		Effort:                "high",
		Mode:                  "direct-PR",
		Kind:                  "ship",
		Project:               "test-proj",
		LaunchID:              "launch-1",
		WindowLabel:           "mu-soldier-t1",
		WorktreeReservationID: "wt-res-1",
		WorktreeFenceToken:    "wt-fence-1",
		EndpointReservationID: "ep-res-1",
		EndpointFenceToken:    "ep-fence-1",
		EndpointIncarnation:   "inc-1",
		PlannedAt:             1700000000,
	}
}

// The committing operation id and the planned timestamp are what make the
// intent a durable record rather than a value.
func TestValidateLaunchIntentRefusesUnattributableIntent(t *testing.T) {
	runGuardCases(t, validLaunchIntent, validateLaunchIntent, []guardCase[LaunchIntent]{
		{"no operation id", func(l *LaunchIntent) { l.OperationID = "" }, "launch intent missing operation id"},
		{"path-separating operation id", func(l *LaunchIntent) { l.OperationID = "op/begin" }, "launch intent missing operation id"},
		{"no planned timestamp", func(l *LaunchIntent) { l.PlannedAt = 0 }, "launch intent missing planned timestamp"},
		{"negative planned timestamp", func(l *LaunchIntent) { l.PlannedAt = -1 }, "launch intent missing planned timestamp"},
	})
}

// The launch identity fences every later binding mutation. A missing or
// path-separating identity would produce a fence that cannot be matched, so
// the intent is refused at commit time rather than at bind time.
func TestValidateLaunchIdentityRefusesUnsafeOrAbsentIdentities(t *testing.T) {
	runGuardCases(t, validLaunchIntent, validateLaunchIntent, []guardCase[LaunchIntent]{
		{"no harness identity", func(l *LaunchIntent) { l.Harness = "  " }, "launch requires an explicit harness identity"},
		{"path-separating harness identity", func(l *LaunchIntent) { l.Harness = "bin/pi" }, "launch requires an explicit harness identity"},
		{"path-separating model", func(l *LaunchIntent) { l.Model = "openai/gpt-5" }, "launch carries an unsafe identity value"},
		{"path-separating window label", func(l *LaunchIntent) { l.WindowLabel = `mu\soldier` }, "launch carries an unsafe identity value"},
		{"no launch id", func(l *LaunchIntent) { l.LaunchID = "" }, "launch requires a deterministic launch identity"},
		{"path-separating launch id", func(l *LaunchIntent) { l.LaunchID = "launch/1" }, "launch requires a deterministic launch identity"},
		{"no worktree reservation id", func(l *LaunchIntent) { l.WorktreeReservationID = "" }, "launch requires a worktree reservation id"},
		{"path-separating worktree reservation id", func(l *LaunchIntent) { l.WorktreeReservationID = "wt/res" }, "launch requires a worktree reservation id"},
		{"no endpoint reservation id", func(l *LaunchIntent) { l.EndpointReservationID = "" }, "launch requires an endpoint reservation id"},
		{"path-separating endpoint reservation id", func(l *LaunchIntent) { l.EndpointReservationID = "ep/res" }, "launch requires an endpoint reservation id"},
		{"no endpoint fence token", func(l *LaunchIntent) { l.EndpointFenceToken = "" }, "launch requires an endpoint fence token"},
		{"path-separating endpoint fence token", func(l *LaunchIntent) { l.EndpointFenceToken = "ep/fence" }, "launch requires an endpoint fence token"},
		{"no endpoint incarnation", func(l *LaunchIntent) { l.EndpointIncarnation = "  " }, "launch requires an opaque endpoint incarnation token"},
		{"path-separating endpoint incarnation", func(l *LaunchIntent) { l.EndpointIncarnation = "inc/1" }, "launch requires an opaque endpoint incarnation token"},
	})
}

// The optional identity fields of an acquired endpoint are still durable
// record keys, so an unsafe value in one of them is refused.
func TestValidateAcquiredEndpointRefusesUnsafeOptionalIdentity(t *testing.T) {
	runGuardCases(t,
		func() AcquiredEndpoint {
			return AcquiredEndpoint{
				OperationID: "op-attach-1", Backend: "tmux", Handle: "pane-1",
				LeaseID: "ep-res-1", FenceToken: "ep-fence-1",
				SessionOwner: "sess-1", WorkspaceID: "ws-1", TabID: "tab-1",
				Incarnation: "inc-1", AcquiredAt: 1700000000,
			}
		},
		validateAcquiredEndpoint,
		[]guardCase[AcquiredEndpoint]{
			{"path-separating session owner", func(e *AcquiredEndpoint) { e.SessionOwner = "sess/1" }, "acquired endpoint carries an unsafe identity value"},
			{"path-separating workspace id", func(e *AcquiredEndpoint) { e.WorkspaceID = `ws\1` }, "acquired endpoint carries an unsafe identity value"},
			{"path-separating tab id", func(e *AcquiredEndpoint) { e.TabID = "tab/1" }, "acquired endpoint carries an unsafe identity value"},
		})
}

// The transfer state names the reservation and the two homes. An unsafe value
// in any of them would escape the reservation-scoped key space.
func TestValidateTransferStateRefusesUnsafeIdentities(t *testing.T) {
	runGuardCases(t,
		func() TransferState {
			return TransferState{
				ReservationID:   "transfer-res-1",
				SourceHome:      "home-a",
				DestinationHome: "home-b",
				FenceToken:      "transfer-fence-1",
				ReservedAt:      1700000000,
			}
		},
		validateTransferState,
		[]guardCase[TransferState]{
			{"no reservation id", func(ts *TransferState) { ts.ReservationID = "" }, "transfer reservation ID must be a safe non-empty value"},
			{"path-separating reservation id", func(ts *TransferState) { ts.ReservationID = "transfer/res" }, "transfer reservation ID must be a safe non-empty value"},
			{"path-separating destination home", func(ts *TransferState) { ts.DestinationHome = "homes/b" }, "transfer destination home must be a safe value"},
			{"path-separating source home", func(ts *TransferState) { ts.SourceHome = `homes\a` }, "transfer source home must be a safe value"},
		})
}

// The activation evidence is the destination's proof the transfer landed. Its
// digest must be a real sha256 and its identities must be safe, or the source
// supersession would be recorded against an unverifiable receipt.
func TestValidateTransferActivationRefusesMalformedEvidence(t *testing.T) {
	runGuardCases(t,
		func() TransferActivationInfo {
			return TransferActivationInfo{
				ReservationID:         "transfer-res-1",
				TaskID:                "t1",
				SourceHome:            "home-a",
				SourceGeneration:      1,
				DestinationHome:       "home-b",
				DestinationGeneration: 1,
				ActivationOperationID: "op-activate-1",
				ActivationDigest:      testSHA256Hex,
			}
		},
		validateTransferActivation,
		[]guardCase[TransferActivationInfo]{
			{"path-separating task id", func(a *TransferActivationInfo) { a.TaskID = "t/1" }, "transfer activation evidence carries an unsafe identity value"},
			{"path-separating destination home", func(a *TransferActivationInfo) { a.DestinationHome = `homes\b` }, "transfer activation evidence carries an unsafe identity value"},
			{"digest is not sha256", func(a *TransferActivationInfo) { a.ActivationDigest = "not-a-digest" }, "transfer activation digest must be a 64-hex sha256 digest"},
			{"digest is short hex", func(a *TransferActivationInfo) { a.ActivationDigest = testSHA256Hex[:63] }, "transfer activation digest must be a 64-hex sha256 digest"},
		})
}

// Every field of an endpoint binding is load-bearing: the backend and handle
// address it, the lease and fence prove ownership, the incarnation rejects
// stale observations, and the bound time orders it.
func TestValidateEndpointBindingRefusesIncompleteBinding(t *testing.T) {
	runGuardCases(t,
		func() EndpointBinding {
			return EndpointBinding{
				Backend: "tmux", Handle: "pane-1", LeaseID: "ep-res-1",
				FenceToken: "ep-fence-1", Incarnation: "inc-1", BoundAtUnix: 1700000000,
			}
		},
		validateEndpointBinding,
		[]guardCase[EndpointBinding]{
			{"no backend", func(b *EndpointBinding) { b.Backend = "  " }, "endpoint binding missing backend"},
			{"no handle", func(b *EndpointBinding) { b.Handle = "  " }, "endpoint binding missing handle"},
			{"no lease id", func(b *EndpointBinding) { b.LeaseID = "  " }, "endpoint binding missing lease id"},
			{"no fence token", func(b *EndpointBinding) { b.FenceToken = "  " }, "endpoint binding missing fence token"},
			{"no incarnation", func(b *EndpointBinding) { b.Incarnation = "  " }, "endpoint binding missing opaque incarnation token"},
			{"no bound timestamp", func(b *EndpointBinding) { b.BoundAtUnix = 0 }, "endpoint binding missing bound timestamp"},
			{"negative bound timestamp", func(b *EndpointBinding) { b.BoundAtUnix = -1 }, "endpoint binding missing bound timestamp"},
		})
}

// A worktree binding identifies a repository checkout precisely enough to
// release it later: repository identity, the three git paths, the head it was
// bound at, and the lease fence.
func TestValidateWorktreeBindingRefusesIncompleteBinding(t *testing.T) {
	runGuardCases(t,
		func() WorktreeBinding {
			return WorktreeBinding{
				RepositoryIdentity: "repo-1",
				Path:               "/tmp/wt",
				GitDir:             "/tmp/wt/.git",
				CommonDir:          "/tmp/repo/.git",
				BaseHead:           "abc123",
				LeaseID:            "wt-res-1",
				FenceToken:         "wt-fence-1",
				BoundAtUnix:        1700000000,
			}
		},
		validateWorktreeBinding,
		[]guardCase[WorktreeBinding]{
			{"incomplete worktree binding", func(b *WorktreeBinding) { b.LeaseID = "" }, "worktree binding missing lease id"},
			{"no repository identity", func(b *WorktreeBinding) { b.RepositoryIdentity = "  " }, "worktree binding missing repository identity"},
			{"no path", func(b *WorktreeBinding) { b.Path = "  " }, "worktree binding missing path"},
			{"no git dir", func(b *WorktreeBinding) { b.GitDir = "  " }, "worktree binding missing git dir"},
			{"no common dir", func(b *WorktreeBinding) { b.CommonDir = "  " }, "worktree binding missing common dir"},
			{"no base head", func(b *WorktreeBinding) { b.BaseHead = "  " }, "worktree binding missing base head"},
			{"no fence token", func(b *WorktreeBinding) { b.FenceToken = "  " }, "worktree binding missing fence token"},
			{"no bound timestamp", func(b *WorktreeBinding) { b.BoundAtUnix = 0 }, "worktree binding missing bound timestamp"},
			{"negative bound timestamp", func(b *WorktreeBinding) { b.BoundAtUnix = -1 }, "worktree binding missing bound timestamp"},
		})
}

func TestValidateLaunchManifestEvidenceRefusesUnattributableRecord(t *testing.T) {
	runGuardCases(t,
		func() LaunchManifestEvidence {
			return LaunchManifestEvidence{
				OperationID: "op-manifest-1", LaunchID: "launch-1",
				ManifestSHA256: testSHA256Hex, RecordedAt: 1700000000,
			}
		},
		validateLaunchManifestEvidence,
		[]guardCase[LaunchManifestEvidence]{
			{"no operation id", func(e *LaunchManifestEvidence) { e.OperationID = "" }, "launch manifest evidence missing operation id"},
			{"path-separating operation id", func(e *LaunchManifestEvidence) { e.OperationID = `op\\manifest` }, "launch manifest evidence missing operation id"},
			{"no launch identity", func(e *LaunchManifestEvidence) { e.LaunchID = "" }, "launch manifest evidence missing launch identity"},
			{"path-separating launch identity", func(e *LaunchManifestEvidence) { e.LaunchID = "launch/1" }, "launch manifest evidence missing launch identity"},
			{"digest is not a sha256", func(e *LaunchManifestEvidence) { e.ManifestSHA256 = "not-a-digest" }, "launch manifest evidence digest must be a 64-hex sha256 digest"},
			{"no recording timestamp", func(e *LaunchManifestEvidence) { e.RecordedAt = 0 }, "launch manifest evidence missing recorded timestamp"},
			{"negative recording timestamp", func(e *LaunchManifestEvidence) { e.RecordedAt = -1 }, "launch manifest evidence missing recorded timestamp"},
		})
}

func TestValidateWorktreeManifestRelationshipRefusesUnboundEvidence(t *testing.T) {
	binding := worktreeBinding()
	binding.LeaseID, binding.FenceToken = "wt-res-1", "wt-fence-1"
	binding.LaunchManifest = &LaunchManifestEvidence{
		OperationID: "op-manifest-1", LaunchID: "launch-1", ManifestSHA256: testSHA256Hex, RecordedAt: 1700000000,
	}
	launch := validLaunchIntent()
	launch.LaunchID, launch.WorktreeReservationID, launch.WorktreeFenceToken = "launch-1", "wt-res-1", "wt-fence-1"
	if err := validateWorktreeManifestRelationship(binding, &launch, KindShip); err != nil {
		t.Fatalf("valid worktree manifest relationship refused: %v", err)
	}

	for _, tc := range []struct {
		name   string
		launch *LaunchIntent
		kind   string
		mutate func(*WorktreeBinding)
		want   string
	}{
		{"no launch intent", nil, KindShip, func(*WorktreeBinding) {}, "requires a committed launch intent"},
		{"review task", &launch, KindReview, func(*WorktreeBinding) {}, "review tasks cannot own launch manifest evidence"},
		{"different launch identity", &launch, KindShip, func(b *WorktreeBinding) { b.LaunchManifest.LaunchID = "launch-other" }, "does not match launch intent"},
		{"different worktree fence", &launch, KindShip, func(b *WorktreeBinding) { b.FenceToken = "wt-fence-other" }, "does not match launch intent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cloneWorktreeBinding(binding)
			tc.mutate(&got)
			if err := validateWorktreeManifestRelationship(got, tc.launch, tc.kind); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateWorktreeManifestRelationship error = %v, want %q", err, tc.want)
			}
		})
	}
}

// The acquired-endpoint record is the committed proof that a soldier holds an
// endpoint. Every field below is what makes the hold attributable and
// releasable: without the operation id nothing says which operation acquired
// it, and without the lease or fence token a stale holder cannot be fenced off.
// These refusals were invisible to the guards lane until BEO-123 fixed the
// instrument that was misreading them as error propagation.
func TestValidateAcquiredEndpointRefusesIncompleteRecord(t *testing.T) {
	runGuardCases(t,
		func() AcquiredEndpoint {
			return AcquiredEndpoint{
				OperationID: "op-attach-1", Backend: "tmux", Handle: "pane-1",
				LeaseID: "ep-res-1", FenceToken: "ep-fence-1",
				SessionOwner: "sess-1", WorkspaceID: "ws-1", TabID: "tab-1",
				Incarnation: "inc-1", AcquiredAt: 1700000000,
			}
		},
		validateAcquiredEndpoint,
		[]guardCase[AcquiredEndpoint]{
			{"no operation id", func(e *AcquiredEndpoint) { e.OperationID = "" }, "acquired endpoint missing operation id"},
			{"path-separating operation id", func(e *AcquiredEndpoint) { e.OperationID = "op/attach" }, "acquired endpoint missing operation id"},
			{"no backend", func(e *AcquiredEndpoint) { e.Backend = "   " }, "acquired endpoint missing backend"},
			{"no handle", func(e *AcquiredEndpoint) { e.Handle = "" }, "acquired endpoint missing handle"},
			{"no lease id", func(e *AcquiredEndpoint) { e.LeaseID = "\t" }, "acquired endpoint missing lease id"},
			{"no fence token", func(e *AcquiredEndpoint) { e.FenceToken = "" }, "acquired endpoint missing fence token"},
			{"no acquisition timestamp", func(e *AcquiredEndpoint) { e.AcquiredAt = 0 }, "acquired endpoint missing acquisition timestamp"},
		})
}

// The launch evidence is what ties a running process back to the command that
// was actually submitted. A digest that is not a real sha256 would make the
// submitted command unverifiable, which is the whole purpose of the record.
func TestValidateLaunchEvidenceRefusesUnverifiableRecord(t *testing.T) {
	runGuardCases(t,
		func() LaunchEvidence {
			return LaunchEvidence{
				OperationID:   "op-launch-1",
				LaunchID:      "launch-1",
				CommandDigest: testSHA256Hex,
				SubmittedAt:   1700000000,
				Seat:          testSeat(),
			}
		},
		validateLaunchEvidence,
		[]guardCase[LaunchEvidence]{
			{"no operation id", func(e *LaunchEvidence) { e.OperationID = "" }, "launch evidence missing operation id"},
			{"path-separating operation id", func(e *LaunchEvidence) { e.OperationID = `op\launch` }, "launch evidence missing operation id"},
			{"no launch identity", func(e *LaunchEvidence) { e.LaunchID = "" }, "launch evidence missing launch identity"},
			{"path-separating launch identity", func(e *LaunchEvidence) { e.LaunchID = "launch/1" }, "launch evidence missing launch identity"},
			{"digest is not a sha256", func(e *LaunchEvidence) { e.CommandDigest = "not-a-digest" }, "launch evidence command digest must be a 64-hex sha256 digest"},
			{"review tree without a head", func(e *LaunchEvidence) { e.ReviewTree = &domain.TreeState{Porcelain: testSHA256Hex} }, "launch evidence review tree requires a head and a porcelain digest"},
			{"review tree without a porcelain digest", func(e *LaunchEvidence) { e.ReviewTree = &domain.TreeState{Head: "abc123"} }, "launch evidence review tree requires a head and a porcelain digest"},
			{"no seat argv", func(e *LaunchEvidence) { e.Seat.Argv = nil }, "launch evidence seat requires the harness argv"},
			{"empty seat argument", func(e *LaunchEvidence) { e.Seat.Argv = []string{"pi", ""} }, "launch evidence seat argv carries an empty argument"},
			{"prompt digest is not a sha256", func(e *LaunchEvidence) { e.Seat.PromptDigest = "prompt" }, "launch evidence seat prompt digest must be a 64-hex sha256 digest"},
			{"applied fence without a role", func(e *LaunchEvidence) { e.Seat.Fence = FenceRecord{Applied: true, ProfileDigest: testSHA256Hex} }, "launch evidence applied fence requires a role and a profile digest and no reason"},
			{"applied fence without a profile digest", func(e *LaunchEvidence) { e.Seat.Fence = FenceRecord{Applied: true, Role: "ship"} }, "launch evidence applied fence requires a role and a profile digest and no reason"},
			{"applied fence carrying a reason", func(e *LaunchEvidence) {
				e.Seat.Fence = FenceRecord{Applied: true, Role: "ship", ProfileDigest: testSHA256Hex, Reason: "why"}
			}, "launch evidence applied fence requires a role and a profile digest and no reason"},
			{"unapplied fence without a reason", func(e *LaunchEvidence) { e.Seat.Fence = FenceRecord{} }, "launch evidence unapplied fence requires a reason and no role or profile digest"},
			{"unapplied fence carrying a role", func(e *LaunchEvidence) { e.Seat.Fence = FenceRecord{Reason: "none", Role: "ship"} }, "launch evidence unapplied fence requires a reason and no role or profile digest"},
			{"unapplied fence carrying a profile digest", func(e *LaunchEvidence) { e.Seat.Fence = FenceRecord{Reason: "none", ProfileDigest: testSHA256Hex} }, "launch evidence unapplied fence requires a reason and no role or profile digest"},
		})
}

// A review task names the ship task it reads and the one head it reviews; no
// other kind carries either field.
func TestValidateReviewContractBindsReviewFieldsToTheReviewKind(t *testing.T) {
	runGuardCases(t,
		func() TaskDefinition {
			return TaskDefinition{Owner: "owner", Kind: KindReview, Description: "review", ReviewTaskID: "ship1", ReviewHead: "abc123"}
		},
		validateReviewContract,
		[]guardCase[TaskDefinition]{
			{"review without the reviewed task", func(d *TaskDefinition) { d.ReviewTaskID = "" }, "review task requires the reviewed task id"},
			{"review of an unsafe task id", func(d *TaskDefinition) { d.ReviewTaskID = "../ship1" }, "review task requires the reviewed task id"},
			{"review without the reviewed head", func(d *TaskDefinition) { d.ReviewHead = "  " }, "review task requires the reviewed head"},
			{"review task id on a ship task", func(d *TaskDefinition) { d.Kind, d.ReviewHead = KindShip, "" }, "review-only fields are not valid for ship tasks"},
			{"review head on a scout task", func(d *TaskDefinition) { d.Kind, d.ReviewTaskID = KindScout, "" }, "review-only fields are not valid for scout tasks"},
		})
}

func TestSoldierKindIsTheClosedSetOfLaunchableKinds(t *testing.T) {
	for _, kind := range []string{KindShip, KindScout, KindReview} {
		if !SoldierKind(kind) {
			t.Errorf("SoldierKind(%q) = false, want true", kind)
		}
	}
	for _, kind := range []string{"", "captain", "Ship"} {
		if SoldierKind(kind) {
			t.Errorf("SoldierKind(%q) = true, want false", kind)
		}
	}
}

// A review launch reserves no worktree, because it reads the reviewed task's;
// every other kind must reserve one.
func TestValidateLaunchIntentRefusesAWorktreeReservationOnAReviewLaunch(t *testing.T) {
	review := func() LaunchIntent {
		l := validLaunchIntent()
		l.Kind, l.WorktreeReservationID, l.WorktreeFenceToken = KindReview, "", ""
		return l
	}
	runGuardCases(t, review, validateLaunchIntent, []guardCase[LaunchIntent]{
		{"review launch reserving a worktree", func(l *LaunchIntent) { l.WorktreeReservationID = "wt-res-1" }, "a review launch reserves no worktree"},
		{"review launch carrying a worktree fence token", func(l *LaunchIntent) { l.WorktreeFenceToken = "wt-fence-1" }, "a review launch reserves no worktree"},
	})
}

// A hold carries release words exactly when it was released, and only words
// that name a grantor, a channel and a quote.
func TestValidateDispatchHoldBindsReleaseWordsToRelease(t *testing.T) {
	words := testWords()
	runGuardCases(t,
		func() DispatchHold {
			return DispatchHold{
				SchemaVersion: TaskAuthoritySchema, ID: "hold-1", Scope: DispatchHoldScope{},
				Actions: []DispatchAction{DispatchActionStart}, Reason: "reason", CreatedAt: 1,
				ReleasedAt: 2, ReleaseWords: &words,
			}
		},
		validateHold,
		[]guardCase[DispatchHold]{
			{"words on an unreleased hold", func(h *DispatchHold) { h.ReleasedAt = 0 }, "carries release words but is not released"},
			{"released hold with no words", func(h *DispatchHold) { h.ReleaseWords = nil }, "carries no release words"},
			{"released hold with an empty quote", func(h *DispatchHold) { h.ReleaseWords = &domain.Words{Grantor: "g", Channel: "c"} }, "quote is required"},
		})
}

func TestValidateReviewVerdictRecordRefusesUnattributableRecord(t *testing.T) {
	runGuardCases(t,
		func() ReviewVerdictRecord {
			return ReviewVerdictRecord{OperationID: "op-verdict-1", Verdict: passVerdictFixture(), RecordedAt: 1700000000}
		},
		validateReviewVerdictRecord,
		[]guardCase[ReviewVerdictRecord]{
			{"no operation id", func(r *ReviewVerdictRecord) { r.OperationID = "" }, "review verdict record missing operation id"},
			{"path-separating operation id", func(r *ReviewVerdictRecord) { r.OperationID = "op/verdict" }, "review verdict record missing operation id"},
			{"invalid verdict", func(r *ReviewVerdictRecord) { r.Verdict.BaseSHA = "" }, "base SHA is required"},
			{"no recorded timestamp", func(r *ReviewVerdictRecord) { r.RecordedAt = 0 }, "review verdict record missing recorded timestamp"},
		})
}

// The delivery contract is the record every later generation reads instead of
// re-resolving the mode. A contract carrying a mode no delivery path
// implements would silently license the wrong delivery behaviour for the rest
// of the task's life, so the record refuses to exist in that shape.
func TestValidateDeliveryContractRefusesUnenforceableRecord(t *testing.T) {
	valid := func() DeliveryContract {
		return DeliveryContract{
			OperationID: "op-contract-1",
			Mode:        "direct-PR",
			Review:      DeliveryStep{Baseline: true, ProbeState: "baseline"},
			Forge:       DeliveryStep{Adapter: "gitlab", Path: "/bin/glab", ProbeState: "ready"},
			RecordedAt:  1700000000,
		}
	}
	runGuardCases(t, valid, validateDeliveryContract, []guardCase[DeliveryContract]{
		{"no operation id", func(d *DeliveryContract) { d.OperationID = "" }, "delivery contract missing operation id"},
		{"path-separating operation id", func(d *DeliveryContract) { d.OperationID = "op/contract" }, "delivery contract missing operation id"},
		{"empty mode", func(d *DeliveryContract) { d.Mode = "" }, "delivery contract carries invalid delivery mode"},
		{"unknown mode", func(d *DeliveryContract) { d.Mode = "direct-pr" }, "delivery contract carries invalid delivery mode"},
		{"no recorded timestamp", func(d *DeliveryContract) { d.RecordedAt = 0 }, "delivery contract missing recorded timestamp"},
		{"changed mode", func(d *DeliveryContract) { d.Mode = "local-only" }, "disagrees with captured review and forge steps"},
		{"relative executable", func(d *DeliveryContract) { d.Forge.Path = "glab" }, "not a Ready supported adapter"},
	})
}
