package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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
	submitErr  error // when set, Submit fails (simulates failure after evidence record)
}

func newReentrantEndpoints() *reentrantEndpointCapabilities {
	return &reentrantEndpointCapabilities{created: map[string]CreatedEndpoint{}, probeAlive: true}
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
	return f.submitErr
}

func (f *reentrantEndpointCapabilities) Probe(ep CreatedEndpoint) (SpawnEndpointObservation, error) {
	if f.probeAlive {
		return SpawnEndpointObservation{State: EndpointAlive}, nil
	}
	return SpawnEndpointObservation{State: EndpointDead}, nil
}

func (f *reentrantEndpointCapabilities) Capture(ep CreatedEndpoint, n int) (string, error) {
	return "> ready", nil
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
	t.Setenv("PATH", filepath.Dir(gitBin))

	snap, err := config.NewResolvedSnapshot(
		config.FleetBaseDocument{
			SchemaVersion: config.FleetBaseSchemaVersion,
			Config:        config.ProjectOverlay{Backend: "tmux", SoldierHarness: "pi", Model: "gpt-5"},
		},
		config.ProjectFacts{Name: "test-proj", Path: repoPath},
		config.BoundaryOverrides{},
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
			Kind:        "ship",
			Authority:   auth,
		},
		harness:             "pi",
		model:               "gpt-5",
		effort:              "high",
		effectiveMode:       "direct-PR",
		requestedMode:       "direct-PR",
		spawnRole:           "general",
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
	phases := []struct {
		name string
		fn   func() error
	}{
		{"begin", r.beginLaunchIntent},
		{"acquire", r.acquireWorktree},
		{"bind-worktree", r.bindWorktree},
		{"prompt", r.buildSoldierPrompt},
		{"create-session", r.createSession},
		{"attach-endpoint", r.attachEndpoint},
		{"submit", r.submitLaunch},
		{"manifest", r.writeLaunchManifest},
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
