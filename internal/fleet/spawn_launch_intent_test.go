package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// reentrantEndpointCapabilities is a reservation-aware endpoint capability
// that find-or-creates under the exact endpoint reservation identity: repeated
// CreateReserved calls with the same reservation return the SAME endpoint, so
// recovery after a crash between create and durable attach never creates a
// replacement. It counts underlying creates and submissions for the
// no-duplicate assertions.
type reentrantEndpointCapabilities struct {
	mu         sync.Mutex
	created    map[string]CreatedEndpoint // reservationID -> endpoint
	creates    int
	submits    int
	probeAlive bool
	submitErr  error        // when set, Submit fails (simulates failure after evidence record)
	ready      string       // the pane capture; the default is ready for pi
	onSubmit   func()       // runs after each delivered Submit
	onProbe    func() error // runs before each Probe; an error is the probe's failure
}

func newReentrantEndpoints() *reentrantEndpointCapabilities {
	return &reentrantEndpointCapabilities{created: map[string]CreatedEndpoint{}, probeAlive: true, ready: "> ready"}
}

func (f *reentrantEndpointCapabilities) CreateReserved(req CreateRequest) (CreatedEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ep, ok := f.created[req.ReservationID]; ok {
		return ep, nil
	}
	f.creates++
	handle := fmt.Sprintf("pane-%d", f.creates)
	ep := CreatedEndpoint{
		Backend:      "tmux",
		Handle:       handle,
		SessionOwner: "sess-" + handle,
		WorkspaceID:  "ws-" + handle,
		TabID:        "tab-" + handle,
	}
	f.created[req.ReservationID] = ep
	return ep, nil
}

func (f *reentrantEndpointCapabilities) Submit(ep CreatedEndpoint, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submits++
	if f.onSubmit != nil {
		f.onSubmit()
	}
	return f.submitErr
}

func (f *reentrantEndpointCapabilities) Probe(ep CreatedEndpoint) (SpawnEndpointObservation, error) {
	if f.onProbe != nil {
		if err := f.onProbe(); err != nil {
			return SpawnEndpointObservation{}, err
		}
	}
	if f.probeAlive {
		return endpointStatusFromState(EndpointAlive), nil
	}
	return endpointStatusFromState(EndpointDead), nil
}

func (f *reentrantEndpointCapabilities) Capture(ep CreatedEndpoint, n int) (string, error) {
	return f.ready, nil
}

func (f *reentrantEndpointCapabilities) Dispose(ep CreatedEndpoint) error { return nil }

// createCount returns the number of distinct endpoints actually created.
func (f *reentrantEndpointCapabilities) createCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates
}

// submitCount returns the number of launch submissions delivered.
func (f *reentrantEndpointCapabilities) submitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.submits
}

// launchFixture is a fully-configured spawn launch context for phase-level
// tests. It drives the REAL production phases (beginLaunchIntent,
// acquireWorktree, bindWorktree, buildSoldierPrompt, createSession,
// attachEndpoint, submitLaunch, writeLaunchManifest, waitAndInjectBrief,
// verifyEndpointReadyBeforePersist, writeTaskMeta, confirmSpawn) against a
// canonical home with a real git-fallback worktree and a counting
// reservation-aware endpoint capability.
type launchFixture struct {
	t         *testing.T
	auth      *taskauthority.Canonical
	homeDir   string
	repoPath  string
	taskID    string
	runner    *Runner
	endpoints *reentrantEndpointCapabilities
}

// requiredSkillStubDir returns a directory holding an executable stub named
// after shipRequiredSkill. Launch fixtures put it on PATH because a real ship
// host has that CLI installed; without it FailClosedDuringLaunch refuses the
// launch before any session is allocated.
func requiredSkillStubDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFakeExecutable(t, filepath.Join(dir, shipRequiredSkill), "#!/bin/sh\nexit 0\n")
	return dir
}

