//go:build e2e

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// workflowEndpoints is the terminal-backend seam for the workflow scenario. It
// is reservation-aware exactly like production (repeated CreateReserved under
// one reservation returns the same endpoint) and reports a live, responsive
// endpoint so the Runner's authorizeLive proof is exercised rather than
// bypassed. Capture returns a ready pattern on the FIRST poll, so the
// readiness handshake completes without any wall-clock wait.
type workflowEndpoints struct {
	created  map[string]fleet.CreatedEndpoint
	creates  int
	submits  int
	captures int
}

func newWorkflowEndpoints() *workflowEndpoints {
	return &workflowEndpoints{created: map[string]fleet.CreatedEndpoint{}}
}

func (e *workflowEndpoints) CreateReserved(req fleet.CreateRequest) (fleet.CreatedEndpoint, error) {
	if ep, ok := e.created[req.ReservationID]; ok {
		return ep, nil
	}
	e.creates++
	ep := fleet.CreatedEndpoint{
		Backend:      "tmux",
		Handle:       "munsu:@" + req.ReservationID,
		SessionOwner: "munsu",
		WorkspaceID:  "ws-" + req.ReservationID,
		TabID:        "tab-" + req.ReservationID,
	}
	e.created[req.ReservationID] = ep
	return ep, nil
}

func (e *workflowEndpoints) Submit(fleet.CreatedEndpoint, string) error {
	e.submits++
	return nil
}

func (e *workflowEndpoints) Probe(fleet.CreatedEndpoint) (fleet.SpawnEndpointObservation, error) {
	return fleet.EndpointStatus{
		Lifecycle:      fleet.LifecycleAlive,
		Responsiveness: fleet.Responsive,
		Freshness:      fleet.FreshnessCurrent,
		Activity:       fleet.ActivityIdle,
		Source:         fleet.SourceProbe,
	}, nil
}

func (e *workflowEndpoints) Capture(fleet.CreatedEndpoint, int) (string, error) {
	e.captures++
	return "> ready", nil
}

func (e *workflowEndpoints) Dispose(fleet.CreatedEndpoint) error { return nil }

// workflowUplink is the notification transport the captain watcher hook
// requires (a nil transport is a fail-closed refusal). Delivery into General's
// terminal needs a resolvable runtime target, which a headless run has not
// got, so the hook queues instead of reaching this transport. The relay proof
// in this scenario is therefore the durable pending -> accepted transition of
// the uplink record, not a notification count.
type workflowUplink struct{}

func (workflowUplink) Notify(string, orchestrator.TargetResult, string) orchestrator.UplinkNotifyResult {
	return orchestrator.QueuedNotification()
}

// workflowActivation counts the activation nudges the captain watcher hook
// delivers to the Captain's own pane when a soldier receipt lands.
type workflowActivation struct {
	attempts int
}

func (a *workflowActivation) Attempt(string, orchestrator.TargetResult, string) orchestrator.ActivationAttempt {
	a.attempts++
	return orchestrator.ActivationAttempt{Acknowledged: true}
}

// workflowTeardown is the teardown backend seam. The soldier's endpoint is
// gone by the time its terminal report has landed, so the probe reports a
// narrow exact structured absence — the reading production needs to skip
// disposal — and the worktree return is counted so the scenario can prove the
// cleanup actually ran rather than being reported as pending.
type workflowTeardown struct {
	disposed int
	returned int
}

func (w *workflowTeardown) RefuseGate() error { return nil }

func (w *workflowTeardown) Probe(string, map[string]string) (fleet.RetirementEndpointStatus, error) {
	return fleet.RetirementEndpointStatus{
		Lifecycle:      fleet.LifecycleDead,
		Responsiveness: fleet.Unresponsive,
		Source:         fleet.SourceProbe,
	}, nil
}

func (w *workflowTeardown) Dispose(string, map[string]string, fleet.DisposeRequest) error {
	w.disposed++
	return nil
}

