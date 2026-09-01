package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// recordContractForTask writes a durable delivery contract onto a task's
// current generation through the canonical op, exactly as a prior spawn would
// have.
func recordContractForTask(t *testing.T, auth *taskauthority.Canonical, taskID, mode string) {
	t.Helper()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatalf("Get(%s): %v", taskID, err)
	}
	req := taskauthority.CanonicalRecordDeliveryContractRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Mode:         mode,
		Reason:       "test",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-contract-"+taskID+"-"+mode), req)
	if err != nil {
		t.Fatalf("NewOperation: %v", err)
	}
	if _, err := auth.RecordDeliveryContract(op, req); err != nil {
		t.Fatalf("RecordDeliveryContract(%s, %s): %v", taskID, mode, err)
	}
}

// TestResolveModeReadsRecordedContract pins the read-back: a task that already
// carries a delivery contract launches under the contracted mode instead of
// whatever a fresh resolution would pick this time.
func TestResolveModeReadsRecordedContract(t *testing.T) {
	f := newLaunchFixture(t, "contract-read")
	recordContractForTask(t, f.auth, f.taskID, "local-only")

	r := f.runner
	r.effectiveMode = ""
	r.requestedMode = ""
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if r.effectiveMode != "local-only" || r.requestedMode != "local-only" {
		t.Fatalf("contract not read back: effective=%q requested=%q", r.effectiveMode, r.requestedMode)
	}
	if r.contractMode != "local-only" {
		t.Fatalf("contractMode = %q", r.contractMode)
	}
	if r.rescaffoldContract {
		t.Fatal("reading a contract is not a re-scaffold")
	}
}

// TestResolveModeWithoutContractResolvesFresh pins the first-spawn path: no
// contract means the configured resolution decides, and its result becomes the
// mode to record.
func TestResolveModeWithoutContractResolvesFresh(t *testing.T) {
	f := newLaunchFixture(t, "contract-absent")
	r := f.runner
	r.effectiveMode = ""
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	// PATH is sanitized to git only in the fixture, so auto-resolution lands
	// on direct-PR.
	if r.effectiveMode != "direct-PR" || r.contractMode != "direct-PR" {
		t.Fatalf("fresh resolution = %q / %q", r.effectiveMode, r.contractMode)
	}
}

// TestResolveModeExplicitModeReScaffoldsContract pins the one sanctioned
// contract change: an explicit --mode differing from the recorded contract
// wins and is marked for re-recording.
func TestResolveModeExplicitModeReScaffoldsContract(t *testing.T) {
	f := newLaunchFixture(t, "contract-rescaffold")
	recordContractForTask(t, f.auth, f.taskID, "local-only")

	r := f.runner
	r.args.Mode = "direct-PR"
	r.effectiveMode = ""
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if r.effectiveMode != "direct-PR" || r.contractMode != "direct-PR" {
		t.Fatalf("explicit mode did not win: %q / %q", r.effectiveMode, r.contractMode)
	}
	if !r.rescaffoldContract {
		t.Fatal("differing explicit mode must be marked as a re-scaffold")
	}
	if err := r.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := r.recordDeliveryContract(); err != nil {
		t.Fatalf("recordDeliveryContract: %v", err)
	}
	agg, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract == nil || agg.DeliveryContract.Mode != "direct-PR" {
		t.Fatalf("re-scaffold did not re-record the contract: %+v", agg.DeliveryContract)
	}
}

// TestRecordDeliveryContractOnFirstLaunch pins the record: generation 1 of a
// task carries the resolved mode durably after the launch intent commits.
func TestRecordDeliveryContractOnFirstLaunch(t *testing.T) {
	f := newLaunchFixture(t, "contract-record")
	r := f.runner
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if err := r.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := r.recordDeliveryContract(); err != nil {
		t.Fatalf("recordDeliveryContract: %v", err)
	}
	agg, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Generation != 1 {
		t.Fatalf("generation = %d", agg.Generation)
	}
	if agg.DeliveryContract == nil || agg.DeliveryContract.Mode != r.contractMode {
		t.Fatalf("contract = %+v, want mode %q", agg.DeliveryContract, r.contractMode)
	}

	// Re-entry under recovery must not record a second time.
	before := agg.Revision
	if err := r.recordDeliveryContract(); err != nil {
		t.Fatalf("re-entrant recordDeliveryContract: %v", err)
	}
	after, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before {
		t.Fatalf("re-entry bumped the revision %d -> %d", before, after.Revision)
	}
}

