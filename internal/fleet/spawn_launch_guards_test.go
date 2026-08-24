package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// The refusal branches on the spawn/launch path. Every fixture case here
// drives the REAL phases to the point the guard protects, breaks exactly one
// durable identity, and re-runs the same phase. The control (the identical
// re-run BEFORE the tamper must replay, not refuse) is what proves the refusal
// came from the branch under test rather than from an earlier one.

// probeStateEndpoints is a spawn endpoint capability whose probe answers a
// fixed observation. It exists for the readiness guards, which need a probe
// that is neither alive nor a narrow structured absence.
type probeStateEndpoints struct {
	state    EndpointObservationState
	capture  string
	submits  int
	disposed int
}

func (p *probeStateEndpoints) CreateReserved(req CreateRequest) (CreatedEndpoint, error) {
	return CreatedEndpoint{Backend: "tmux", Handle: "pane-1"}, nil
}

func (p *probeStateEndpoints) Submit(ep CreatedEndpoint, text string) error {
	p.submits++
	return nil
}

func (p *probeStateEndpoints) Probe(ep CreatedEndpoint) (SpawnEndpointObservation, error) {
	return endpointStatusFromState(p.state), nil
}

func (p *probeStateEndpoints) Capture(ep CreatedEndpoint, n int) (string, error) {
	if p.capture == "" {
		return "> ready", nil
	}
	return p.capture, nil
}

func (p *probeStateEndpoints) Dispose(ep CreatedEndpoint) error {
	p.disposed++
	return nil
}

// Every canonical phase refuses to act without a composed Task Authority:
// without it there is no generation, no precondition and no journal, so any
// "success" would be an unrecorded side effect.
func TestSpawnPhasesRefuseWithoutComposedAuthority(t *testing.T) {
	cases := []struct {
		name    string
		run     func(r *Runner) error
		wantSub string
	}{
		{"task aggregate", func(r *Runner) error { _, err := r.taskAggregate(); return err }, "task authority is not composed for spawn"},
		{"begin launch intent", (*Runner).beginLaunchIntent, "launch intent: task authority is not composed for spawn"},
		{"bind worktree", func(r *Runner) error { _, err := r.bindWorktree(); return err }, "binding worktree before endpoint launch: task authority is not composed for spawn"},
		{"attach endpoint", (*Runner).attachEndpoint, "attaching acquired endpoint: task authority is not composed for spawn"},
		{"submit launch", (*Runner).submitLaunch, "submitting launch: task authority is not composed for spawn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{args: Args{ID: "no-authority", Kind: "ship"}}
			err := tc.run(r)
			if err == nil {
				t.Fatal("phase proceeded with no composed task authority")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want %q", err, tc.wantSub)
			}
		})
	}
}

// A canonical launch acquires its worktree under the launch intent's one-time
// reservation. With no committed intent there is no reservation to acquire
// under, and an unreserved acquisition is refused rather than falling back to
// a plain pool get.
func TestAcquireWorktreeRefusesWithoutLaunchReservation(t *testing.T) {
	r := &Runner{args: Args{ID: "no-reservation", Kind: "ship"}}
	if r.wtReservationID() != "" {
		t.Fatal("fixture invalid: a reservation exists before any launch intent")
	}

	err := r.acquireWorktree()
	if err == nil {
		t.Fatal("acquireWorktree proceeded with no launch worktree reservation")
	}
	if !strings.Contains(err.Error(), "no launch worktree reservation") {
		t.Fatalf("error = %v, want the missing-reservation refusal", err)
	}
	if r.wtPath != "" {
		t.Fatalf("refused acquisition still set a worktree path %q", r.wtPath)
	}
}