func (w *workflowTeardown) ReturnWorktree(string, string) error { w.returned++; return nil }

func (w *workflowTeardown) QueryMergeStatus(*domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	return nil, nil
}

func workflowInitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test User", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test User", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("workflow\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// workflowHarnessOnPath puts an executable stub for the named harness on PATH
// and supplies its credential env var, so harness.Preflight passes for the
// real reason (binary present, auth configured) rather than being skipped.
func workflowHarnessOnPath(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFakeExecutable(t, filepath.Join(dir, name), "#!/bin/sh\nexit 0\n")
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	testutil.SetPath(t, dir, filepath.Dir(gitBin))
	t.Setenv("ANTHROPIC_API_KEY", "workflow-e2e-stub")
	t.Setenv("OPENAI_API_KEY", "workflow-e2e-stub")
}

func workflowCanonical(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	auth, err := taskauthority.NewCanonical(mustOpenHome(t, homeDir))
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func workflowCreateTask(t *testing.T, auth *taskauthority.Canonical, taskID, project string) {
	t.Helper()
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := domain.NewProjectID(project)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID:                 auth.HomeID(),
		TaskID:                 tid,
		Owner:                  "owner",
		Kind:                   "scout",
		Project:                pid,
		ScoutScope:             "walk the full lifecycle for this dispatch policy",
		ScoutRuntimeBudgetSecs: 300,
		Reason:                 "create",
	}
	opID, err := domain.NewOperationID("op-create-" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, req); err != nil {
		t.Fatal(err)
	}
}

// workflowPublishedDigest is the config snapshot identity General assigns to
// the Captain. It exists in the Captain's published snapshot and nowhere else,
// so a General-direct dispatch can never resolve it.
const workflowPublishedDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// workflowCase is one topology policy driven end to end. The two rows differ
// only in the Fleet-boundary facts the dispatch policy is resolved from, and
// in the observable consequences that policy has downstream.
type workflowCase struct {
	name string
	// policy is the row of the dispatch-policy matrix this scenario walks.
	policy fleet.DispatchPolicy
	// role is MUNSU_ROLE for the spawning process.
	role string
	// model is named ONLY by the config surface the policy is allowed to read,
	// so reading the other surface resolves the other model and the spawn
	// assertion fails.
	model string
	// digest is the exact config snapshot identity that surface carries. The
	// Captain's is the published snapshot General assigned it; General's is
	// resolved from the fleet base and must never be the published one.
	digest string
	// parentCaptainID is the parent identity the policy resolves and the
	// launch envelope durably records.
	parentCaptainID string
	// relayed is how many durable captain->General uplinks the report leg must
	// leave for the supervision cycle to relay and retire. General-direct has
	// no captain in the loop: its terminal report closes its own handoff, so
	// any pending uplink at all is a topology violation.
	relayed int
	// activations is how many nudges the Captain's own pane must receive for
	// the soldier receipt that landed in its home. General has no Captain pane.
	activations int
}

func TestFleetLifecycleWorkflowAcrossTopologyPolicies(t *testing.T) {
	gateMarker, hadGateMarker := os.LookupEnv("NO_MISTAKES_GATE")
	if err := os.Unsetenv("NO_MISTAKES_GATE"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if hadGateMarker {
			_ = os.Setenv("NO_MISTAKES_GATE", gateMarker)
		} else {
			_ = os.Unsetenv("NO_MISTAKES_GATE")
		}
	}()
	cases := []workflowCase{
		{
			name:            "general-direct",
			policy:          fleet.DispatchPolicyGeneralDirect,
			role:            "general",
			model:           "workflow-general-model",
			parentCaptainID: "general",
			relayed:         0,
			activations:     0,
		},
		{
			name:            "captain-mediated",
			policy:          fleet.DispatchPolicyCaptainMediated,
			role:            "captain",
			model:           "workflow-captain-model",
			digest:          workflowPublishedDigest,
			parentCaptainID: "cap-1",
			relayed:         1,
			activations:     1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runLifecycleWorkflow(t, tc) })
	}
}