// newLaunchFixture builds the fixture. PATH is sanitized to git only so the
// worktree acquisition uses the deterministic git fallback (never the
// external treehouse pool).
func newLaunchFixture(t *testing.T, taskID string) *launchFixture {
	t.Helper()
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	auth := canonicalAtHome(t, homeDir)
	canonicalCreateTask(t, auth, taskID, "ship", "test-proj")

	repoPath := initRepoForSpawnBinding(t, t.TempDir())
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	testutil.SetPath(t, filepath.Dir(gitBin), requiredSkillStubDir(t))

	snap, err := config.NewResolvedSnapshot(
		config.FleetBaseDocument{
			SchemaVersion: config.FleetBaseSchemaVersion,
			Config:        config.ProjectOverlay{Backend: "tmux", SoldierHarness: "pi", Model: "gpt-5"},
		},
		config.ProjectFacts{Name: "test-proj", Path: repoPath},
	)
	if err != nil {
		t.Fatalf("NewResolvedSnapshot: %v", err)
	}
	resolved := snap.Config()

	endpoints := newReentrantEndpoints()
	r := &Runner{
		homeDir: homeDir,
		args: Args{
			ID:          taskID,
			ProjectName: "test-proj",
			Authority:   auth,
		},
		kind:                taskauthority.KindShip,
		harness:             "pi",
		model:               "gpt-5",
		effort:              "high",
		effectiveMode:       "direct-PR",
		requestedMode:       "direct-PR",
		contractMode:        "direct-PR",
		spawnRole:           "general",
		dispatchPolicy:      DispatchPolicyGeneralDirect,
		parentCaptainID:     "general", // the General-direct parent sentinel the boundary policy resolves
		projectConfigLoaded: true,
		projectConfig: SpawnProjectConfig{
			Frozen:         snap,
			SnapshotDigest: resolved.Digest,
			ProjectName:    resolved.Project,
			ProjectPath:    resolved.ProjectPath,
			Soldier:        SpawnSoldierConfig{Harness: "pi", Model: "gpt-5", Effort: "high", Mode: "direct-PR"},
		},
		projPath:  repoPath,
		endpoints: endpoints,
	}
	// The registered brief the launch prompt is built from.
	briefDir := filepath.Join(homeDir, "data", taskID)
	if err := os.MkdirAll(briefDir, 0755); err != nil {
		t.Fatalf("brief dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(briefDir, "brief.md"), []byte("# test brief for "+taskID), 0644); err != nil {
		t.Fatalf("brief file: %v", err)
	}

	return &launchFixture{t: t, auth: auth, homeDir: homeDir, repoPath: repoPath, taskID: taskID, runner: r, endpoints: endpoints}
}

// errCrashSimulated is the sentinel the phase runner returns when it stops at
// the requested crash boundary (simulating a crash/failure at that point).
var errCrashSimulated = errors.New("simulated crash boundary")

// runLaunchPhases drives the launch-critical phases in production order.
// When crashAfter names a phase, the run stops after that phase completes
// with errCrashSimulated (the durable state of the completed phase remains,
// exactly like a crash). A nil crashAfter runs every phase.
func runLaunchPhases(f *launchFixture, crashAfter string) error {
	r := f.runner
	// The bound worktree is produced by bind-worktree and consumed by prompt:
	// the phases carry the same BoundWorktree value production does, so a
	// reordering of this list fails the same way production would.
	var bound BoundWorktree
	phases := []struct {
		name string
		fn   func() error
	}{
		{"begin", r.beginLaunchIntent},
		{"contract", r.recordDeliveryContract},
		{"acquire", r.acquireWorktree},
		{"bind-worktree", func() error {
			var err error
			bound, err = r.bindWorktree()
			return err
		}},
		{"prompt", func() error { return r.buildSoldierPrompt(bound) }},
		{"probe-fence", func() error { return r.probeFence(bound) }},
		{"manifest", r.prepareAndPersistLaunchManifest},
		{"create-session", r.createSession},
		{"attach-endpoint", r.attachEndpoint},
		{"submit", r.submitLaunch},
		{"ready", r.waitAndInjectBrief},
		{"verify", r.verifyEndpointReadyBeforePersist},
		{"meta", r.writeTaskMeta},
		{"confirm", func() error {
			_, err := r.confirmSpawn()
			return err
		}},
	}
	for _, p := range phases {
		if err := p.fn(); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		if p.name == crashAfter {
			return errCrashSimulated
		}
	}
	return nil
}

func prepareLaunchWithRecordedManifestAnchor(t *testing.T, f *launchFixture) (taskauthority.Aggregate, preparedLaunchFiles) {
	t.Helper()
	r := f.runner
	if err := r.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := r.recordDeliveryContract(); err != nil {
		t.Fatalf("recordDeliveryContract: %v", err)
	}
	if err := r.acquireWorktree(); err != nil {
		t.Fatalf("acquireWorktree: %v", err)
	}
	bound, err := r.bindWorktree()
	if err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}
	if err := r.buildSoldierPrompt(bound); err != nil {
		t.Fatalf("buildSoldierPrompt: %v", err)
	}
	if err := r.probeFence(bound); err != nil {
		t.Fatalf("probeFence: %v", err)
	}
	agg := f.aggregate()
	snapshotDigest := ""
	if r.projectConfigLoaded {
		snapshotDigest = r.projectConfig.SnapshotDigest
	}
	_, scriptBytes, err := prepareLaunchScript(LaunchArtifactInput{
		WorktreePath: r.cwd, LaunchDir: r.launchDir, HomeDir: r.homeDir, TaskID: r.args.ID,
		SnapshotDigest: snapshotDigest, LaunchBin: r.launchBin, LaunchArgs: r.launchArgs,
		LaunchID: r.launchID, Generation: agg.Generation.String(), EndpointFence: r.epFenceToken(), Fence: r.fence,
	})
	if err != nil {
		t.Fatalf("prepareLaunchScript: %v", err)
	}
	prepared, err := prepareLaunchFiles(DefaultCharter(r.args.ID, r.kind, r.effectiveMode), r.briefData, r.promptEnv, r.prompt, scriptBytes, r.harness)
	if err != nil {
		t.Fatalf("prepareLaunchFiles: %v", err)
	}
	taskID := mustTaskID(t, f.taskID)
	req := taskauthority.CanonicalRecordLaunchManifestRequest{
		HomeID: r.args.Authority.HomeID(), TaskID: taskID,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		LaunchID:     r.launchID, WorktreeLeaseID: agg.Worktree.LeaseID,
		WorktreeFenceToken: agg.Worktree.FenceToken, ManifestSHA256: prepared.digest,
		Reason: "test anchor before publication",
	}
	op, err := r.spawnOperation("manifest", agg.Generation, req)
	if err != nil {
		t.Fatalf("spawnOperation: %v", err)
	}
	if _, err := r.args.Authority.RecordLaunchManifest(op, req); err != nil {
		t.Fatalf("RecordLaunchManifest: %v", err)
	}
	return f.aggregate(), prepared
}

