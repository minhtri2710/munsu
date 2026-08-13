package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
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

// bindWorktreeForSpawnFixture binds a generation-scoped worktree through the
// canonical Authority so ConfirmSpawn can evaluate its worktree-binding
// precondition. The task is created at generation 1 revision 1, so the bind
// carries the exact precondition (1,1).
func bindWorktreeForSpawnFixture(t *testing.T, auth *taskauthority.Canonical, taskID string) {
	t.Helper()
	req := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(1, 1),
		Binding: taskauthority.WorktreeBinding{
			RepositoryIdentity: "repo-identity",
			Path:               "/tmp/wt",
			GitDir:             "/repo/.git/worktrees/wt",
			CommonDir:          "/repo/.git",
			Head:               strings.Repeat("a", 40),
			LeaseID:            "wt-lease-" + taskID,
			FenceToken:         "wt-fence-" + taskID,
			BoundAtUnix:        time.Now().Unix(),
		},
		Reason: "spawn",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-bind-wt-"+taskID), req)
	if err != nil {
		t.Fatalf("NewOperation(%s): %v", taskID, err)
	}
	if _, err := auth.BindWorktree(op, req); err != nil {
		t.Fatalf("BindWorktree(%s): %v", taskID, err)
	}
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
		EndpointIncarnation:   "inc-" + taskID,
		Reason:                "spawn",
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

func TestEndpointBindingOrderingPersistsBindingMetadataThenWorking(t *testing.T) {
	homeDir := t.TempDir()
	auth := mustCanonical(t)
	canonicalCreateTask(t, auth, "bind-task", "ship", "test-proj")
	bindWorktreeForSpawnFixture(t, auth, "bind-task")
	r := &Runner{
		homeDir:       homeDir,
		args:          Args{ID: "bind-task", ProjectName: "test-proj", Kind: "ship", Authority: auth},
		windowID:      "session:pane-1",
		wtPath:        filepath.Join(homeDir, "worktree"),
		projPath:      filepath.Join(homeDir, "project"),
		harness:       "pi",
		effectiveMode: "local-only",
		endpoint: CreatedEndpoint{
			Backend:      "herdr",
			Handle:       "session:pane-1",
			SessionOwner: "session",
			WorkspaceID:  "workspace-1",
			TabID:        "tab-1",
			Incarnation:  "bind-task-incarnation",
			Metadata: map[string]string{
				"herdr_session":      "session",
				"herdr_workspace_id": "workspace-1",
				"herdr_tab_id":       "tab-1",
			},
		},
	}
	// The task meta projection is written before the authoritative working
	// transition: a metadata failure must leave the task non-working, so the
	// projection write must not commit the transition.
	if err := r.writeTaskMeta(); err != nil {
		t.Fatalf("writeTaskMeta: %v", err)
	}
	before, err := auth.Get(mustTaskID(t, "bind-task"))
	if err != nil {
		t.Fatal(err)
	}
	if before.Phase == taskauthority.PhaseWorking || before.Endpoint != nil {
		t.Fatalf("meta write alone must not bind the endpoint or mark the task working: %+v", before)
	}
	// ConfirmSpawn commits the endpoint binding and the working transition
	// together in one canonical operation.
	if _, err := r.confirmSpawn(); err != nil {
		t.Fatalf("confirmSpawn: %v", err)
	}
	bound, err := auth.Get(mustTaskID(t, "bind-task"))
	if err != nil {
		t.Fatal(err)
	}
	if bound.Endpoint == nil || bound.Endpoint.Backend != "herdr" || bound.Endpoint.Handle != "session:pane-1" || bound.Endpoint.LeaseID == "" || bound.Endpoint.FenceToken == "" {
		t.Fatalf("endpoint binding=%+v", bound.Endpoint)
	}
	if bound.Endpoint.SessionOwner != "session" || bound.Endpoint.WorkspaceID != "workspace-1" || bound.Endpoint.TabID != "tab-1" || bound.Endpoint.BoundAtUnix <= 0 {
		t.Fatalf("endpoint binding=%+v", bound.Endpoint)
	}
	if bound.Phase != taskauthority.PhaseWorking {
		t.Fatalf("phase=%q want working after confirm spawn", bound.Phase)
	}
	if bound.Revision != 3 {
		t.Fatalf("revision=%d want 3 (create, bind worktree, confirm spawn)", bound.Revision)
	}
	reloadedMeta, err := home.ReadMeta(homeDir, "bind-task")
	if err != nil {
		t.Fatal(err)
	}
	if reloadedMeta["backend"] != "herdr" || reloadedMeta["window"] != "session:pane-1" || reloadedMeta["herdr_session"] != "session" {
		t.Fatalf("meta=%v", reloadedMeta)
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
	if err := r.bindWorktree(); err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}
	agg, err := auth.Get(mustTaskID(t, "bind-wt"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree == nil || agg.Worktree.RepositoryIdentity == "" || agg.Worktree.Path == "" || agg.Worktree.GitDir == "" || agg.Worktree.CommonDir == "" || agg.Worktree.GitDir == agg.Worktree.CommonDir || agg.Worktree.Head == "" || agg.Worktree.LeaseID == "" || agg.Worktree.FenceToken == "" {
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

func TestEndpointBindingMetadataFailureLeavesTaskNonWorking(t *testing.T) {
	homeDir := t.TempDir()
	auth := mustCanonical(t)
	canonicalCreateTask(t, auth, "bind-task", "ship", "test-proj")
	bindWorktreeForSpawnFixture(t, auth, "bind-task")
	r := &Runner{homeDir: homeDir, args: Args{ID: "bind-task", Authority: auth}, endpoint: CreatedEndpoint{Backend: "tmux", Handle: "munsu:@1"}}
	stateDir := home.StateDir(homeDir)
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	blockedPath := filepath.Join(stateDir, "bind-task.meta")
	if err := os.Mkdir(blockedPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := r.writeTaskMeta(); err == nil || !strings.Contains(err.Error(), "writing task meta") {
		t.Fatalf("writeTaskMeta error=%v, want hard metadata error", err)
	}
	// ConfirmSpawn runs after the meta projection in Run(); a metadata
	// failure leaves the task queued with no endpoint binding.
	agg, err := auth.Get(mustTaskID(t, "bind-task"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase == taskauthority.PhaseWorking || agg.Endpoint != nil {
		t.Fatalf("metadata write failure must leave task non-working: %+v", agg)
	}
}

func TestEndpointBindingFailureLeavesTaskNonWorking(t *testing.T) {
	homeDir := t.TempDir()
	auth := mustCanonical(t)
	canonicalCreateTask(t, auth, "bind-task", "ship", "test-proj")
	bindWorktreeForSpawnFixture(t, auth, "bind-task")
	r := &Runner{homeDir: homeDir, args: Args{ID: "bind-task", Authority: auth}, endpoint: CreatedEndpoint{Backend: "tmux"}}
	if _, err := r.confirmSpawn(); err == nil {
		t.Fatal("expected binding failure for incomplete endpoint")
	}
	agg, err := auth.Get(mustTaskID(t, "bind-task"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase == taskauthority.PhaseWorking || agg.Endpoint != nil {
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
	r := &Runner{homeDir: t.TempDir(), endpoints: caps}
	if err := r.createSession(); err != nil {
		t.Fatalf("createSession should accept starting endpoint: %v", err)
	}
}

func TestFinalEndpointVerificationRejectsStartingObservation(t *testing.T) {
	caps := &sequenceEndpointCapabilities{
		created: CreatedEndpoint{Backend: "herdr", Handle: "session:pane-1"},
		probes:  []SpawnEndpointObservation{endpointStatusFromState(EndpointStarting)},
	}
	r := &Runner{homeDir: t.TempDir(), endpoints: caps, endpoint: caps.created, windowID: caps.created.Handle}
	if err := r.verifyEndpointReadyBeforePersist(); err == nil {
		t.Fatal("final verification should reject starting observation")
	}
}