// TestRecordDeliveryContractRefusesUncontractedLaunch builds the refused
// state: a launch that reached the record with no valid resolved mode never
// contracts the task.
func TestRecordDeliveryContractRefusesUncontractedLaunch(t *testing.T) {
	f := newLaunchFixture(t, "contract-uncontracted")
	r := f.runner
	if err := r.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	r.contractMode = ""
	err := r.recordDeliveryContract()
	if err == nil {
		t.Fatal("record accepted a launch with no resolved delivery mode")
	}
	if !strings.Contains(err.Error(), "no valid delivery mode") {
		t.Fatalf("refusal does not name the missing mode: %v", err)
	}
	agg, aggErr := f.auth.Get(mustTaskID(t, f.taskID))
	if aggErr != nil {
		t.Fatal(aggErr)
	}
	if agg.DeliveryContract != nil {
		t.Fatalf("refused launch contracted the task anyway: %+v", *agg.DeliveryContract)
	}
}

// TestRecordDeliveryContractRefusesWithoutAuthority pins the composition
// guard: the record is never skipped silently when the Authority is absent.
func TestRecordDeliveryContractRefusesWithoutAuthority(t *testing.T) {
	r := &Runner{args: Args{ID: "t1"}, contractMode: "direct-PR"}
	err := r.recordDeliveryContract()
	if err == nil || !strings.Contains(err.Error(), "task authority is not composed") {
		t.Fatalf("record without authority = %v", err)
	}
}

// seedTypedDeliveryConfig makes typed config available on a launch fixture's
// home with a chosen default mode and require-no-mistakes, so the contract
// tests exercise the production typed path (ResolveSpawnProjectConfig) rather
// than the untyped fallback.
func seedTypedDeliveryConfig(t *testing.T, f *launchFixture, defaultMode string, requireNoMistakes bool) {
	t.Helper()
	require := requireNoMistakes
	storeTestDocuments(t, f.runner.homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.ProjectOverlay{
			Backend:           "tmux",
			SoldierHarness:    "pi",
			Model:             "gpt-5",
			DefaultMode:       defaultMode,
			RequireNoMistakes: &require,
		},
	}, []testProjectRecord{
		{Name: "test-proj", Path: f.runner.projPath},
	}, nil)
	if !TypedConfigAvailable(f.runner.homeDir) {
		t.Fatal("fixture did not make typed config available")
	}
}

// TestTypedConfigContractOutranksDriftedDefaultMode is repair case (a): under
// typed config, a contracted task launches under its contract even when the
// project default mode has since drifted to a mode this machine cannot even
// run. The mode question is already answered, so the snapshot's mode
// resolution is skipped rather than allowed to fail the launch — and the
// contract is not re-recorded.
func TestTypedConfigContractOutranksDriftedDefaultMode(t *testing.T) {
	f := newLaunchFixture(t, "typed-contract-drift")
	recordContractForTask(t, f.auth, f.taskID, "direct-PR")
	// no-mistakes is absent from the fixture PATH, so a fresh resolution of
	// this default would hard-fail in EnsureDeliveryModeRunnable.
	seedTypedDeliveryConfig(t, f, "no-mistakes", false)

	r := f.runner
	r.effectiveMode = ""
	r.requestedMode = ""
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode refused a contracted task over a drifted default: %v", err)
	}
	if r.effectiveMode != "direct-PR" || r.contractMode != "direct-PR" {
		t.Fatalf("contract not authoritative under typed config: effective=%q contract=%q", r.effectiveMode, r.contractMode)
	}
	if r.projectConfig.Soldier.Mode != "direct-PR" {
		t.Fatalf("resolved snapshot mode = %q, want the contract mode", r.projectConfig.Soldier.Mode)
	}
	if r.rescaffoldContract {
		t.Fatal("reading a contract is not a re-scaffold")
	}

	if err := r.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	before, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.recordDeliveryContract(); err != nil {
		t.Fatalf("recordDeliveryContract: %v", err)
	}
	after, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision {
		t.Fatalf("an unchanged contract was re-recorded: revision %d -> %d", before.Revision, after.Revision)
	}
}