// The launch intent binds the immutable typed snapshot digest. Without a
// loaded snapshot there is no configuration identity to bind the launch to,
// so the intent is refused before any acquisition.
func TestBeginLaunchIntentRefusesWithoutSnapshotIdentity(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(*Runner)
	}{
		{"snapshot not loaded", func(r *Runner) { r.projectConfigLoaded = false }},
		{"snapshot digest empty", func(r *Runner) { r.projectConfig.SnapshotDigest = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchFixture(t, "no-snapshot-identity")
			loaded, digest := f.runner.projectConfigLoaded, f.runner.projectConfig.SnapshotDigest
			tc.break_(f.runner)

			err := f.runner.beginLaunchIntent()
			if err == nil {
				t.Fatal("beginLaunchIntent committed an intent with no snapshot identity")
			}
			if !strings.Contains(err.Error(), "spawn requires the typed project snapshot") {
				t.Fatalf("error = %v, want the snapshot-identity refusal", err)
			}
			if agg := f.aggregate(); agg.Launch != nil {
				t.Fatal("refused intent was still committed")
			}

			// Control: restoring only the snapshot identity commits the intent,
			// so the refusal above came from this branch.
			f.runner.projectConfigLoaded, f.runner.projectConfig.SnapshotDigest = loaded, digest
			if err := f.runner.beginLaunchIntent(); err != nil {
				t.Fatalf("repaired runner still refused: %v", err)
			}
			if agg := f.aggregate(); agg.Launch == nil {
				t.Fatal("repaired run committed no launch intent")
			}
		})
	}
}

// A first launch is only allowed out of the queued phase: a task already
// working (or done) with no committed intent means another live spawn owns it.
func TestBeginLaunchIntentRefusesNonQueuedPhaseWithoutIntent(t *testing.T) {
	f := newLaunchFixture(t, "not-queued")
	tamperTaskAggregate(t, f.homeDir, f.taskID, func(agg *taskauthority.Aggregate) {
		agg.Phase = taskauthority.PhaseWorking
	})
	if agg := f.aggregate(); agg.Launch != nil {
		t.Fatal("fixture invalid: a launch intent exists, so this is the re-entry branch, not the first-launch one")
	}

	err := f.runner.beginLaunchIntent()
	if err == nil {
		t.Fatal("beginLaunchIntent minted an intent for a non-queued task")
	}
	if !strings.Contains(err.Error(), "spawn requires queued; duplicate live spawn refused") {
		t.Fatalf("error = %v, want the first-launch non-queued refusal", err)
	}

	// Control: the same fixture with the phase left queued commits the intent,
	// so the phase is what the refusal above came from.
	queued := newLaunchFixture(t, "still-queued")
	if err := queued.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("queued task refused: %v", err)
	}
}

