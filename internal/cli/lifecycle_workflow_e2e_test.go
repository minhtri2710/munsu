//go:build e2e

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	created        map[string]fleet.CreatedEndpoint
	creates        int
	submits        int
	captures       int
	pendingCommand string
	shell          string
}

func newWorkflowEndpoints(t *testing.T) *workflowEndpoints {
	return &workflowEndpoints{created: map[string]fleet.CreatedEndpoint{}, shell: testutil.BashShell(t)}
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

func (e *workflowEndpoints) Submit(_ fleet.CreatedEndpoint, command string) error {
	e.submits++
	e.pendingCommand = command
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
	if e.pendingCommand != "" {
		cmd := exec.Command(e.shell, "-c", e.pendingCommand)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("executing submitted launch command: %w: %s", err, out)
		}
		e.pendingCommand = ""
	}
	return "> ready", nil
}

func (e *workflowEndpoints) Dispose(fleet.CreatedEndpoint) error { return nil }

// workflowTeardown is the teardown backend seam. The soldier's endpoint is
// gone by the time its terminal report has landed, so the probe reports a
// narrow exact structured absence — the reading production needs to skip
// disposal — and the worktree return is counted so the scenario can prove the
// cleanup actually ran rather than being reported as pending.
type workflowTeardown struct {
	disposed int
	returned int
}

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

func (w *workflowTeardown) ReturnWorktree(string, string, string) error { w.returned++; return nil }

