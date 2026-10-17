package fleet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// mustOpID builds a validated typed Operation identity.
func mustOpID(t *testing.T, value string) domain.OperationID {
	t.Helper()
	id, err := domain.NewOperationID(value)
	if err != nil {
		t.Fatalf("NewOperationID(%s): %v", value, err)
	}
	return id
}

// mustTaskID builds a validated typed Task identity.
func mustTaskID(t *testing.T, value string) domain.TaskID {
	t.Helper()
	id, err := domain.NewTaskID(value)
	if err != nil {
		t.Fatalf("NewTaskID(%s): %v", value, err)
	}
	return id
}

// mustCanonical creates a canonical Task Authority over a fresh real home.
// The canonical path is the only surface; there is no in-memory fake.
func mustCanonical(t *testing.T) *taskauthority.Canonical {
	t.Helper()
	h, err := home.Init(t.TempDir())
	if err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	return c
}

// mustHome opens the initialized canonical home at dir, returning the
// canonical-rooted home. The canonical root may resolve symlinks (e.g.
// /var -> /private/var on macOS), so callers must derive all home paths from
// the returned Home.Root() rather than from the literal dir when canonical
// state and projections must agree.
func mustHome(t *testing.T, dir string) *home.Home {
	t.Helper()
	h, err := home.Open(dir)
	if err != nil {
		t.Fatalf("home.Open(%s): %v", dir, err)
	}
	return h
}

// canonicalCreateTask creates one task through the canonical surface.
func canonicalCreateTask(t *testing.T, c *taskauthority.Canonical, taskID, kind, project string) {
	t.Helper()
	req := taskauthority.CanonicalCreateRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Owner: "general", Description: "Ready", Kind: kind, Reason: "test",
	}
	if project != "" {
		p, err := domain.NewProjectID(project)
		if err != nil {
			t.Fatalf("NewProjectID(%s): %v", project, err)
		}
		req.Project = p
	}
	op, err := domain.NewOperation(mustOpID(t, "op-create-"+taskID), req)
	if err != nil {
		t.Fatalf("NewOperation(%s): %v", taskID, err)
	}
	if _, err := c.Create(op, req); err != nil {
		t.Fatalf("Create(%s): %v", taskID, err)
	}
}

// canonicalAtHome builds a canonical Task Authority over the initialized home
// at homeDir (the home must already be initialized by the caller). It is used
// by spawn binding tests that must observe durable lease markers on the exact
// home the Runner resolves.
func canonicalAtHome(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	c, err := taskauthority.NewCanonical(mustHome(t, homeDir))
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	return c
}

// seedLaunchIntent commits a deterministic BeginSpawn launch intent for the
// task through the canonical surface and adopts it onto the Runner, mirroring
// the production beginLaunchIntent derivation (same snapshot digest, explicit
// identities, and one-time reservation fences) so phase tests exercise the
// intent-fenced launch path.
func seedLaunchIntent(t *testing.T, auth *taskauthority.Canonical, r *Runner, taskID string) {
	t.Helper()
	wtRes, wtFence, epRes, epFence := spawnReservationIdentities(taskID, 1)
	req := taskauthority.CanonicalBeginSpawnRequest{
		HomeID:                auth.HomeID(),
		TaskID:                mustTaskID(t, taskID),
		Precondition:          domain.Of(1, 1),
		SnapshotDigest:        strings.Repeat("a", 64),
		Backend:               "tmux",
		Harness:               "pi",
		Model:                 "gpt-5",
		Effort:                "high",
		Mode:                  "direct-PR",
		Kind:                  "ship",
		Project:               "test-proj",
		ParentTaskID:          "general",
		LaunchID:              fmt.Sprintf("launch-%s-1", taskID),
		WindowLabel:           fmt.Sprintf("%s-g1", soldierTabLabel("test-proj", taskID)),
		WorktreeReservationID: wtRes,
		WorktreeFenceToken:    wtFence,
		EndpointReservationID: epRes,
		EndpointFenceToken:    epFence,
		SeatPathDirs:          []string{"/usr/bin"}, EndpointIncarnation: "inc-" + taskID,
		SeatEnv: testSeatEnv(),
		Reason:  "spawn",
	}
	op, err := domain.NewOperation(mustOpID(t, "spawn-begin-"+taskID+"-1"), req)
	if err != nil {
		t.Fatalf("NewOperation(begin %s): %v", taskID, err)
	}
	if _, err := auth.BeginSpawn(op, req); err != nil {
		t.Fatalf("BeginSpawn(%s): %v", taskID, err)
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatalf("Get(%s): %v", taskID, err)
	}
	if agg.Launch == nil {
		t.Fatalf("launch intent missing after BeginSpawn for %s", taskID)
	}
	r.launch = agg.Launch
	r.launchID = agg.Launch.LaunchID
	r.windowLabel = agg.Launch.WindowLabel
	r.incarnation = agg.Launch.EndpointIncarnation
}