// A Captain may not spawn over a task that already has a live soldier
// endpoint. The pane projection is the herdr-side proof of that, and is a
// separate refusal from the tmux window one.
func TestCaptainBacklogAuthorityRefusesLivePaneSession(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	auth := canonicalAtHome(t, homeDir)
	canonicalCreateTask(t, auth, "live-pane", "ship", "test-proj")
	if err := home.WriteMeta(homeDir, "live-pane", map[string]string{"herdr_pane_id": "%42"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	r := &Runner{
		homeDir:   homeDir,
		args:      Args{ID: "live-pane", Kind: "ship", Authority: auth},
		spawnRole: "captain",
	}

	err := r.checkCaptainBacklogAuthority()
	if err == nil {
		t.Fatal("captain spawn allowed over a live pane session")
	}
	if !strings.Contains(err.Error(), "already has a live soldier session (pane=%42)") {
		t.Fatalf("error = %v, want the live-pane refusal", err)
	}

	// Control: the same queued task with no pane projection is allowed, so
	// the refusal came from the pane branch and not from the phase check.
	if err := home.WriteMeta(homeDir, "live-pane", map[string]string{}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("queued task with no live session refused: %v", err)
	}
}

// Session creation is a capability call. Without composed endpoint
// capabilities there is nothing to create through, and the phase refuses
// rather than proceeding endpoint-less.
func TestCreateSessionRefusesWithoutEndpointCapabilities(t *testing.T) {
	r := &Runner{args: Args{ID: "no-endpoints", Kind: "ship"}}
	err := r.createSession()
	if err == nil {
		t.Fatal("createSession proceeded with no endpoint capabilities")
	}
	if !strings.Contains(err.Error(), "spawn endpoint capabilities are required") {
		t.Fatalf("error = %v, want the missing-capabilities refusal", err)
	}
}

// Recovery re-adopts the durably recorded endpoint only when it belongs to
// the same backend the launch intent committed to. A recorded endpoint on
// another backend is a different acquisition and is never adopted.
func TestCreateSessionRefusesRecordedEndpointOnAnotherBackend(t *testing.T) {
	f := newLaunchFixture(t, "backend-mismatch")
	if err := runLaunchPhases(f, "attach-endpoint"); err != errCrashSimulated {
		t.Fatalf("runLaunchPhases through attach: %v", err)
	}

	// Control: the recorded endpoint matches the intent, so recovery adopts it.
	if err := f.runner.createSession(); err != nil {
		t.Fatalf("recovery over a matching recorded endpoint refused: %v", err)
	}

	tamperTaskAggregate(t, f.homeDir, f.taskID, func(agg *taskauthority.Aggregate) {
		agg.AcquiredEndpoint.Backend = "herdr"
	})
	err := f.runner.createSession()
	if err == nil {
		t.Fatal("recovery adopted a recorded endpoint on a different backend")
	}
	if !strings.Contains(err.Error(), `does not match launch intent backend "tmux"`) {
		t.Fatalf("error = %v, want the backend-mismatch refusal", err)
	}
	if f.endpoints.createCount() != 1 {
		t.Fatalf("refused recovery created a replacement endpoint: %d creates", f.endpoints.createCount())
	}
}

// The durable acquired-endpoint record is fenced by the launch intent's
// endpoint reservation. A record whose identity matches but whose fence does
// not belongs to a different launch, and is never adopted.
func TestAttachEndpointRefusesRecordOutsideTheLaunchFence(t *testing.T) {
	f := newLaunchFixture(t, "attach-fence")
	if err := runLaunchPhases(f, "attach-endpoint"); err != errCrashSimulated {
		t.Fatalf("runLaunchPhases through attach: %v", err)
	}

	// Control: re-running attach over the same record is idempotent.
	if err := f.runner.attachEndpoint(); err != nil {
		t.Fatalf("idempotent re-attach refused: %v", err)
	}

	cases := []struct {
		name   string
		tamper func(*taskauthority.AcquiredEndpoint)
	}{
		{"foreign lease", func(a *taskauthority.AcquiredEndpoint) { a.LeaseID = "lease-from-another-launch" }},
		{"foreign fence token", func(a *taskauthority.AcquiredEndpoint) { a.FenceToken = "fence-from-another-launch" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchFixture(t, "attach-fence-"+strings.ReplaceAll(tc.name, " ", "-"))
			if err := runLaunchPhases(f, "attach-endpoint"); err != errCrashSimulated {
				t.Fatalf("runLaunchPhases through attach: %v", err)
			}
			tamperTaskAggregate(t, f.homeDir, f.taskID, func(agg *taskauthority.Aggregate) {
				tc.tamper(agg.AcquiredEndpoint)
			})

			err := f.runner.attachEndpoint()
			if err == nil {
				t.Fatal("attach adopted a record outside the launch endpoint fence")
			}
			if !strings.Contains(err.Error(), "does not match the launch endpoint reservation fence") {
				t.Fatalf("error = %v, want the endpoint-fence refusal", err)
			}
		})
	}
}

// Recorded launch evidence pins the exact submitted command. A recorded
// digest that differs is a different submission, so the launch refuses rather
// than submitting a second command under the same launch identity.
func TestSubmitLaunchRefusesEvidenceForAnotherSubmission(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(*taskauthority.LaunchEvidence)
	}{
		{"another launch id", func(e *taskauthority.LaunchEvidence) { e.LaunchID = "launch-from-another-attempt" }},
		// The digest must stay shape-valid (64-hex) so the refusal under test
		// is the identity comparison, not the state validator.
		{"another command digest", func(e *taskauthority.LaunchEvidence) {
			e.CommandDigest = strings.Repeat("ab", 32)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchFixture(t, "submit-evidence-"+strings.ReplaceAll(tc.name, " ", "-"))
			if err := runLaunchPhases(f, "submit"); err != errCrashSimulated {
				t.Fatalf("runLaunchPhases through submit: %v", err)
			}
			submitsAfterFirst := f.endpoints.submitCount()

			// Control: re-running submit over its own evidence replays.
			if err := f.runner.submitLaunch(); err != nil {
				t.Fatalf("idempotent re-submit refused: %v", err)
			}
			if f.endpoints.submitCount() != submitsAfterFirst {
				t.Fatalf("replay submitted a second command: %d", f.endpoints.submitCount())
			}

			tamperTaskAggregate(t, f.homeDir, f.taskID, func(agg *taskauthority.Aggregate) {
				tc.tamper(agg.LaunchEvidence)
			})
			err := f.runner.submitLaunch()
			if err == nil {
				t.Fatal("submit replayed over evidence from another submission")
			}
			if !strings.Contains(err.Error(), "does not match this launch") {
				t.Fatalf("error = %v, want the launch-evidence refusal", err)
			}
			if f.endpoints.submitCount() != submitsAfterFirst {
				t.Fatalf("refused submit still delivered a command: %d", f.endpoints.submitCount())
			}
		})
	}
}

// The committed endpoint binding is fenced by the launch intent's endpoint
// reservation. A binding outside that fence belongs to another launch, so the
// confirmation refuses rather than reporting someone else's spawn as replayed.
func TestConfirmSpawnRefusesBindingOutsideTheLaunchFence(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(*taskauthority.EndpointBinding)
	}{
		{"foreign lease", func(b *taskauthority.EndpointBinding) { b.LeaseID = "lease-from-another-launch" }},
		{"foreign fence token", func(b *taskauthority.EndpointBinding) { b.FenceToken = "fence-from-another-launch" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchFixture(t, "confirm-fence-"+strings.ReplaceAll(tc.name, " ", "-"))
			if err := runLaunchPhases(f, ""); err != nil {
				t.Fatalf("runLaunchPhases: %v", err)
			}

			// Control: the committed binding is this launch's, so confirm replays.
			out, err := f.runner.confirmSpawn()
			if err != nil {
				t.Fatalf("replayed confirm refused: %v", err)
			}
			if !out.Replayed {
				t.Fatal("confirm over a committed binding did not replay")
			}

			tamperTaskAggregate(t, f.homeDir, f.taskID, func(agg *taskauthority.Aggregate) {
				tc.tamper(agg.Endpoint)
			})
			if _, err := f.runner.confirmSpawn(); err == nil {
				t.Fatal("confirm replayed over a binding outside the launch fence")
			} else if !strings.Contains(err.Error(), "committed endpoint binding does not match the launch endpoint reservation fence") {
				t.Fatalf("error = %v, want the binding-fence refusal", err)
			}
		})
	}
}