func runLifecycleWorkflow(t *testing.T, tc workflowCase) {
	t.Helper()
	taskID := "wf-" + tc.name

	// --- init -------------------------------------------------------------
	// A genuinely fresh home: nothing but home.Init and the typed config
	// documents the policy's own surface owns.
	generalHome := filepath.Join(t.TempDir(), "general")
	repo := workflowInitRepo(t)
	workflowHarnessOnPath(t, harness.Pi)
	if _, err := home.Init(generalHome); err != nil {
		t.Fatalf("init general home: %v", err)
	}
	if err := config.StoreFleetBase(generalHome, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.ProjectOverlay{
			SoldierHarness: harness.Pi,
			Model:          "workflow-general-model",
			DefaultMode:    "local-only",
			Backend:        "tmux",
		},
		CaptainProfile: config.CaptainProfile{Harness: harness.Pi},
	}); err != nil {
		t.Fatalf("store fleet base: %v", err)
	}
	if err := fleet.Add(generalHome, "alpha", repo, "local-only", false); err != nil {
		t.Fatalf("register project: %v", err)
	}

	spawnHome := generalHome
	if tc.policy == fleet.DispatchPolicyCaptainMediated {
		spawnHome = workflowSeedCaptainHome(t, generalHome, repo, tc.parentCaptainID)
	}

	// --- session-start ----------------------------------------------------
	workflowSessionStart(t, generalHome, "general", 0)
	if spawnHome != generalHome {
		workflowSessionStart(t, spawnHome, "captain", 0)
	}

	// --- spawn ------------------------------------------------------------
	auth := workflowCanonical(t, spawnHome)
	workflowCreateTask(t, auth, taskID, "alpha")
	briefDir := filepath.Join(spawnHome, "data", taskID)
	if err := os.MkdirAll(briefDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(briefDir, "brief.md"), []byte("# walk the lifecycle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	endpoints := newWorkflowEndpoints()
	t.Setenv("MUNSU_ROLE", tc.role)
	t.Chdir(spawnHome)
	if _, err := fleet.Spawn(fleet.Args{
		ID:          taskID,
		ProjectName: "alpha",
		Kind:        "scout",
		Mode:        "local-only",
		HomeDir:     spawnHome,
		Endpoints:   endpoints,
		Authority:   auth,
	}); err != nil {
		t.Fatalf("spawn under %s: %v", tc.policy, err)
	}
	if endpoints.creates != 1 || endpoints.submits != 1 {
		t.Fatalf("endpoint lifecycle: creates=%d submits=%d, want 1/1", endpoints.creates, endpoints.submits)
	}
	// The readiness handshake settled on the first poll. A second capture
	// would mean the Runner fell into its 2s backoff — the wall-clock wait
	// this scenario must never depend on.
	if endpoints.captures != 1 {
		t.Fatalf("harness readiness captures = %d, want 1 (a retry means a wall-clock wait)", endpoints.captures)
	}

	meta, err := home.ReadMeta(spawnHome, taskID)
	if err != nil {
		t.Fatalf("read task meta: %v", err)
	}
	// The model and the snapshot digest are named only by the config surface
	// this policy may read (published snapshot for captain-mediated, fleet
	// base for general-direct). Reading the other surface resolves the other
	// identity, so these two assertions pin the surface, not just the spawn.
	if meta["model"] != tc.model {
		t.Fatalf("meta model = %q, want %q (wrong config surface for %s)", meta["model"], tc.model, tc.policy)
	}
	switch {
	case tc.digest != "":
		if meta["config_snapshot_digest"] != tc.digest {
			t.Fatalf("meta config_snapshot_digest = %q, want the assigned published snapshot %q",
				meta["config_snapshot_digest"], tc.digest)
		}
	default:
		got := meta["config_snapshot_digest"]
		if got == "" || got == workflowPublishedDigest {
			t.Fatalf("meta config_snapshot_digest = %q, want a fleet-base-resolved digest", got)
		}
	}
	if meta["window"] == "" || meta["worktree"] == "" {
		t.Fatalf("meta is missing launch identity: %v", meta)
	}
	// The launch envelope durably records the parent identity the policy
	// resolved at the Fleet boundary.
	envelope := map[string]any{}
	raw, err := os.ReadFile(filepath.Join(meta["worktree"], fleet.EnvelopeName))
	if err != nil {
		t.Fatalf("read launch envelope: %v", err)
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode launch envelope: %v", err)
	}
	if got, _ := envelope["parent_captain_id"].(string); got != tc.parentCaptainID {
		t.Fatalf("envelope parent_captain_id = %q, want %q", got, tc.parentCaptainID)
	}

	// A session restarting over the live scout must arm supervision for it.
	workflowSessionStart(t, spawnHome, tc.role, 1)

	// --- report (soldier terminal uplink) ---------------------------------
	// The soldier's home and parent home are both the spawning home, exactly
	// as the generated launch script exports them.
	// The soldier writes the report generation-named, exactly as the brief
	// instructs (fresh task: generation 1).
	if err := os.MkdirAll(filepath.Dir(fleet.ReportPath(spawnHome, taskID, 1)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.ReportPath(spawnHome, taskID, 1), []byte("# findings\n"), 0644); err != nil {
		t.Fatal(err)
	}
	workflowRunCLI(t, spawnHome, map[string]string{
		"MUNSU_ROLE": "soldier", "MUNSU_HOME": spawnHome,
		"MUNSU_TASK_ID": taskID, "MUNSU_PARENT_STATUS": spawnHome,
	}, "report", "done", "findings recorded", "--key", taskID, "--ring", "no-ring")

	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseDone {
		t.Fatalf("phase after terminal report = %s, want done", agg.Phase)
	}
	// ADR-0015: the terminal report closes its own handoff. Nothing else can.
	receiptAcked := orchestrator.IsReceiptAcked(spawnHome, taskID, taskID)
	if !receiptAcked {
		t.Fatal("terminal receipt was not acknowledged by its writer")
	}
	relayOpen, err := orchestrator.IsTaskReportRelayOpen(spawnHome, taskID)
	if err != nil || relayOpen {
		t.Fatalf("report relay obligation open=%v err=%v, want closed", relayOpen, err)
	}

	// --- relay/ack + supervision wake -------------------------------------
	uplink := workflowUplink{}
	activation := &workflowActivation{}
	if tc.policy == fleet.DispatchPolicyCaptainMediated {
		// The Captain relays the soldier's terminal outcome up to General.
		// --ring no-ring leaves the uplink durable-but-undelivered, so what
		// clears it below is the supervision cycle's replay, not this write.
		workflowRunCLI(t, spawnHome, map[string]string{
			"MUNSU_ROLE": "captain", "MUNSU_HOME": spawnHome,
			"MUNSU_TASK_ID": "captain:" + tc.parentCaptainID, "MUNSU_PARENT_STATUS": generalHome,
		}, "report", "done", "soldier "+taskID+" reported done", "--key", taskID, "--ring", "no-ring")
	}

	// A General-direct dispatch has no captain in the loop: the terminal
	// report closed its own handoff and there is nothing to relay. Any
	// pending uplink here is a topology violation.
	pending := workflowPendingUplinks(t, spawnHome)
	pendingBeforeReplay := len(pending)
	if pendingBeforeReplay != tc.relayed {
		t.Fatalf("pending captain->General uplinks = %d, want %d under %s",
			len(pending), tc.relayed, tc.policy)
	}

	// The soldier's own terminal report enqueued a signal wake of its own, so
	// drain the queue first: whatever is read back below is then exactly what
	// the supervision cycle emitted and nothing that preceded it.
	if _, err := orchestrator.DrainWakes(spawnHome); err != nil {
		t.Fatalf("drain wakes before supervision cycle: %v", err)
	}

	// The supervision cycle replays the pending uplink; with no ack yet it
	// must stay pending. A cycle that retired it without an ack would lose
	// the report.
	supervisionEmitted := workflowSupervisionCycle(t, spawnHome, uplink, activation)
	// "a wake was emitted" is the wrong claim: any wake source at all -- a
	// planted check, an unrelated task -- satisfies it while every lifecycle
	// wake source is dead. The claim is that the fleet scan read THIS task's
	// done: status line and woke the supervisor for it, so assert the wake's
	// kind and its key.
	wakes, err := orchestrator.DrainWakes(spawnHome)
	if err != nil {
		t.Fatalf("drain supervision wakes on %s: %v", spawnHome, err)
	}
	signalled := false
	for _, wake := range wakes {
		if wake.Kind == "signal" && wake.Key == taskID {
			signalled = true
			break
		}
	}
	if !signalled {
		t.Fatalf("supervision cycle under %s emitted no signal wake keyed to %s (emitted=%t, wakes=%v)",
			tc.policy, taskID, supervisionEmitted, wakes)
	}
	pendingAfterReplay := len(workflowPendingUplinks(t, spawnHome))
	if pendingAfterReplay != tc.relayed {
		t.Fatalf("pending uplinks after replay = %d, want %d (an unacked report must not retire)", pendingAfterReplay, tc.relayed)
	}

	if tc.relayed > 0 {
		env := pending[0]
		if env.ReceiverRank != orchestrator.RankGeneral {
			t.Fatalf("relayed uplink receiver rank = %q, want %q", env.ReceiverRank, orchestrator.RankGeneral)
		}
		// General acknowledges the exact ref, then the next cycle retires it.
		recv, err := orchestrator.NewReceiver(generalHome)
		if err != nil {
			t.Fatalf("NewReceiver(general): %v", err)
		}
		ref := orchestrator.NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity}
		if got, err := recv.Receive(ref); err != nil || got == nil {
			t.Fatalf("General receive of relayed report: %v", err)
		}
		ack, err := recv.Ack(ref)
		if err != nil {
			t.Fatalf("General ack of relayed report: %v", err)
		}
		if ack.Outcome != orchestrator.OutcomeAccepted {
			t.Fatalf("ack outcome = %q, want accepted", ack.Outcome)
		}
		workflowSupervisionCycle(t, spawnHome, uplink, activation)
		if got := len(workflowPendingUplinks(t, spawnHome)); got != 0 {
			t.Fatalf("pending uplinks after ack = %d, want 0 (the acked relay must retire)", got)
		}
	}
	pendingFinal := len(workflowPendingUplinks(t, spawnHome))

	// The Captain is nudged for the soldier receipt that landed in its home,
	// and the nudge is idempotent across the cycles above. A General-direct
	// dispatch has no Captain pane to nudge.
	if activation.attempts != tc.activations {
		t.Fatalf("captain activation nudges = %d, want %d under %s", activation.attempts, tc.activations, tc.policy)
	}

	// --- teardown ---------------------------------------------------------
	teardown := &workflowTeardown{}
	if _, err := fleet.RetireTask(fleet.Options{HomeDir: spawnHome, ID: taskID},
		teardown, orchestratorRetirementJournals{}, auth); err != nil {
		t.Fatalf("teardown under %s: %v", tc.policy, err)
	}
	if teardown.returned != 1 {
		t.Fatalf("worktree returned %d times, want 1", teardown.returned)
	}
	// An authoritatively absent endpoint is already gone; disposing it again
	// would be a blind teardown of whatever now holds the handle.
	if teardown.disposed != 0 {
		t.Fatalf("endpoint disposed %d times, want 0 for an absent endpoint", teardown.disposed)
	}
	retired, err := auth.Get(tid)
	if err != nil {
		t.Fatalf("read task after teardown: %v", err)
	}
	if retired.Phase != taskauthority.PhaseRetired {
		t.Fatalf("phase after teardown = %s, want retired", retired.Phase)
	}
	t.Logf("workflow evidence: policy=%s model=%s digest=%s parent=%s endpoint(create=%d submit=%d ready-captures=%d) receipt-acked=%t relay-open=%t pending(before=%d after-replay=%d final=%d) supervision-emitted=%t activations=%d teardown(phase=%s returned=%d disposed=%d)",
		tc.policy, meta["model"], meta["config_snapshot_digest"], tc.parentCaptainID,
		endpoints.creates, endpoints.submits, endpoints.captures, receiptAcked, relayOpen,
		pendingBeforeReplay, pendingAfterReplay, pendingFinal, supervisionEmitted,
		activation.attempts, retired.Phase, teardown.returned, teardown.disposed)
}

// workflowSeedCaptainHome builds the Captain-owned side of the captain-
// mediated row: a provenance-marked home whose ONLY config surface is the
// published snapshot General assigned it, registered in General's registry
// with a parent-home return channel.
func workflowSeedCaptainHome(t *testing.T, generalHome, repo, captainID string) string {
	t.Helper()
	captainHome := filepath.Join(generalHome, "captains", captainID)
	if _, err := home.Init(captainHome); err != nil {
		t.Fatalf("init captain home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(captainHome, "AGENTS.md"), []byte("# captain\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := fleet.SeedProvenance(captainHome, captainID); err != nil {
		t.Fatalf("seed captain provenance: %v", err)
	}
	if err := config.StorePublishedSnapshot(captainHome, config.ResolvedProjectConfig{
		Project:        "alpha",
		ProjectPath:    repo,
		Backend:        "tmux",
		SoldierHarness: harness.Pi,
		Model:          "workflow-captain-model",
		DefaultMode:    "local-only",
		CaptainProfile: config.CaptainProfile{Harness: harness.Pi},
		Digest:         workflowPublishedDigest,
	}); err != nil {
		t.Fatalf("publish captain snapshot: %v", err)
	}
	if err := fleet.Register(generalHome, captainID, captainHome, "alpha", "alpha"); err != nil {
		t.Fatalf("register captain: %v", err)
	}
	if err := config.Set(captainHome, "parent-home", generalHome); err != nil {
		t.Fatalf("set parent-home: %v", err)
	}
	// The Captain's own endpoint, as General's registry records it. Without it
	// the activation hook has no target and silently skips.
	canonical, err := home.CanonicalCaptainHome(captainHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(generalHome, "captain:"+captainID, map[string]string{
		"kind": "captain", "sm_id": captainID, "home": canonical,
		"backend": "herdr", "window": "munsu:@captain", "herdr_session": "munsu",
		"herdr_pane_id": "captain-pane",
	}); err != nil {
		t.Fatalf("write captain meta: %v", err)
	}
	return captainHome
}

// workflowSessionStart drives the real session-start path against the home and
// fails closed on anything short of a full, lock-acquiring start. The watcher
// ensure is injected so the scenario never leaves a supervisor process behind;
// everything up to and including the ensure handoff is production code.
const (
	workflowSessionHomeEnv   = "MUNSU_WORKFLOW_SESSION_HOME"
	workflowSessionRoleEnv   = "MUNSU_WORKFLOW_SESSION_ROLE"
	workflowSessionEnsureEnv = "MUNSU_WORKFLOW_SESSION_ENSURE"
	// workflowSessionHelper is the single place the helper is named: the
	// -test.run selector below is built from it.
	workflowSessionHelper = "TestWorkflowSessionStartHelper"
	// workflowSessionDone is the last thing the helper emits. A -test.run
	// selector that matches nothing runs no test and still exits 0, so the
	// subprocess exit status is no evidence at all; this marker is.
	workflowSessionDone = "workflow-session-start-complete"
)

func TestWorkflowSessionStartHelper(t *testing.T) {
	homeDir := os.Getenv(workflowSessionHomeEnv)
	if homeDir == "" {
		return
	}
	role := os.Getenv(workflowSessionRoleEnv)
	wantEnsured, err := strconv.Atoi(os.Getenv(workflowSessionEnsureEnv))
	if err != nil {
		t.Fatalf("parse expected ensure count: %v", err)
	}
	t.Setenv("MUNSU_ROLE", role)
	ensured := 0
	res, err := bootstrap.RunSessionStartWithWatcher(io.Discard, homeDir,
		func(h string) bootstrap.WatchEnsureResult {
			if h != homeDir {
				t.Fatalf("watcher ensure targeted %q, want %q", h, homeDir)
			}
			ensured++
			return bootstrap.WatchEnsureResult{State: "idle"}
		}, nil, taskDataDirReclaimer(homeDir))
	if err != nil {
		t.Fatalf("session-start on %s: %v", homeDir, err)
	}
	if !res.LockAcquired {
		t.Fatalf("session-start on a fresh home did not acquire the session lock")
	}
	if res.RuntimeIdentity == nil || res.Bootstrap == nil {
		t.Fatalf("session-start returned no runtime identity or bootstrap result: %+v", res)
	}
	if ensured != wantEnsured {
		t.Fatalf("watcher ensure ran %d times, want %d (state=%q)", ensured, wantEnsured, res.Watcher.State)
	}
	t.Logf("%s home=%s", workflowSessionDone, homeDir)
}

func workflowSessionStart(t *testing.T, homeDir, role string, wantEnsured int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^"+workflowSessionHelper+"$", "-test.v")
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if len(entry) >= len("NO_MISTAKES_GATE=") && entry[:len("NO_MISTAKES_GATE=")] == "NO_MISTAKES_GATE=" {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env,
		workflowSessionHomeEnv+"="+homeDir,
		workflowSessionRoleEnv+"="+role,
		workflowSessionEnsureEnv+"="+strconv.Itoa(wantEnsured),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("session-start subprocess on %s: %v\n%s", homeDir, err, output)
	}
	// Rename the helper and the selector matches nothing, the subprocess exits
	// 0 having run no session-start at all, and this leg silently becomes a
	// no-op. Only the helper's own end-of-body marker, carrying the home it
	// actually ran against, proves otherwise.
	if want := workflowSessionDone + " home=" + homeDir; !bytes.Contains(output, []byte(want)) {
		t.Fatalf("session-start subprocess did not run %s to completion on %s (no %q in output):\n%s",
			workflowSessionHelper, homeDir, want, output)
	}
}

// workflowRunCLI executes one real munsu command with the exact process
// environment the runtime would have.
func workflowRunCLI(t *testing.T, homeDir string, env map[string]string, args ...string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	root := NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("munsu %v in %s: %v", args, homeDir, err)
	}
}

// workflowPendingUplinks lists the durable uplink reports the home has written
// and not yet had accepted.
func workflowPendingUplinks(t *testing.T, homeDir string) []*orchestrator.Envelope {
	t.Helper()
	pending, err := orchestrator.NewStore(homeDir).ListAllPending()
	if err != nil {
		t.Fatalf("list pending uplinks in %s: %v", homeDir, err)
	}
	return pending
}

// workflowSupervisionCycle runs one real supervision cycle with the production
// captain watcher hooks, so the captain->General uplink replay and the
// activation nudge are driven by the same code the watcher runs.
func workflowSupervisionCycle(t *testing.T, homeDir string, uplink workflowUplink, activation *workflowActivation) bool {
	t.Helper()
	hooks := orchestrator.NewCaptainWatcherHooks(uplink, activation)
	emitted, err := orchestrator.RunCycleWithProbeAndSender(homeDir,
		runtimeTaskEndpointProbe(), newSessionMailboxSender(), hooks,
		fleetRetirementPort{compose: func(h string) (*taskauthority.Canonical, error) {
			return taskauthority.NewCanonical(mustOpenHome(t, h))
		}}, fleetCheckValidationPort{}, runtimeTaskStatePort{})
	if err != nil {
		t.Fatalf("supervision cycle on %s: %v", homeDir, err)
	}
	return emitted
}