func TestPrepareAndPersistLaunchManifestRefusesUnboundState(t *testing.T) {
	t.Run("authority missing", func(t *testing.T) {
		f := newLaunchFixture(t, "manifest-no-authority")
		f.runner.args.Authority = nil
		if err := f.runner.prepareAndPersistLaunchManifest(); err == nil || !strings.Contains(err.Error(), "canonical launch intent is required") {
			t.Fatalf("prepareAndPersistLaunchManifest error = %v, want missing authority refusal", err)
		}
	})
	t.Run("launch intent missing", func(t *testing.T) {
		f := newLaunchFixture(t, "manifest-no-launch")
		f.runner.launch = nil
		if err := f.runner.prepareAndPersistLaunchManifest(); err == nil || !strings.Contains(err.Error(), "canonical launch intent is required") {
			t.Fatalf("prepareAndPersistLaunchManifest error = %v, want missing launch refusal", err)
		}
	})
	t.Run("canonical worktree missing", func(t *testing.T) {
		f := newLaunchFixture(t, "manifest-no-worktree")
		if err := prepareLaunchUpToManifest(t, f); err != nil {
			t.Fatalf("prepare launch: %v", err)
		}
		tamperTaskAggregate(t, f.homeDir, f.taskID, func(agg *taskauthority.Aggregate) { agg.Worktree = nil })
		if err := f.runner.prepareAndPersistLaunchManifest(); err == nil || !strings.Contains(err.Error(), "canonical worktree binding or launch intent") {
			t.Fatalf("prepareAndPersistLaunchManifest error = %v, want missing binding refusal", err)
		}
	})
	t.Run("launch identity mismatch", func(t *testing.T) {
		f := newLaunchFixture(t, "manifest-launch-id-mismatch")
		if err := prepareLaunchUpToManifest(t, f); err != nil {
			t.Fatalf("prepare launch: %v", err)
		}
		f.runner.launchID = "another-launch"
		if err := f.runner.prepareAndPersistLaunchManifest(); err == nil || !strings.Contains(err.Error(), "canonical worktree binding or launch intent") {
			t.Fatalf("prepareAndPersistLaunchManifest error = %v, want launch identity refusal", err)
		}
	})
	t.Run("worktree fence mismatch", func(t *testing.T) {
		f := newLaunchFixture(t, "manifest-worktree-fence-mismatch")
		if _, _ = prepareLaunchWithRecordedManifestAnchor(t, f); f.runner.launch == nil {
			t.Fatal("fixture did not prepare launch intent")
		}
		f.runner.launch.WorktreeFenceToken = "foreign-fence"
		if err := f.runner.prepareAndPersistLaunchManifest(); err == nil || !strings.Contains(err.Error(), "canonical manifest anchor does not match this launch and worktree") {
			t.Fatalf("prepareAndPersistLaunchManifest error = %v, want worktree fence refusal", err)
		}
	})
	t.Run("missing anchor after successful launch", func(t *testing.T) {
		f := newLaunchFixture(t, "manifest-anchor-missing")
		if err := runLaunchPhases(f, ""); err != nil {
			t.Fatalf("complete launch: %v", err)
		}
		tamperTaskAggregate(t, f.homeDir, f.taskID, func(a *taskauthority.Aggregate) { a.Worktree.LaunchManifest = nil })
		if err := f.runner.prepareAndPersistLaunchManifest(); err == nil || !strings.Contains(err.Error(), "successful launch evidence has no matching canonical manifest anchor") {
			t.Fatalf("prepareAndPersistLaunchManifest error = %v, want missing-anchor refusal", err)
		}
	})
	t.Run("manifest digest mismatch", func(t *testing.T) {
		f := newLaunchFixture(t, "manifest-anchor-digest-mismatch")
		if err := prepareLaunchUpToManifest(t, f); err != nil {
			t.Fatalf("prepare launch: %v", err)
		}
		agg, prepared := prepareLaunchWithRecordedManifestAnchor(t, f)
		for _, name := range prepared.names {
			path := filepath.Join(agg.Worktree.Path, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("create artifact directory for %s: %v", name, err)
			}
			if err := home.AtomicCreate(path, prepared.files[name], 0o644); err != nil {
				t.Fatalf("publish %s: %v", name, err)
			}
		}
		if err := home.AtomicCreate(filepath.Join(agg.Worktree.Path, ManifestName), prepared.manifest, 0o644); err != nil {
			t.Fatalf("publish manifest: %v", err)
		}
		if err := VerifyLaunchArtifacts(agg.Worktree.Path, prepared.digest); err != nil {
			t.Fatalf("verify prepared artifacts before anchor tampering: %v", err)
		}
		tamperTaskAggregate(t, f.homeDir, f.taskID, func(a *taskauthority.Aggregate) {
			a.Worktree.LaunchManifest.ManifestSHA256 = sha256Content([]byte("different manifest"))
		})
		if err := f.runner.prepareAndPersistLaunchManifest(); err == nil || !strings.Contains(err.Error(), "conflicts with the prepared bytes") {
			t.Fatalf("prepareAndPersistLaunchManifest error = %v, want canonical digest conflict", err)
		}
	})
}