// A harness whose binary is installed but whose credentials are not
// configured fails preflight before any resource is allocated.
func TestPreflightHarnessRefusesUnconfiguredAuth(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("writing claude stub: %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("ANTHROPIC_API_KEY", "")

	r := &Runner{args: Args{ID: "auth-absent", Kind: "ship"}, harness: "claude"}
	err := r.preflightHarness()
	if err == nil {
		t.Fatal("preflight passed a harness with no configured auth")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Fatalf("error = %v, want a claude preflight error", err)
	}

	// Control: with credentials configured the same harness passes, so the
	// refusal came from the auth branch and not from the binary one.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	if err := r.preflightHarness(); err != nil {
		t.Fatalf("configured harness still refused preflight: %v", err)
	}
}

// Readiness waiting concludes "the window died" only from an authorized exact
// absence: the bound handle, probed dead, under the launch fence and the
// current generation.
func TestWaitForHarnessReadyRefusesOnAuthorizedAbsence(t *testing.T) {
	f := newLaunchFixture(t, "ready-absent")
	if err := runLaunchPhases(f, "attach-endpoint"); err != errCrashSimulated {
		t.Fatalf("runLaunchPhases through attach: %v", err)
	}
	// Control: the same wait over a live endpoint returns ready.
	if err := f.runner.waitForHarnessReady(5); err != nil {
		t.Fatalf("live endpoint was not seen ready: %v", err)
	}

	f.endpoints.probeAlive = false
	err := f.runner.waitForHarnessReady(5)
	if err == nil {
		t.Fatal("wait reported ready over a dead window")
	}
	if !strings.Contains(err.Error(), "window died while waiting for ready") {
		t.Fatalf("error = %v, want the authorized-absence refusal", err)
	}
}

