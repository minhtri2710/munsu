package fleet

import (
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// contractedLaunch drives a fixture through the launch steps that commit the
// generation's intent and the durable contract, leaving the runner exactly
// where the fallback sites run: contracted, not yet launched.
func contractedLaunch(t *testing.T, f *launchFixture, mode string) {
	t.Helper()
	r := f.runner
	r.effectiveMode = mode
	r.requestedMode = mode
	r.contractMode = mode
	if err := r.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := r.recordDeliveryContract(); err != nil {
		t.Fatalf("recordDeliveryContract: %v", err)
	}
}

func contractOf(t *testing.T, f *launchFixture) taskauthority.DeliveryContract {
	t.Helper()
	agg, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract == nil {
		t.Fatal("task carries no delivery contract")
	}
	return *agg.DeliveryContract
}

// TestReconcileDeliveryFallbackNoDivergenceRecordsNothing pins the ordinary
// launch: a mode that matches the contract is not a transition, so the record
// is left exactly as the contract wrote it.
func TestReconcileDeliveryFallbackNoDivergenceRecordsNothing(t *testing.T) {
	f := newLaunchFixture(t, "fallback-none")
	contractedLaunch(t, f, "direct-PR")
	before := contractOf(t, f)

	if err := f.runner.reconcileDeliveryFallback(); err != nil {
		t.Fatalf("reconcileDeliveryFallback: %v", err)
	}
	after := contractOf(t, f)
	if after.Fallback != nil {
		t.Fatalf("a launch with no divergence recorded a transition: %+v", *after.Fallback)
	}
	if after.OperationID != before.OperationID || after.Mode != before.Mode {
		t.Fatalf("contract mutated without a fallback: %+v -> %+v", before, after)
	}
}

// TestReconcileDeliveryFallbackRecordsPreflightFallback builds the first
// fallback site's real state: preflightNoMistakes hits a gate blocker under
// the configured allow-direct-pr-fallback policy, mutates effectiveMode, and
// the contract must end up stating direct-PR and how it got there.
func TestReconcileDeliveryFallbackRecordsPreflightFallback(t *testing.T) {
	f := newLaunchFixture(t, "fallback-preflight")
	contractedLaunch(t, f, "no-mistakes")

	r := f.runner
	r.allowDirectPRFallback = true
	r.args.NoMistakesPreflight = func(string) error {
		return &GateBlockerError{Category: GateBlockerAgentUnavailable, Detail: "gate agent not on PATH"}
	}
	if err := r.preflightNoMistakes(); err != nil {
		t.Fatalf("preflightNoMistakes: %v", err)
	}
	if r.effectiveMode != "direct-PR" || r.fallbackReason == "" {
		t.Fatalf("preflight did not fall back: mode=%q reason=%q", r.effectiveMode, r.fallbackReason)
	}

	if err := r.reconcileDeliveryFallback(); err != nil {
		t.Fatalf("reconcileDeliveryFallback: %v", err)
	}
	dc := contractOf(t, f)
	if dc.Mode != "direct-PR" {
		t.Fatalf("contract does not state the mode in force: %q", dc.Mode)
	}
	if dc.Fallback == nil {
		t.Fatal("the authorized fallback was not recorded as a transition")
	}
	if dc.Fallback.From != "no-mistakes" || dc.Fallback.To != "direct-PR" {
		t.Fatalf("transition = %+v", *dc.Fallback)
	}
	if dc.Fallback.Reason != r.fallbackReason {
		t.Fatalf("recorded reason %q != runner reason %q", dc.Fallback.Reason, r.fallbackReason)
	}
	if dc.Fallback.Generation != 1 {
		t.Fatalf("transition generation = %d", dc.Fallback.Generation)
	}
}

// TestReconcileDeliveryFallbackRecordsLateCapabilityLoss builds the second
// fallback site's real state: an expired attestation carrying a pre-authorized
// fallback mode. The same reconcile step must record it, so both sites reach
// the durable record through one path.
func TestReconcileDeliveryFallbackRecordsLateCapabilityLoss(t *testing.T) {
	f := newLaunchFixture(t, "fallback-lateloss")
	contractedLaunch(t, f, "no-mistakes")

	r := f.runner
	r.attestation = &CapabilityAttestation{
		RequestedMode:  "no-mistakes",
		EffectiveMode:  "no-mistakes",
		Expiry:         time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		FallbackPolicy: &FallbackPolicy{AuthorizedMode: "direct-PR"},
	}
	if err := r.checkAttestation(); err != nil {
		t.Fatalf("checkAttestation: %v", err)
	}
	if r.effectiveMode != "direct-PR" || r.fallbackReason == "" {
		t.Fatalf("late loss did not fall back: mode=%q reason=%q", r.effectiveMode, r.fallbackReason)
	}

	if err := r.reconcileDeliveryFallback(); err != nil {
		t.Fatalf("reconcileDeliveryFallback: %v", err)
	}
	dc := contractOf(t, f)
	if dc.Mode != "direct-PR" || dc.Fallback == nil {
		t.Fatalf("late capability loss not recorded on the contract: %+v", dc)
	}
	if dc.Fallback.From != "no-mistakes" || dc.Fallback.To != "direct-PR" {
		t.Fatalf("transition = %+v", *dc.Fallback)
	}
	if !strings.Contains(dc.Fallback.Reason, "attestation expired") {
		t.Fatalf("transition reason does not carry the loss detail: %q", dc.Fallback.Reason)
	}
}

// TestReconcileDeliveryFallbackIsIdempotent pins recovery re-entry: replaying
// the reconcile against an already-transitioned contract records nothing new.
func TestReconcileDeliveryFallbackIsIdempotent(t *testing.T) {
	f := newLaunchFixture(t, "fallback-reentry")
	contractedLaunch(t, f, "no-mistakes")

	r := f.runner
	r.effectiveMode = "direct-PR"
	r.fallbackReason = "no-mistakes blocked (agent-unavailable): gate agent not on PATH"
	if err := r.reconcileDeliveryFallback(); err != nil {
		t.Fatalf("reconcileDeliveryFallback: %v", err)
	}
	agg, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	before := agg.Revision
	firstOp := agg.DeliveryContract.Fallback.OperationID

	if err := r.reconcileDeliveryFallback(); err != nil {
		t.Fatalf("re-entrant reconcileDeliveryFallback: %v", err)
	}
	after, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before {
		t.Fatalf("re-entry bumped the revision %d -> %d", before, after.Revision)
	}
	if after.DeliveryContract.Fallback.OperationID != firstOp {
		t.Fatalf("re-entry rewrote the committed transition: %+v", *after.DeliveryContract.Fallback)
	}
}

// TestNextGenerationReadsFallenBackModeAndDoesNotReFallBack pins the point of
// recording the transition: generation N+1 reads direct-PR off the contract,
// so preflightNoMistakes never runs its no-mistakes branch, nothing is
// re-recorded, and a capability that has since returned does NOT flip the
// task back to no-mistakes.
func TestNextGenerationReadsFallenBackModeAndDoesNotReFallBack(t *testing.T) {
	f := newLaunchFixture(t, "fallback-nextgen")
	contractedLaunch(t, f, "no-mistakes")
	r := f.runner
	r.effectiveMode = "direct-PR"
	r.fallbackReason = "no-mistakes blocked (agent-unavailable): gate agent not on PATH"
	if err := r.reconcileDeliveryFallback(); err != nil {
		t.Fatalf("reconcileDeliveryFallback: %v", err)
	}
	// The task closes and reopens: generation N+1 is where the next spawn
	// resolves, and it must read the contract the fallback moved.
	reopenTaskForFallbackTest(t, f)
	before, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if before.Generation < 2 {
		t.Fatalf("the next spawn is not on a new generation: %s", before.Generation)
	}
	if before.DeliveryContract == nil || before.DeliveryContract.Mode != "direct-PR" || before.DeliveryContract.Fallback == nil {
		t.Fatalf("the new generation did not carry the transitioned contract: %+v", before.DeliveryContract)
	}

	// The next spawn resolves the mode from scratch against the moved
	// contract.
	r.effectiveMode = ""
	r.requestedMode = ""
	r.contractMode = ""
	r.fallbackReason = "was set by the prior generation"
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if r.effectiveMode != "direct-PR" || r.contractMode != "direct-PR" {
		t.Fatalf("next generation did not read the fallen-back mode: %q / %q", r.effectiveMode, r.contractMode)
	}
	if r.fallbackReason != "" {
		t.Fatalf("reading a contract carried a stale fallback reason: %q", r.fallbackReason)
	}

	// The gate preflight would now succeed (capability returned), but the
	// contracted mode is no longer no-mistakes so it never runs.
	probed := false
	r.allowDirectPRFallback = true
	r.args.NoMistakesPreflight = func(string) error {
		probed = true
		return nil
	}
	if err := r.preflightNoMistakes(); err != nil {
		t.Fatalf("preflightNoMistakes: %v", err)
	}
	if probed {
		t.Fatal("a contracted direct-PR task still ran the no-mistakes preflight")
	}
	if err := r.reconcileDeliveryFallback(); err != nil {
		t.Fatalf("reconcileDeliveryFallback: %v", err)
	}
	after, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision {
		t.Fatalf("the next generation re-recorded the transition: revision %d -> %d", before.Revision, after.Revision)
	}
	if after.DeliveryContract.Mode != "direct-PR" {
		t.Fatalf("a returned capability flipped the contract back: %q", after.DeliveryContract.Mode)
	}
}

// TestReconcileDeliveryFallbackRefusesUnexplainedDivergence builds fail-closed
// (a): the launch mode diverges from the contract with no fallback event
// behind it. A transition with no reason is never recorded, and the launch
// aborts rather than delivering under an unrecorded mode.
func TestReconcileDeliveryFallbackRefusesUnexplainedDivergence(t *testing.T) {
	f := newLaunchFixture(t, "fallback-unexplained")
	contractedLaunch(t, f, "direct-PR")

	r := f.runner
	r.effectiveMode = "local-only"
	r.fallbackReason = ""
	err := r.reconcileDeliveryFallback()
	if err == nil {
		t.Fatal("an unexplained mode divergence was accepted")
	}
	if !strings.Contains(err.Error(), "no recorded fallback") {
		t.Fatalf("refusal does not name the missing fallback: %v", err)
	}
	dc := contractOf(t, f)
	if dc.Mode != "direct-PR" || dc.Fallback != nil {
		t.Fatalf("refused divergence mutated the contract: %+v", dc)
	}
}

// TestReconcileDeliveryFallbackRefusesUnrecordableTransition builds
// fail-closed (b): the recording op refuses the transition, so the launch
// aborts. Nothing delivers under a mode the contract does not state.
func TestReconcileDeliveryFallbackRefusesUnrecordableTransition(t *testing.T) {
	f := newLaunchFixture(t, "fallback-unrecordable")
	contractedLaunch(t, f, "direct-PR")

	r := f.runner
	r.effectiveMode = "yolo"
	r.fallbackReason = "operator says so"
	err := r.reconcileDeliveryFallback()
	if err == nil {
		t.Fatal("a transition to an unenforceable mode was accepted")
	}
	if !strings.Contains(err.Error(), "delivery fallback") {
		t.Fatalf("refusal is not the fallback record: %v", err)
	}
	dc := contractOf(t, f)
	if dc.Mode != "direct-PR" || dc.Fallback != nil {
		t.Fatalf("refused transition mutated the contract: %+v", dc)
	}
}

// TestReconcileDeliveryFallbackRefusesUncontractedLaunch pins the launch-time
// invariant D1 established: by this point the task is contracted. A launch
// that reached the reconcile with no contract is a broken launch, not a
// silent pass.
func TestReconcileDeliveryFallbackRefusesUncontractedLaunch(t *testing.T) {
	f := newLaunchFixture(t, "fallback-uncontracted")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	err := f.runner.reconcileDeliveryFallback()
	if err == nil || !strings.Contains(err.Error(), "carries no delivery contract") {
		t.Fatalf("reconcile on an uncontracted task = %v", err)
	}
}

// TestReconcileDeliveryFallbackRefusesWithoutAuthority pins the composition
// guard: the reconcile is never skipped silently when the Authority is absent.
func TestReconcileDeliveryFallbackRefusesWithoutAuthority(t *testing.T) {
	r := &Runner{args: Args{ID: "t1"}, effectiveMode: "direct-PR"}
	err := r.reconcileDeliveryFallback()
	if err == nil || !strings.Contains(err.Error(), "task authority is not composed") {
		t.Fatalf("reconcile without authority = %v", err)
	}
}

// TestReScaffoldAfterFallbackClearsTransition pins the re-scaffold as a fresh
// contract on the fleet side too: an explicit --mode on the spawn that opens
// the next generation replaces the moved contract and carries no inherited
// transition.
func TestReScaffoldAfterFallbackClearsTransition(t *testing.T) {
	f := newLaunchFixture(t, "fallback-rescaffold")
	contractedLaunch(t, f, "no-mistakes")
	r := f.runner
	r.effectiveMode = "direct-PR"
	r.fallbackReason = "no-mistakes blocked (agent-unavailable): gate agent not on PATH"
	if err := r.reconcileDeliveryFallback(); err != nil {
		t.Fatalf("reconcileDeliveryFallback: %v", err)
	}
	// A re-scaffold is an explicit mode selection on the spawn that opens a
	// NEW generation, so the task is reopened first.
	reopenTaskForFallbackTest(t, f)

	r.args.Mode = "local-only"
	r.effectiveMode = ""
	r.requestedMode = ""
	r.contractMode = ""
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if !r.rescaffoldContract {
		t.Fatal("an explicit --mode differing from the moved contract must re-scaffold")
	}
	if err := r.recordDeliveryContract(); err != nil {
		t.Fatalf("recordDeliveryContract: %v", err)
	}
	dc := contractOf(t, f)
	if dc.Mode != "local-only" {
		t.Fatalf("re-scaffold did not replace the moved contract: %+v", dc)
	}
	if dc.Fallback != nil {
		t.Fatalf("re-scaffolded contract inherited a transition: %+v", *dc.Fallback)
	}
}

// reopenTaskForFallbackTest closes and reopens the fixture's task so the next
// spawn resolves against a fresh generation, exactly as a real re-scaffold
// spawn does.
func reopenTaskForFallbackTest(t *testing.T, f *launchFixture) {
	t.Helper()
	agg, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	complete := taskauthority.CanonicalCompleteRequest{
		HomeID:       f.auth.HomeID(),
		TaskID:       mustTaskID(t, f.taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		To:           taskauthority.PhaseDone,
		Reason:       "done",
	}
	completeOp, err := domain.NewOperation(mustOpID(t, "op-complete-"+f.taskID), complete)
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	if _, err := f.auth.Complete(completeOp, complete); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	agg, err = f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	reopen := taskauthority.CanonicalReopenRequest{
		HomeID:       f.auth.HomeID(),
		TaskID:       mustTaskID(t, f.taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Reason:       "reopen",
	}
	reopenOp, err := domain.NewOperation(mustOpID(t, "op-reopen-"+f.taskID), reopen)
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	if _, err := f.auth.Reopen(reopenOp, reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
}

// TestRequireNoMistakesRefusesPostFallbackContract pins the interaction the
// recorded transition makes visible, and deliberately does NOT carve an
// exception for it. Generation N falls back to direct-PR under the configured
// allow-direct-pr-fallback policy and the contract records it. Generation N+1
// reads a contract of direct-PR under a project that requires no-mistakes and
// refuses loudly — the existing contracted-mode policy gate applied to the
// moved contract. The recourse is a re-scaffold, not a silent relaunch.
func TestRequireNoMistakesRefusesPostFallbackContract(t *testing.T) {
	f := newLaunchFixture(t, "fallback-require")
	contractedLaunch(t, f, "no-mistakes")
	r := f.runner
	r.effectiveMode = "direct-PR"
	r.fallbackReason = "no-mistakes blocked (agent-unavailable): gate agent not on PATH"
	if err := r.reconcileDeliveryFallback(); err != nil {
		t.Fatalf("reconcileDeliveryFallback: %v", err)
	}

	seedTypedDeliveryConfig(t, f, "", true)
	r.effectiveMode = ""
	r.requestedMode = ""
	r.contractMode = ""
	err := r.resolveMode()
	if err == nil {
		t.Fatal("require-no-mistakes accepted a task whose contract fell back to direct-PR")
	}
	if !strings.Contains(err.Error(), "require-no-mistakes") || !strings.Contains(err.Error(), "direct-PR") {
		t.Fatalf("refusal does not name the policy and the mode in force: %v", err)
	}
	if !strings.Contains(err.Error(), "re-scaffold") {
		t.Fatalf("refusal offers no recourse: %v", err)
	}
	dc := contractOf(t, f)
	if dc.Mode != "direct-PR" || dc.Fallback == nil {
		t.Fatalf("the refusal mutated the recorded transition: %+v", dc)
	}
}