func (w *workflowTeardown) QueryMergeStatus(taskauthority.DeliveryStep, *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
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
// real reason (binary present, auth configured) rather than being skipped. The
// stub answers `claude auth status` with exit 0, which is how claude reports
// configured auth; the other harnesses read their credential env var.
func workflowHarnessOnPath(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFakeExecutable(t, filepath.Join(dir, name), "#!/bin/sh\nexit 0\n")
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	testutil.SetPath(t, append([]string{dir, filepath.Dir(gitBin)}, testutil.BashShellDirs(t)...)...)
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
		HomeID:     auth.HomeID(),
		TaskID:     tid,
		Owner:      "owner",
		Kind:       "scout",
		Project:    pid,
		ScoutScope: "walk the full lifecycle for this dispatch policy",
		Reason:     "create",
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
	// generalReports is how many report wakes the Captain's report leaves in
	// General's home. General-direct has no captain in the loop, so any report
	// wake there is a topology violation.
	generalReports int
}

func TestFleetLifecycleWorkflowAcrossTopologyPolicies(t *testing.T) {
	cases := []workflowCase{
		{
			name:            "general-direct",
			policy:          fleet.DispatchPolicyGeneralDirect,
			role:            "general",
			model:           "workflow-general-model",
			parentCaptainID: "general",
			generalReports:  0,
		},
		{
			name:            "captain-mediated",
			policy:          fleet.DispatchPolicyCaptainMediated,
			role:            "captain",
			model:           "workflow-captain-model",
			digest:          workflowPublishedDigest,
			parentCaptainID: "cap-1",
			generalReports:  1,
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
		Config: config.FleetBaseConfig{
			SoldierHarness: harness.Pi,
			Model:          "workflow-general-model",
			Backend:        "tmux",
		},
		CaptainProfile: config.CaptainProfile{Harness: harness.Pi},
	}); err != nil {
		t.Fatalf("store fleet base: %v", err)
	}
	if err := fleet.Add(generalHome, "alpha", repo, false); err != nil {
		t.Fatalf("register project: %v", err)
	}
	if err := config.StoreProjectOverlay(generalHome, "alpha", config.ProjectOverlay{FenceUnsupported: "allow"}); err != nil {
		t.Fatalf("store project overlay: %v", err)
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
	// Spawn lints the brief against the scaffold's own sections, so the brief is
	// the scaffolded scout brief of the task's own contract.
	if err := fleet.Scaffold(fleet.ScaffoldOptions{HomeDir: spawnHome, ID: taskID, Repo: "alpha", Mode: "local-only", Scout: true, ScoutScope: "walk the full lifecycle for this dispatch policy", Generation: 1}); err != nil {
		t.Fatalf("scaffold brief: %v", err)
	}

	endpoints := newWorkflowEndpoints(t)
	t.Setenv("MUNSU_ROLE", tc.role)
	t.Chdir(spawnHome)
	if _, err := fleet.Spawn(fleet.Args{
		ID:          taskID,
		ProjectName: "alpha",
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
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	spawned, err := auth.Get(tid)
	if err != nil {
		t.Fatalf("read task after spawn: %v", err)
	}
	if spawned.Launch == nil {
		t.Fatal("spawned task has no launch intent")
	}
	digest := spawned.Launch.SnapshotDigest
	// The model and the snapshot digest are named only by the config surface
	// this policy may read (published snapshot for captain-mediated, fleet
	// base for general-direct). Reading the other surface resolves the other
	// identity, so these two assertions pin the surface, not just the spawn.
	if meta["model"] != tc.model {
		t.Fatalf("meta model = %q, want %q (wrong config surface for %s)", meta["model"], tc.model, tc.policy)
	}
	switch {
	case tc.digest != "":
		if digest != tc.digest {
			t.Fatalf("launch snapshot digest = %q, want the assigned published snapshot %q", digest, tc.digest)
		}
	default:
		if digest == "" || digest == workflowPublishedDigest {
			t.Fatalf("launch snapshot digest = %q, want a fleet-base-resolved digest", digest)
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

	// --- report (soldier terminal report) ---------------------------------
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
		"MUNSU_ROLE": "soldier", "MUNSU_TASK_KIND": "scout", "MUNSU_HOME": spawnHome,
		"MUNSU_TASK_ID": taskID, "MUNSU_PARENT_STATUS": spawnHome,
	}, "report", "done", "findings recorded", "--key", taskID)

	// --- supervision derives the report; the parent settles it ------------
	// A done report is audit-only for the status scan: the supervisor gets the
	// report wake, never a signal wake for it.
	supervisionEmitted := workflowSupervisionCycle(t, spawnHome)
	reports := workflowWakesOfKind(t, spawnHome, home.ReportWakeKind, taskID)
	if len(reports) != 1 || reports[0].Payload != taskID+": done: findings recorded [key="+taskID+"]" {
		t.Fatalf("report wakes for %s under %s = %+v, want the one done report", taskID, tc.policy, reports)
	}
	if signals := workflowWakesOfKind(t, spawnHome, "signal", taskID); len(signals) != 0 {
		t.Fatalf("supervision cycle under %s woke the supervisor for the done task %s: %v", tc.policy, taskID, signals)
	}
	workflowSettleReports(t, spawnHome)
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseDone {
		t.Fatalf("phase after the settled report = %s, want done", agg.Phase)
	}

	if tc.policy == fleet.DispatchPolicyCaptainMediated {
		// The Captain reports the soldier's outcome up to General: one line in
		// its status file in General's home, which General's next claim turns
		// into a report wake. Nothing is pushed to any pane.
		workflowRunCLI(t, spawnHome, map[string]string{
			"MUNSU_ROLE": "captain", "MUNSU_HOME": spawnHome,
			"MUNSU_TASK_ID": "captain:" + tc.parentCaptainID, "MUNSU_PARENT_STATUS": generalHome,
		}, "report", "done", "soldier "+taskID+" reported done", "--key", taskID)
	}
	if err := home.DeriveReportWakes(generalHome); err != nil {
		t.Fatal(err)
	}
	generalReports := workflowWakesOfKind(t, generalHome, home.ReportWakeKind, "")
	if len(generalReports) != tc.generalReports {
		t.Fatalf("report wakes in General's home = %+v, want %d under %s", generalReports, tc.generalReports, tc.policy)
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
	t.Logf("workflow evidence: policy=%s model=%s digest=%s parent=%s endpoint(create=%d submit=%d ready-captures=%d) general-reports=%d supervision-emitted=%t teardown(phase=%s returned=%d disposed=%d)",
		tc.policy, meta["model"], digest, tc.parentCaptainID,
		endpoints.creates, endpoints.submits, endpoints.captures, len(generalReports), supervisionEmitted,
		retired.Phase, teardown.returned, teardown.disposed)
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
	if err := home.SeedCaptainProvenance(captainHome, captainID); err != nil {
		t.Fatalf("seed captain provenance: %v", err)
	}
	if err := config.StorePublishedSnapshot(captainHome, config.ResolvedProjectConfig{
		Project:          "alpha",
		ProjectPath:      repo,
		Backend:          "tmux",
		SoldierHarness:   harness.Pi,
		Model:            "workflow-captain-model",
		CaptainProfile:   config.CaptainProfile{Harness: harness.Pi},
		ForgeStep:        config.ResolvedStep{Baseline: true, ProbeState: "baseline", Reason: "no tool configured"},
		WorktreeStep:     config.ResolvedStep{Baseline: true, ProbeState: "baseline", Reason: "no tool configured"},
		FenceUnsupported: "allow",
		Digest:           workflowPublishedDigest,
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
		"backend": "herdr", "window": "munsu:@captain", "session_owner": "munsu",
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
	cmd.Env = append(os.Environ(),
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

// workflowWakesOfKind lists the queued wakes of one kind in homeDir, for one
// key when key is not empty.
func workflowWakesOfKind(t *testing.T, homeDir, kind, key string) []home.WakeRecord {
	t.Helper()
	queued, err := home.PeekWakes(homeDir)
	if err != nil {
		t.Fatalf("peek wakes in %s: %v", homeDir, err)
	}
	var out []home.WakeRecord
	for _, w := range queued {
		if w.Kind == kind && (key == "" || w.Key == key) {
			out = append(out, w)
		}
	}
	return out
}

// workflowSettleReports claims and acknowledges every wake in homeDir, as the
// parent does, and fails on any report whose settlement could not finish.
func workflowSettleReports(t *testing.T, homeDir string) {
	t.Helper()
	claim, err := orchestrator.ClaimWakes(homeDir, "general", 60, 100)
	if err != nil {
		t.Fatalf("claim wakes in %s: %v", homeDir, err)
	}
	var ids []string
	for _, w := range claim.Wakes {
		ids = append(ids, w.Epoch+":"+w.Seq)
	}
	unaccepted, err := orchestrator.AckWakes(homeDir, claim.LeaseID, ids)
	if err != nil || len(unaccepted) != 0 {
		t.Fatalf("ack wakes in %s: unaccepted=%+v err=%v", homeDir, unaccepted, err)
	}
}

// workflowSupervisionCycle runs one real supervision cycle with the production
// watcher hooks.
func workflowSupervisionCycle(t *testing.T, homeDir string) bool {
	t.Helper()
	emitted, err := orchestrator.RunCycleWithProbeAndSender(homeDir,
		runtimeTaskEndpointProbe(), newSessionMailboxSender(), watcherHooks(),
		fleetReviewVerdictPort{})
	if err != nil {
		t.Fatalf("supervision cycle on %s: %v", homeDir, err)
	}
	return emitted
}

// mustOpenHome opens an already-initialized home for e2e fixtures.
func mustOpenHome(t *testing.T, homeDir string) *home.Home {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
