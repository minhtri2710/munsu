package fleet

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// captureContract resolves the fixture's launch and records its delivery
// contract on the task's current generation, as the first spawn does.
func captureContract(t *testing.T, f *launchFixture) {
	t.Helper()
	r := f.runner
	r.effectiveMode = ""
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if err := r.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := r.recordDeliveryContract(); err != nil {
		t.Fatalf("recordDeliveryContract: %v", err)
	}
}

// seedDeliveryConfig stores the typed config documents with the fixture
// project's review and forge tool entries (nil means baseline).
func seedDeliveryConfig(t *testing.T, f *launchFixture, review, forge *config.ToolEntry) {
	t.Helper()
	storeTestDocuments(t, f.runner.homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{Backend: "tmux", SoldierHarness: "pi", Model: "gpt-5"},
	}, []testProjectRecord{
		{Name: "test-proj", Path: f.runner.projPath, Config: config.ProjectOverlay{Review: review, Forge: forge}},
	}, nil)
}

func contractOf(t *testing.T, f *launchFixture) *taskauthority.DeliveryContract {
	t.Helper()
	agg, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	return agg.DeliveryContract
}

// TestRecordDeliveryContractOnFirstLaunch pins the capture: generation 1 of a
// task carries the resolved mode and the captured review and forge steps
// durably after the launch intent commits. A re-entrant record under recovery
// does not record a second time.
func TestRecordDeliveryContractOnFirstLaunch(t *testing.T) {
	f := newLaunchFixture(t, "contract-record")
	installFakeGH(t)
	seedDeliveryConfig(t, f, nil, &config.ToolEntry{Adapter: "github"})
	captureContract(t, f)

	agg, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Generation != 1 {
		t.Fatalf("generation = %d", agg.Generation)
	}
	dc := agg.DeliveryContract
	if dc == nil || dc.Mode != "direct-PR" || !dc.Review.Baseline || dc.Forge.Adapter != "github" {
		t.Fatalf("contract = %+v, want direct-PR with baseline review and github forge", dc)
	}

	before := agg.Revision
	if err := f.runner.recordDeliveryContract(); err != nil {
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

// TestContractedLaunchReadsCapturedStepsOverDriftedConfig pins the read-back:
// within a generation the captured contract decides the launch. A project
// whose current forge tool is no longer Ready still launches the contracted
// task, because a fresh resolution of that config would refuse.
func TestContractedLaunchReadsCapturedStepsOverDriftedConfig(t *testing.T) {
	f := newLaunchFixture(t, "contract-read")
	seedDeliveryConfig(t, f, nil, nil)
	captureContract(t, f)

	// gh-axi is absent from the sanitized fixture PATH, so the drifted forge
	// is not Ready.
	seedDeliveryConfig(t, f, nil, &config.ToolEntry{Adapter: "github"})
	if _, err := ResolveSpawnProjectConfig(f.runner.homeDir, Args{ProjectName: "test-proj"}, DispatchPolicyGeneralDirect, nil); err == nil {
		t.Fatal("fixture: a fresh resolution of the drifted config must refuse")
	}

	r := f.runner
	r.effectiveMode = ""
	if err := r.resolveMode(); err != nil {
		t.Fatalf("contracted launch refused over drifted config: %v", err)
	}
	if r.effectiveMode != "local-only" || !r.forgeStep.Baseline {
		t.Fatalf("launch read %q with forge %+v, want the captured local-only baseline", r.effectiveMode, r.forgeStep)
	}
}

// TestContractedLaunchReprobesCapturedForgeBeforeLaunch pins the launch
// re-probe: a captured forge tool that is no longer Ready fails the launch
// closed in preflight, before any launch intent or worktree is taken.
func TestContractedLaunchReprobesCapturedForgeBeforeLaunch(t *testing.T) {
	f := newLaunchFixture(t, "contract-reprobe")
	installFakeGH(t)
	seedDeliveryConfig(t, f, nil, &config.ToolEntry{Adapter: "github"})
	captureContract(t, f)

	oldAxi := ghAxiLookPath
	t.Cleanup(func() { ghAxiLookPath = oldAxi })
	ghAxiLookPath = func() (string, error) { return "", errors.New("gh-axi removed after capture") }

	r := f.runner
	r.effectiveMode = ""
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	err := r.preflightDelivery()
	if err == nil || !strings.Contains(err.Error(), "configured forge is not Ready") {
		t.Fatalf("preflightDelivery = %v, want a fail-closed forge refusal", err)
	}
}

// TestResolveModeReopenRecapturesConfig pins the per-generation capture: a
// reopen does not carry the prior contract forward, so the new generation
// captures the steps the project configures now.
func TestResolveModeReopenRecapturesConfig(t *testing.T) {
	f := newLaunchFixture(t, "contract-reopen")
	seedDeliveryConfig(t, f, nil, nil)
	captureContract(t, f)

	reopenTaskForBranchTest(t, f)
	if dc := contractOf(t, f); dc != nil {
		t.Fatalf("reopen carried the prior contract forward: %+v", *dc)
	}

	installFakeGH(t)
	seedDeliveryConfig(t, f, nil, &config.ToolEntry{Adapter: "github"})
	captureContract(t, f)
	dc := contractOf(t, f)
	if dc == nil || dc.Mode != "direct-PR" || dc.Forge.Adapter != "github" {
		t.Fatalf("reopened generation captured %+v, want direct-PR with github forge", dc)
	}
}

// TestResolveModeRefusesNotReadyConfiguredToolBeforeMutation pins the refusal
// before mutation: a configured review tool that is not Ready refuses in
// resolveMode, and the task keeps no launch intent or contract.
func TestResolveModeRefusesNotReadyConfiguredToolBeforeMutation(t *testing.T) {
	f := newLaunchFixture(t, "contract-refuse")
	installFakeGH(t)
	seedDeliveryConfig(t, f, &config.ToolEntry{Adapter: "no-mistakes", Path: filepath.Join(t.TempDir(), "no-mistakes")}, &config.ToolEntry{Adapter: "github"})
	before, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}

	r := f.runner
	err = r.resolveMode()
	if err == nil || !strings.Contains(err.Error(), "review adapter no-mistakes probe failed") {
		t.Fatalf("resolveMode = %v, want a refusal naming the review step, adapter and probe", err)
	}
	after, err := f.auth.Get(mustTaskID(t, f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Launch != nil || after.DeliveryContract != nil {
		t.Fatalf("refused resolution mutated the task: revision %d -> %d, launch %v, contract %v", before.Revision, after.Revision, after.Launch, after.DeliveryContract)
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
	if dc := contractOf(t, f); dc != nil {
		t.Fatalf("refused launch contracted the task anyway: %+v", *dc)
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

// TestCapturedForgeStepRefusesUncontractedTask pins the refusal for a task with
// no captured contract: delivery has no forge step to read.
func TestCapturedForgeStepRefusesUncontractedTask(t *testing.T) {
	f := newLaunchFixture(t, "captured-none")
	_, err := capturedForgeStep(f.auth, f.taskID)
	if err == nil || !strings.Contains(err.Error(), "has no captured delivery contract") {
		t.Fatalf("capturedForgeStep on an uncontracted task = %v, want a missing-contract refusal", err)
	}
}

// TestResolveSpawnProjectConfigRefusesContractModeMismatch pins the refusal for
// a contract whose mode disagrees with its captured steps, before any config read.
func TestResolveSpawnProjectConfigRefusesContractModeMismatch(t *testing.T) {
	contract := &taskauthority.DeliveryContract{
		Mode:   "local-only",
		Review: taskauthority.DeliveryStep{Adapter: "no-mistakes", Path: "/bin/no-mistakes", ProbeState: "ready"},
		Forge:  taskauthority.DeliveryStep{Adapter: "github", Path: "/bin/gh-axi", ProbeState: "ready"},
	}
	_, err := ResolveSpawnProjectConfig(t.TempDir(), Args{ProjectName: "test-proj"}, DispatchPolicyGeneralDirect, contract)
	if err == nil || !strings.Contains(err.Error(), "disagrees with captured review and forge steps") {
		t.Fatalf("ResolveSpawnProjectConfig = %v, want a contract mode refusal", err)
	}
}