// An observation that is neither alive nor starting — and not a narrow
// authorized absence either — is not readiness. It refuses instead of
// polling on against an endpoint whose state is not readable.
func TestWaitForHarnessReadyRefusesUnreadableObservation(t *testing.T) {
	eps := &probeStateEndpoints{state: EndpointUnresponsive}
	r := &Runner{
		args:      Args{ID: "ready-unreadable", Kind: "ship"},
		harness:   "pi",
		endpoints: eps,
		endpoint:  CreatedEndpoint{Backend: "tmux", Handle: "pane-1"},
		windowID:  "pane-1",
	}

	err := r.waitForHarnessReady(5)
	if err == nil {
		t.Fatal("wait reported ready over an unreadable observation")
	}
	if strings.Contains(err.Error(), "window died") {
		t.Fatalf("unreadable observation was concluded absent: %v", err)
	}
	if !strings.Contains(err.Error(), "while waiting for ready") {
		t.Fatalf("error = %v, want the unreadable-observation refusal", err)
	}

	// Control: the identical runner over an alive observation reaches the
	// ready pattern, so the refusal came from the lifecycle branch.
	eps.state = EndpointAlive
	if err := r.waitForHarnessReady(5); err != nil {
		t.Fatalf("alive endpoint was not seen ready: %v", err)
	}
}

// The attestation reference is the projected evidence of the accepted
// capability attestation. A missing attestation or a missing home binding
// would project a malformed reference, so acceptance refuses first.
func TestValidateAttestationReferenceRefusesIncompleteReference(t *testing.T) {
	if err := validateAttestationReference(nil); err == nil {
		t.Fatal("acceptance passed with no capability attestation")
	} else if !strings.Contains(err.Error(), "requires a capability attestation") {
		t.Fatalf("error = %v, want the missing-attestation refusal", err)
	}

	att := &CapabilityAttestation{Project: "test-proj", Home: "  "}
	if err := validateAttestationReference(att); err == nil {
		t.Fatal("acceptance passed with no home binding")
	} else if !strings.Contains(err.Error(), "requires a home binding") {
		t.Fatalf("error = %v, want the missing-home refusal", err)
	}

	// Control: the project binding was already present, so repairing only the
	// home binding is accepted.
	att.Home = "/homes/general"
	if err := validateAttestationReference(att); err != nil {
		t.Fatalf("complete reference refused: %v", err)
	}
}

func TestCaptainBacklogAuthorityHelperUnknownPhaseRefuses(t *testing.T) {
	r := &Runner{homeDir: t.TempDir()}
	err := r.checkCaptainBacklogAuthorityForPhase("task-unknown", taskauthority.Phase("unknown"))
	if err == nil || !strings.Contains(err.Error(), "unexpected phase") {
		t.Fatalf("checkCaptainBacklogAuthorityForPhase error = %v, want unexpected-phase refusal", err)
	}
}