func prepareLaunchUpToManifest(t *testing.T, f *launchFixture) error {
	t.Helper()
	if err := runLaunchPhases(f, "probe-fence"); !errors.Is(err, errCrashSimulated) {
		return fmt.Errorf("launch phases through probe-fence: %w", err)
	}
	return nil
}

func TestVerifyRecordedLaunchArtifactsRefusesUnmatchedAuthority(t *testing.T) {
	t.Run("authority missing", func(t *testing.T) {
		f := newLaunchFixture(t, "verify-manifest-no-authority")
		f.runner.args.Authority = nil
		if err := f.runner.verifyRecordedLaunchArtifacts(); err == nil || !strings.Contains(err.Error(), "task authority is not composed") {
			t.Fatalf("verifyRecordedLaunchArtifacts error = %v, want missing-authority refusal", err)
		}
	})
	t.Run("manifest evidence missing", func(t *testing.T) {
		f := newLaunchFixture(t, "verify-manifest-no-evidence")
		if err := f.runner.verifyRecordedLaunchArtifacts(); err == nil || !strings.Contains(err.Error(), "successful launch lacks canonical worktree manifest evidence") {
			t.Fatalf("verifyRecordedLaunchArtifacts error = %v, want missing-evidence refusal", err)
		}
	})
	t.Run("canonical launch identity mismatch", func(t *testing.T) {
		f := newLaunchFixture(t, "verify-manifest-identity-mismatch")
		if err := runLaunchPhases(f, ""); err != nil {
			t.Fatalf("complete launch: %v", err)
		}
		tamperTaskAggregate(t, f.homeDir, f.taskID, func(agg *taskauthority.Aggregate) { agg.LaunchEvidence.LaunchID = "foreign-launch" })
		if err := f.runner.verifyRecordedLaunchArtifacts(); err == nil || !strings.Contains(err.Error(), "launch identity mismatch") {
			t.Fatalf("verifyRecordedLaunchArtifacts error = %v, want launch-identity refusal", err)
		}
	})
	t.Run("prepared command digest mismatch", func(t *testing.T) {
		f := newLaunchFixture(t, "verify-manifest-command-mismatch")
		if err := runLaunchPhases(f, ""); err != nil {
			t.Fatalf("complete launch: %v", err)
		}
		f.runner.preparedLaunch = &preparedLaunchArtifact{artifact: LaunchArtifact{CommandDigest: sha256Content([]byte("foreign command"))}}
		if err := f.runner.verifyRecordedLaunchArtifacts(); err == nil || !strings.Contains(err.Error(), "command digest mismatch") {
			t.Fatalf("verifyRecordedLaunchArtifacts error = %v, want command-digest refusal", err)
		}
	})
}

func TestSubmitLaunchRefusesMissingPreparedArtifacts(t *testing.T) {
	f := newLaunchFixture(t, "submit-missing-prepared-artifacts")
	if err := runLaunchPhases(f, "attach-endpoint"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("launch phases = %v, want simulated pre-submit stop", err)
	}
	f.runner.preparedLaunch, f.runner.preparedFiles = nil, nil
	before := f.endpoints.submitCount()
	if err := f.runner.submitLaunch(); err == nil || !strings.Contains(err.Error(), "canonical launch artifacts were not prepared") {
		t.Fatalf("submitLaunch error = %v, want missing-artifact refusal", err)
	}
	if f.endpoints.submitCount() != before {
		t.Fatalf("missing artifact refusal submitted a command: before=%d after=%d", before, f.endpoints.submitCount())
	}
}