// TestTypedConfigReScaffoldViaOverlayThenFlag is repair case (b): the
// sanctioned way a contract changes under typed config. The project overlay
// default moves first, then the spawn asserts it with --mode — so the identity
// assertion passes on its own terms — and the differing contract is
// re-recorded. Changing a contract therefore costs a durable config change AND
// an explicit flag; neither alone is enough.
func TestTypedConfigReScaffoldViaOverlayThenFlag(t *testing.T) {
	f := newLaunchFixture(t, "typed-contract-rescaffold")
	recordContractForTask(t, f.auth, f.taskID, "direct-PR")
	seedTypedDeliveryConfig(t, f, "local-only", false)

	r := f.runner
	r.args.Mode = "local-only"
	r.effectiveMode = ""
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if r.effectiveMode != "local-only" || r.contractMode != "local-only" {
		t.Fatalf("overlay-then-flag re-scaffold did not take: effective=%q contract=%q", r.effectiveMode, r.contractMode)
	}
	if !r.rescaffoldContract {
		t.Fatal("a --mode differing from the contract must be marked as a re-scaffold")
	}
	if err := r.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := r.recordDeliveryContract(); err != nil {
		t.Fatalf("recordDeliveryContract: %v", err)
	}
	agg, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract == nil || agg.DeliveryContract.Mode != "local-only" {
		t.Fatalf("re-scaffold did not re-record the contract: %+v", agg.DeliveryContract)
	}
}

// TestTypedConfigModeAssertionSurvivesContract is repair case (c): a present
// contract does not buy a --mode past the snapshot identity assertion. A flag
// conflicting with the resolved project snapshot is still refused, so the
// registry can never be silently overridden — the overlay must move first.
func TestTypedConfigModeAssertionSurvivesContract(t *testing.T) {
	f := newLaunchFixture(t, "typed-contract-assertion")
	recordContractForTask(t, f.auth, f.taskID, "local-only")
	seedTypedDeliveryConfig(t, f, "local-only", false)

	r := f.runner
	r.args.Mode = "direct-PR"
	err := r.resolveMode()
	if err == nil {
		t.Fatal("a --mode conflicting with the snapshot default was accepted because a contract was present")
	}
	if !strings.Contains(err.Error(), "conflicts with resolved project snapshot") {
		t.Fatalf("refusal is not the snapshot identity assertion: %v", err)
	}
}

// TestTypedConfigRequireNoMistakesRefusesContractedTask is repair case (d):
// reading the contract does not skip the policy gate. A project that requires
// no-mistakes refuses to launch a task contracted to deliver some other way,
// and names the recourse.
func TestTypedConfigRequireNoMistakesRefusesContractedTask(t *testing.T) {
	f := newLaunchFixture(t, "typed-contract-require")
	recordContractForTask(t, f.auth, f.taskID, "direct-PR")
	seedTypedDeliveryConfig(t, f, "", true)

	r := f.runner
	err := r.resolveMode()
	if err == nil {
		t.Fatal("require-no-mistakes accepted a task contracted to deliver direct-PR")
	}
	if !strings.Contains(err.Error(), "require-no-mistakes") || !strings.Contains(err.Error(), "direct-PR") {
		t.Fatalf("refusal does not name the policy and the contracted mode: %v", err)
	}
	if !strings.Contains(err.Error(), "re-scaffold") {
		t.Fatalf("refusal offers no recourse: %v", err)
	}
}