func TestConfirmSpawnBindsTheEndpointAndWorkingTogether(t *testing.T) {
	f := newLaunchFixture(t, "bind-task")
	if err := runLaunchPhases(f, "verify"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("runLaunchPhases: %v", err)
	}
	if before := f.aggregate(); before.Phase == taskauthority.PhaseWorking || before.Endpoint != nil {
		t.Fatalf("aggregate before confirm=%+v", before)
	}
	// ConfirmSpawn commits the endpoint binding and the working transition
	// together in one canonical operation.
	if _, err := f.runner.confirmSpawn(); err != nil {
		t.Fatalf("confirmSpawn: %v", err)
	}
	r, bound := f.runner, f.aggregate()
	if bound.Endpoint == nil || bound.Endpoint.Backend != r.endpoint.Backend || bound.Endpoint.Handle != r.endpoint.Handle || bound.Endpoint.LeaseID != r.launch.EndpointReservationID || bound.Endpoint.FenceToken != r.launch.EndpointFenceToken || bound.Endpoint.BoundAtUnix <= 0 {
		t.Fatalf("endpoint binding=%+v", bound.Endpoint)
	}
	if bound.Phase != taskauthority.PhaseWorking {
		t.Fatalf("phase=%q want working after confirm spawn", bound.Phase)
	}
}

// The snapshot row of a launched task carries the aggregate's launch
// submission and phase change times.
func TestSnapshotCarriesLaunchAndPhaseChangeTimes(t *testing.T) {
	f := newLaunchFixture(t, "times-task")
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("runLaunchPhases: %v", err)
	}
	agg := f.aggregate()
	if agg.Phase != taskauthority.PhaseWorking || agg.LaunchEvidence == nil || agg.PhaseChangedAt == 0 {
		t.Fatalf("launched aggregate = phase %q, launch evidence %v, phase changed at %d", agg.Phase, agg.LaunchEvidence, agg.PhaseChangedAt)
	}
	snap, err := Snapshot(f.homeDir, SnapshotDependencies{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("snapshot rows = %+v, want one", snap.Tasks)
	}
	if ts := snap.Tasks[0]; ts.LaunchedAt != agg.LaunchEvidence.SubmittedAt || ts.PhaseChangedAt != agg.PhaseChangedAt {
		t.Fatalf("row launched at %d, phase changed at %d; aggregate %d, %d", ts.LaunchedAt, ts.PhaseChangedAt, agg.LaunchEvidence.SubmittedAt, agg.PhaseChangedAt)
	}
}