func TestLaunchManifestAnchorPrecedesArtifactPublication(t *testing.T) {
	isolateHuman(t)
	f := newLaunchFixture(t, "manifest-anchor-before-publication")
	agg, prepared := prepareLaunchWithRecordedManifestAnchor(t, f)
	if agg.Worktree == nil || agg.Worktree.LaunchManifest == nil || agg.LaunchEvidence != nil || agg.AcquiredEndpoint != nil {
		t.Fatalf("anchor boundary aggregate = %+v, want manifest anchor without endpoint or successful submit", agg)
	}
	if f.endpoints.createCount() != 0 || f.endpoints.submitCount() != 0 {
		t.Fatalf("endpoint activity before artifact publication: creates=%d submits=%d", f.endpoints.createCount(), f.endpoints.submitCount())
	}
	if _, err := os.Lstat(filepath.Join(agg.Worktree.Path, ManifestName)); !os.IsNotExist(err) {
		t.Fatalf("manifest exists before publication: stat error=%v", err)
	}
	firstName := prepared.names[0]
	firstPath := filepath.Join(agg.Worktree.Path, filepath.FromSlash(firstName))
	if err := home.AtomicCreate(firstPath, prepared.files[firstName], 0o644); err != nil {
		t.Fatalf("publish first prepared file before simulated crash: %v", err)
	}
	if err := f.runner.prepareAndPersistLaunchManifest(); err != nil {
		t.Fatalf("resume after anchor-only crash: %v", err)
	}
	if err := VerifyLaunchArtifacts(agg.Worktree.Path, prepared.digest); err != nil {
		t.Fatalf("verify recovered prepared artifacts: %v", err)
	}
	if got := f.aggregate().Worktree.LaunchManifest; got == nil || got.ManifestSHA256 != prepared.digest {
		t.Fatalf("recovery changed canonical manifest anchor: %+v", got)
	}
	if f.endpoints.createCount() != 0 || f.endpoints.submitCount() != 0 {
		t.Fatalf("artifact recovery acquired/submitted an endpoint: creates=%d submits=%d", f.endpoints.createCount(), f.endpoints.submitCount())
	}
}

func TestLaunchManifestAnchorRefusesConflictingPartialArtifact(t *testing.T) {
	isolateHuman(t)
	f := newLaunchFixture(t, "manifest-anchor-conflicting-artifact")
	agg, _ := prepareLaunchWithRecordedManifestAnchor(t, f)
	conflict := []byte("partial crash-left artifact")
	path := filepath.Join(agg.Worktree.Path, BriefName)
	if err := os.WriteFile(path, conflict, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.runner.prepareAndPersistLaunchManifest(); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("prepareAndPersistLaunchManifest error = %v, want conflicting artifact refusal", err)
	}
	if got, err := os.ReadFile(path); err != nil || !reflect.DeepEqual(got, conflict) {
		t.Fatalf("conflicting artifact after refusal = %q, %v; refusal must preserve it", got, err)
	}
	after := f.aggregate()
	if after.Worktree == nil || after.Worktree.LaunchManifest == nil || after.Worktree.LaunchManifest.ManifestSHA256 != agg.Worktree.LaunchManifest.ManifestSHA256 {
		t.Fatalf("canonical anchor changed after conflicting artifact refusal: %+v", after.Worktree)
	}
	if f.endpoints.createCount() != 0 || f.endpoints.submitCount() != 0 {
		t.Fatalf("conflicting artifact allowed endpoint activity: creates=%d submits=%d", f.endpoints.createCount(), f.endpoints.submitCount())
	}
}

// aggregate reads the current canonical aggregate of the fixture task.
func (f *launchFixture) aggregate() taskauthority.Aggregate {
	f.t.Helper()
	agg, err := f.auth.Get(mustTaskID(f.t, f.taskID))
	if err != nil {
		f.t.Fatalf("Get(%s): %v", f.taskID, err)
	}
	return agg
}

// tamperTaskAggregate rewrites the task's current aggregate document through
// the same durable home storage the canonical surface uses (test-only
// adversarial state construction; the mutation must keep the aggregate
// shape-valid).
func tamperTaskAggregate(t *testing.T, homeDir, taskID string, mutate func(*taskauthority.Aggregate)) {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	lk, err := h.Lock("task-" + hexEncode(taskID))
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer lk.Release()
	key := "task-authority/tasks/" + taskID + "/current.json"
	data, err := h.Read(home.RootState, key)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var doc struct {
		HomeRevision uint64                  `json:"home_revision"`
		Aggregate    taskauthority.Aggregate `json:"aggregate"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode task doc: %v", err)
	}
	mutate(&doc.Aggregate)
	newData, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode task doc: %v", err)
	}
	if _, err := h.Commit(lk, "tamper-"+taskID, doc.HomeRevision, []home.ChangeItem{
		{Root: home.RootState, Key: key, Data: newData},
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// hexEncode is the test-local hex helper for the canonical task lock scope.
func hexEncode(s string) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, len(s)*2)
	for i, c := range []byte(s) {
		out[i*2] = hexDigits[c>>4]
		out[i*2+1] = hexDigits[c&0xf]
	}
	return string(out)
}

// TestLaunchIntentReceiptPrecedesAcquisition proves the durable launch intent
// is committed BEFORE any worktree or endpoint provider call: after
// beginLaunchIntent the aggregate carries the intent and NO acquired
// resource, and the first provider call happens only at acquireWorktree.
func TestLaunchIntentReceiptPrecedesAcquisition(t *testing.T) {
	f := newLaunchFixture(t, "intent-before")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if f.endpoints.createCount() != 0 {
		t.Fatalf("endpoint created before acquisition: %d", f.endpoints.createCount())
	}
	agg := f.aggregate()
	if agg.Launch == nil {
		t.Fatal("launch intent missing after beginLaunchIntent")
	}
	if agg.Worktree != nil || agg.Endpoint != nil || agg.AcquiredEndpoint != nil || agg.LaunchEvidence != nil {
		t.Fatalf("pre-acquisition aggregate carries acquired resources: worktree=%+v endpoint=%+v acquired=%+v evidence=%+v", agg.Worktree, agg.Endpoint, agg.AcquiredEndpoint, agg.LaunchEvidence)
	}
	if agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("phase = %q, want queued before acquisition", agg.Phase)
	}

	// The intent carries the exact immutable launch identity.
	l := agg.Launch
	if l.SnapshotDigest != f.runner.projectConfig.SnapshotDigest {
		t.Fatalf("snapshot digest = %q, want %q", l.SnapshotDigest, f.runner.projectConfig.SnapshotDigest)
	}
	if l.Backend != "tmux" || l.Harness != "pi" || l.Model != "gpt-5" || l.Effort != "high" {
		t.Fatalf("backend/harness/model/effort = %s/%s/%s/%s", l.Backend, l.Harness, l.Model, l.Effort)
	}
	if l.Mode != "direct-PR" || l.Kind != "ship" || l.Project != "test-proj" || l.ParentTaskID != "general" {
		t.Fatalf("mode/kind/project/parent = %s/%s/%s/%s", l.Mode, l.Kind, l.Project, l.ParentTaskID)
	}
	wtRes, wtFence, epRes, epFence := spawnReservationIdentities(f.taskID, 1)
	if l.WorktreeReservationID != wtRes || l.WorktreeFenceToken != wtFence || l.EndpointReservationID != epRes || l.EndpointFenceToken != epFence {
		t.Fatalf("reservation fences not the deterministic intent-owned identities: %+v", l)
	}
	if l.LaunchID != fmt.Sprintf("launch-%s-1", f.taskID) {
		t.Fatalf("launch id = %q", l.LaunchID)
	}
	if !strings.Contains(l.WindowLabel, "mu-test-proj-"+f.taskID) || !strings.HasSuffix(l.WindowLabel, "-g1") {
		t.Fatalf("window label = %q, want generation-scoped", l.WindowLabel)
	}
}

// TestLaunchIntentRetryDoesNotMintDifferentIntent proves a retry of the same
// launch re-adopts the committed intent: beginLaunchIntent a second time with
// the identical deterministic derivation neither advances the revision nor
// replaces the committed identity.
func TestLaunchIntentRetryDoesNotMintDifferentIntent(t *testing.T) {
	f := newLaunchFixture(t, "intent-retry")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	first := f.aggregate()
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent retry: %v", err)
	}
	second := f.aggregate()
	if second.Revision != first.Revision {
		t.Fatalf("retry advanced revision %d -> %d", first.Revision, second.Revision)
	}
	if second.Launch.LaunchID != first.Launch.LaunchID || second.Launch.SnapshotDigest != first.Launch.SnapshotDigest {
		t.Fatalf("retry minted a different intent: %+v vs %+v", second.Launch, first.Launch)
	}
}

// TestLaunchIntentDeterministicAcrossRuns proves two independent Runner
// constructions with the same snapshot/config derive the identical
// deterministic intent identity (same Operation ID and digest) so a retry
// never mints a different intent.
func TestLaunchIntentDeterministicAcrossRuns(t *testing.T) {
	f1 := newLaunchFixture(t, "intent-det")
	if err := f1.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	first := f1.aggregate()

	f2 := newLaunchFixture(t, "intent-det")
	if err := f2.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("second beginLaunchIntent: %v", err)
	}
	second := f2.aggregate()

	if first.Launch.LaunchID != second.Launch.LaunchID ||
		first.Launch.WorktreeReservationID != second.Launch.WorktreeReservationID ||
		first.Launch.EndpointFenceToken != second.Launch.EndpointFenceToken ||
		first.Launch.SnapshotDigest != second.Launch.SnapshotDigest {
		t.Fatalf("intent identity differs across runs: %+v vs %+v", first.Launch, second.Launch)
	}
}

// TestLaunchIntentSnapshotDigestAndFencesPreservedInAggregate proves the
// exact snapshot digest and one-time reservation lease/fence identities are
// preserved in the canonical aggregate and match the deterministic
// derivation (required acceptance: exact reservation lease/fence and snapshot
// digest preserved in canonical aggregate).
func TestLaunchIntentSnapshotDigestAndFencesPreservedInAggregate(t *testing.T) {
	f := newLaunchFixture(t, "intent-preserve")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	agg := f.aggregate()
	if agg.Launch == nil {
		t.Fatal("launch intent missing")
	}
	if !domain.IsSHA256(agg.Launch.SnapshotDigest) {
		t.Fatalf("snapshot digest not a sha256: %q", agg.Launch.SnapshotDigest)
	}
	wtRes, wtFence, epRes, epFence := spawnReservationIdentities(f.taskID, 1)
	if agg.Launch.WorktreeReservationID != wtRes || agg.Launch.WorktreeFenceToken != wtFence ||
		agg.Launch.EndpointReservationID != epRes || agg.Launch.EndpointFenceToken != epFence {
		t.Fatalf("aggregate reservation identities diverge from derivation: %+v", agg.Launch)
	}
}

// TestLaunchIntentChangedDigestFailsClosed proves a retry that would commit a
// DIFFERENT intent (changed Operation ID digest / snapshot digest) under the
// same launch fails closed instead of re-launching.
func TestLaunchIntentChangedDigestFailsClosed(t *testing.T) {
	f := newLaunchFixture(t, "intent-changed")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	// The deterministic derivation changes (a different frozen snapshot): the
	// committed intent no longer matches and the runner refuses.
	f.runner.projectConfig.SnapshotDigest = strings.Repeat("b", 64)
	if err := f.runner.beginLaunchIntent(); err == nil {
		t.Fatal("changed snapshot digest must fail closed, got nil")
	} else if !strings.Contains(err.Error(), "different launch intent") {
		t.Fatalf("error = %v, want different-launch-intent refusal", err)
	}
}

// TestLaunchIntentRejectsNonQueuedPhase proves a task that is not queued (and
// not a completed launch under the identical identity) cannot begin a launch.
func TestLaunchIntentRejectsNonQueuedPhase(t *testing.T) {
	f := newLaunchFixture(t, "intent-phase")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	// Block the queued task: the launch intent stays committed but the phase
	// is no longer queued, so a retry fails closed.
	taskID := mustTaskID(t, f.taskID)
	agg := f.aggregate()
	blockReq := taskauthority.CanonicalBlockRequest{
		HomeID: f.auth.HomeID(), TaskID: taskID,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Detail:       "test block", Reason: "test",
	}
	op, err := domain.NewOperation(mustOpID(t, "spawn-block-"+f.taskID), blockReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.Block(op, blockReq); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if err := f.runner.beginLaunchIntent(); err == nil {
		t.Fatal("blocked task must fail closed at launch intent")
	} else if !strings.Contains(err.Error(), "requires queued") {
		t.Fatalf("error = %v, want requires-queued refusal", err)
	}
}

// TestLaunchIntentStaleGenerationFailsClosed proves a committed launch intent
// whose generation is no longer current fails closed: the deterministic
// construction for the current generation cannot match the stale committed
// intent, so recovery never continues a superseded launch.
func TestLaunchIntentStaleGenerationFailsClosed(t *testing.T) {
	f := newLaunchFixture(t, "intent-stale")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	// Advance the current generation document (keeping the aggregate shape
	// valid) so the committed gen-1 intent is stale: the runner now derives a
	// gen-2 intent that cannot match it.
	tamperTaskAggregate(t, f.homeDir, f.taskID, func(agg *taskauthority.Aggregate) {
		agg.Generation = 2
	})
	if err := f.runner.beginLaunchIntent(); err == nil {
		t.Fatal("stale generation must fail closed, got nil")
	} else if !strings.Contains(err.Error(), "different launch intent") {
		t.Fatalf("error = %v, want different-launch-intent refusal", err)
	}
}

// launchFixture helpers for JSON round-trips.

// TestLaunchRecordsTheSeatOfTheLaunch proves the durable launch evidence
// carries the seat: the harness argv without the prompt, the prompt's digest,
// and the fence outcome the probe phase produced.
func TestLaunchRecordsTheSeatOfTheLaunch(t *testing.T) {
	isolateHuman(t)
	f := newLaunchFixture(t, "seat-record")
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatal(err)
	}
	agg := f.aggregate()
	if agg.LaunchEvidence == nil {
		t.Fatal("no launch evidence")
	}
	seat := agg.LaunchEvidence.Seat
	r := f.runner
	wantArgv := append([]string{r.launchBin}, r.launchArgs[:len(r.launchArgs)-1]...)
	if r.launchBin != "pi" || !reflect.DeepEqual(seat.Argv, wantArgv) {
		t.Fatalf("seat argv = %q, want %q", seat.Argv, wantArgv)
	}
	for _, arg := range seat.Argv {
		if arg == r.prompt {
			t.Fatal("the seat argv carries the prompt; it is pinned by digest only")
		}
	}
	if seat.PromptDigest != sha256Content([]byte(r.prompt)) || r.prompt == "" {
		t.Fatalf("seat prompt digest = %q, want the digest of the launch prompt", seat.PromptDigest)
	}
	if !reflect.DeepEqual(seat.Fence, r.fenceRecord) {
		t.Fatalf("seat fence = %+v, want the probe's record %+v", seat.Fence, r.fenceRecord)
	}
	if runtime.GOOS == "darwin" {
		if !seat.Fence.Applied || seat.Fence.Role != "soldier" || seat.Fence.ProfileDigest == "" {
			t.Fatalf("seat fence = %+v, want an applied soldier fence on darwin", seat.Fence)
		}
	} else if seat.Fence.Applied || seat.Fence.Reason == "" {
		t.Fatalf("seat fence = %+v, want the no-fence reason off darwin", seat.Fence)
	}
}

// TestLaunchWritesTheManifestOfTheLaunchingHarness proves a launch binds the
// five core artifacts plus exactly the worktree files its harness declares,
// and that teardown's verification accepts the manifest the launch wrote.
func TestLaunchWritesTheManifestOfTheLaunchingHarness(t *testing.T) {
	core := []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName}
	for _, tc := range []struct {
		harness string
		ready   string
		want    []string
	}{
		{"pi", "> ready", append(append([]string{}, core...), ".pi/settings.json")},
		{"claude", "bypass permissions on", core},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			isolateHuman(t)
			f := newLaunchFixture(t, "manifest-"+tc.harness)
			f.runner.harness = tc.harness
			f.runner.projectConfig.Soldier.Harness = tc.harness
			f.endpoints.ready = tc.ready
			if err := runLaunchPhases(f, ""); err != nil {
				t.Fatal(err)
			}
			manifest, err := ReadManifest(f.runner.wtPath)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, a := range manifest.Artifacts {
				got = append(got, a.Path)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("manifest paths = %v, want %v", got, tc.want)
			}
			if err := VerifyLaunchArtifacts(f.runner.wtPath, f.runner.manifestSHA256); err != nil {
				t.Fatalf("VerifyLaunchArtifacts: %v", err)
			}
		})
	}
}

// TestLaunchLeavesTheTargetWorktreeClean proves a launch into a repo with no
// .gitignore leaves the soldier's own worktree free of untracked launch files,
// and that the ignore does not reach the repo's main checkout.
func TestLaunchLeavesTheTargetWorktreeClean(t *testing.T) {
	for _, tc := range []struct{ harness, ready string }{
		{"pi", "> ready"},
		{"claude", "bypass permissions on"},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			isolateHuman(t)
			f := newLaunchFixture(t, "clean-"+tc.harness)
			f.runner.harness = tc.harness
			f.runner.projectConfig.Soldier.Harness = tc.harness
			f.endpoints.ready = tc.ready
			if err := runLaunchPhases(f, ""); err != nil {
				t.Fatal(err)
			}
			if got := gitTestRun(t, f.runner.wtPath, "status", "--porcelain", "--untracked-files=all"); got != "" {
				t.Errorf("soldier worktree is dirty after launch:\n%s", got)
			}
			if !checkIgnored(t, f.runner.wtPath, CharterName) {
				t.Errorf("soldier worktree does not ignore %s", CharterName)
			}
			if checkIgnored(t, f.repoPath, CharterName) {
				t.Errorf("main checkout ignores %s; the soldier excludes leaked", CharterName)
			}
		})
	}
}

// TestRefusedExcludesLeaveNoPaneOrEndpoint proves a target repo whose common
// config git would read differently under worktree config stops the launch
// before any launch file is written or anything is submitted.
func TestRefusedExcludesLeaveNoPaneOrEndpoint(t *testing.T) {
	isolateHuman(t)
	f := newLaunchFixture(t, "excludes-refused")
	gitTestRun(t, f.repoPath, "config", "--local", "core.bare", "true")
	err := runLaunchPhases(f, "")
	if err == nil || !strings.HasPrefix(err.Error(), "prompt: excluding soldier launch files: ") {
		t.Fatalf("err = %v, want the prompt-phase excludes refusal", err)
	}
	if _, serr := os.Stat(filepath.Join(f.runner.wtPath, BriefName)); !os.IsNotExist(serr) {
		t.Errorf("launch file written despite the refusal: %v", serr)
	}
	if f.endpoints.createCount() != 0 || f.endpoints.submitCount() != 0 {
		t.Fatalf("endpoint creates=%d submits=%d, want none", f.endpoints.createCount(), f.endpoints.submitCount())
	}
}

// TestRefusedFenceProbeLeavesNoPaneOrEndpoint proves a fence refusal stops the
// launch before any pane or endpoint is allocated.
func TestRefusedFenceProbeLeavesNoPaneOrEndpoint(t *testing.T) {
	isolateHuman(t)
	f := newLaunchFixture(t, "fence-refused")
	f.runner.effectiveMode = "no-mistakes" // the primary has no gate remote, so the fence cannot name its gate
	err := runLaunchPhases(f, "")
	if err == nil || !strings.HasPrefix(err.Error(), "probe-fence: launch fence: ") {
		t.Fatalf("err = %v, want the probe-fence refusal", err)
	}
	if f.endpoints.createCount() != 0 || f.endpoints.submitCount() != 0 {
		t.Fatalf("endpoint creates=%d submits=%d, want none before the fence is proven", f.endpoints.createCount(), f.endpoints.submitCount())
	}
	if agg := f.aggregate(); agg.Endpoint != nil || agg.AcquiredEndpoint != nil || agg.LaunchEvidence != nil {
		t.Fatalf("aggregate carries endpoint records after a refused fence: %+v", agg)
	}
}