// The snapshot row of a live launched seat carries the context use its
// harness wrote to the seat's own session dir; a seat that is not live, or has
// no launch evidence or worktree, reads none.
func TestSnapshotCarriesSeatContextUsage(t *testing.T) {
	for _, tc := range []struct {
		name         string
		state        EndpointObservationState
		drop         func(*taskauthority.Aggregate) // nil keeps the launched aggregate
		used, window int
	}{
		{"live", EndpointAlive, nil, 64000, 256000},
		{"dead", EndpointDead, nil, 0, 0},
		{"live without launch evidence", EndpointAlive, func(a *taskauthority.Aggregate) { a.LaunchEvidence = nil }, 0, 0},
		{"live without a worktree", EndpointAlive, func(a *taskauthority.Aggregate) { a.Worktree = nil }, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchFixture(t, "ctx-task")
			if err := runLaunchPhases(f, ""); err != nil {
				t.Fatalf("runLaunchPhases: %v", err)
			}
			agg := f.aggregate()
			if agg.Worktree == nil || agg.LaunchEvidence == nil {
				t.Fatalf("launched aggregate = worktree %v, launch evidence %v", agg.Worktree, agg.LaunchEvidence)
			}
			agentDir, err := piAgentDirPath(f.homeDir, f.taskID)
			if err != nil {
				t.Fatal(err)
			}
			cwd, err := filepath.EvalSymlinks(agg.Worktree.Path)
			if err != nil {
				t.Fatal(err)
			}
			sessions := filepath.Join(agentDir, "sessions", "--"+strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")+"--")
			if err := os.MkdirAll(sessions, 0o755); err != nil {
				t.Fatal(err)
			}
			line := `{"type":"message","message":{"role":"assistant","provider":"p","model":"gpt-5","stopReason":"stop","usage":{"totalTokens":64000}}}` + "\n"
			if err := os.WriteFile(filepath.Join(sessions, "s.jsonl"), []byte(line), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{"p":{"models":[{"id":"gpt-5","contextWindow":256000}]}}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.drop != nil {
				tamperTaskAggregate(t, f.homeDir, f.taskID, tc.drop)
			}
			snap, err := Snapshot(f.homeDir, SnapshotDependencies{Endpoint: snapshotProbe{status: endpointStatusFromState(tc.state)}})
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if len(snap.Tasks) != 1 {
				t.Fatalf("snapshot rows = %+v, want one", snap.Tasks)
			}
			if ts := snap.Tasks[0]; ts.ContextUsed != tc.used || ts.ContextWindow != tc.window {
				t.Errorf("row context %d/%d, want %d/%d", ts.ContextUsed, ts.ContextWindow, tc.used, tc.window)
			}
		})
	}
}

// A fenced claude seat writes sessions to its own config dir; an unfenced one
// to the Human's claude config dir it runs with.
func TestSeatContextUsageReadsTheClaudeSeatsConfigDir(t *testing.T) {
	taskHome, human := t.TempDir(), t.TempDir()
	t.Setenv("HOME", human)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fenced, err := claudeConfigDirPath(taskHome, "t1")
	if err != nil {
		t.Fatal(err)
	}
	for cfg, n := range map[string]string{fenced: "111", filepath.Join(human, ".claude"): "222"} {
		dir := filepath.Join(cfg, "projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(cwd, "-"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		row := `{"type":"assistant","isSidechain":false,"message":{"usage":{"cache_read_input_tokens":` + n + `}}}`
		if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(row+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		fenced bool
		want   int
	}{{true, 111}, {false, 222}} {
		if used, _ := seatContextUsage(taskHome, "t1", harness.Claude, tc.fenced, cwd); used != tc.want {
			t.Errorf("fenced=%v: used %d, want %d", tc.fenced, used, tc.want)
		}
	}
}

func TestConfirmSpawnFailsClosedWithoutAuthority(t *testing.T) {
	homeDir := t.TempDir()
	r := &Runner{homeDir: homeDir, args: Args{ID: "bind-task"}, endpoint: CreatedEndpoint{Backend: "herdr", Handle: "session:pane-1"}}
	if _, err := r.confirmSpawn(); err == nil || !strings.Contains(err.Error(), "not composed") {
		t.Fatalf("confirmSpawn without Authority error = %v, want composition failure", err)
	}
}

func TestSpawnBindWorktreePersistsExactRepositoryIdentityAndLease(t *testing.T) {
	primary := initRepoForSpawnBinding(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSpawnBinding(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	auth := canonicalAtHome(t, homeDir)
	canonicalCreateTask(t, auth, "bind-wt", "ship", "test-proj")
	r := &Runner{homeDir: homeDir, args: Args{ID: "bind-wt", ProjectName: "test-proj", Authority: auth}, projPath: primary, wtPath: worktree}
	seedLaunchIntent(t, auth, r, "bind-wt")
	if _, err := r.bindWorktree(); err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}
	agg, err := auth.Get(mustTaskID(t, "bind-wt"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree == nil || agg.Worktree.RepositoryIdentity == "" || agg.Worktree.Path == "" || agg.Worktree.GitDir == "" || agg.Worktree.CommonDir == "" || agg.Worktree.GitDir == agg.Worktree.CommonDir || agg.Worktree.BaseHead == "" || agg.Worktree.LeaseID == "" || agg.Worktree.FenceToken == "" {
		t.Fatalf("worktree binding=%+v", agg.Worktree)
	}
	if agg.Phase == taskauthority.PhaseWorking {
		t.Fatal("worktree binding alone must not mark task working")
	}
	// The binding carries the launch intent's one-time worktree reservation
	// fence (never a freshly minted identity) so recovery under the same
	// Operation ID/generation re-adopts the exact binding.
	if agg.Worktree.LeaseID != r.launch.WorktreeReservationID || agg.Worktree.FenceToken != r.launch.WorktreeFenceToken {
		t.Fatalf("worktree binding lease/fence %s/%s does not match launch reservation %s/%s", agg.Worktree.LeaseID, agg.Worktree.FenceToken, r.launch.WorktreeReservationID, r.launch.WorktreeFenceToken)
	}
}

func initRepoForSpawnBinding(t *testing.T, dir string) string {
	t.Helper()
	runGitForSpawnBinding(t, dir, "init", "-b", "main")
	runGitForSpawnBinding(t, dir, "config", "user.email", "test@example.com")
	runGitForSpawnBinding(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitForSpawnBinding(t, dir, "add", "README.md")
	runGitForSpawnBinding(t, dir, "commit", "-m", "initial")
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func runGitForSpawnBinding(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test User", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test User", "GIT_COMMITTER_EMAIL=test@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func TestEndpointBindingFailureLeavesTaskNonWorking(t *testing.T) {
	f := newLaunchFixture(t, "bind-task")
	if err := runLaunchPhases(f, "verify"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("runLaunchPhases: %v", err)
	}
	f.runner.endpoint.Handle = "foreign:pane-9"
	if _, err := f.runner.confirmSpawn(); err == nil || !strings.Contains(err.Error(), "does not match the recorded acquired endpoint") {
		t.Fatalf("confirmSpawn error = %v, want the acquired-endpoint mismatch refusal", err)
	}
	if agg := f.aggregate(); agg.Phase == taskauthority.PhaseWorking || agg.Endpoint != nil {
		t.Fatalf("aggregate after failed confirm=%+v", agg)
	}
}

type sequenceEndpointCapabilities struct {
	created CreatedEndpoint
	probes  []SpawnEndpointObservation
}

func (s *sequenceEndpointCapabilities) CreateReserved(CreateRequest) (CreatedEndpoint, error) {
	return s.created, nil
}
func (s *sequenceEndpointCapabilities) Submit(CreatedEndpoint, string) error { return nil }
func (s *sequenceEndpointCapabilities) Probe(CreatedEndpoint) (SpawnEndpointObservation, error) {
	if len(s.probes) == 0 {
		return endpointStatusFromState(EndpointUnknown), nil
	}
	result := s.probes[0]
	s.probes = s.probes[1:]
	return result, nil
}
func (s *sequenceEndpointCapabilities) Capture(CreatedEndpoint, int) (string, error) {
	return "ready", nil
}
func (s *sequenceEndpointCapabilities) Dispose(CreatedEndpoint) error { return nil }

func TestCreateSessionAcceptsStartingObservation(t *testing.T) {
	caps := &sequenceEndpointCapabilities{
		created: CreatedEndpoint{Backend: "herdr", Handle: "session:pane-1"},
		probes:  []SpawnEndpointObservation{endpointStatusFromState(EndpointStarting)},
	}
	r := &Runner{homeDir: t.TempDir(), endpoints: caps, launch: &taskauthority.LaunchIntent{}}
	if err := r.createSession(); err != nil {
		t.Fatalf("createSession should accept starting endpoint: %v", err)
	}
}

func TestFinalEndpointVerificationRejectsStartingObservation(t *testing.T) {
	caps := &sequenceEndpointCapabilities{
		created: CreatedEndpoint{Backend: "herdr", Handle: "session:pane-1"},
		probes:  []SpawnEndpointObservation{endpointStatusFromState(EndpointStarting)},
	}
	r := &Runner{homeDir: t.TempDir(), endpoints: caps, launch: &taskauthority.LaunchIntent{}, endpoint: caps.created, windowID: caps.created.Handle}
	if err := r.verifyEndpointReadyBeforePersist(); err == nil {
		t.Fatal("final verification should reject starting observation")
	}
}

// The ConfirmSpawn conflict seam: with the manifest and launch evidence intact
// through final verification, a runner endpoint identity that differs from the
// recorded acquired endpoint is refused by BindEndpoint, and the refusal changes
// no canonical state. The only mutation is the runner's endpoint handle.
func TestConfirmSpawnRefusesEndpointSubstitutionWithIntactProof(t *testing.T) {
	f := newLaunchFixture(t, "confirm-conflict")
	if err := runLaunchPhases(f, "verify"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("runLaunchPhases through verify: %v", err)
	}
	before := f.aggregate()
	if before.LaunchEvidence == nil || before.Worktree == nil || before.Worktree.LaunchManifest == nil || before.AcquiredEndpoint == nil || before.Phase != taskauthority.PhaseQueued || before.Endpoint != nil {
		t.Fatalf("pre-confirm proof state = %+v", before)
	}
	f.runner.endpoint.Handle = "pane-substituted"
	if _, err := f.runner.confirmSpawn(); !errors.Is(err, taskauthority.ErrConflict) || !strings.Contains(err.Error(), "does not match the recorded acquired endpoint") {
		t.Fatalf("confirmSpawn = %v, want ErrConflict naming the recorded acquired endpoint", err)
	}
	after := f.aggregate()
	if after.Revision != before.Revision || after.Phase != taskauthority.PhaseQueued || after.Endpoint != nil ||
		!reflect.DeepEqual(after.LaunchEvidence, before.LaunchEvidence) ||
		!reflect.DeepEqual(after.Worktree.LaunchManifest, before.Worktree.LaunchManifest) ||
		!reflect.DeepEqual(after.AcquiredEndpoint, before.AcquiredEndpoint) {
		t.Fatalf("refused confirm changed canonical state:\nbefore %+v\nafter  %+v", before, after)
	}
}
