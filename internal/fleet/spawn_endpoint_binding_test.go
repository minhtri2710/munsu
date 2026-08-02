package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
)

// bindWorktreeForSpawnFixture binds a generation-scoped worktree through the
// Authority so ConfirmSpawn can evaluate its worktree-binding precondition.
func bindWorktreeForSpawnFixture(t *testing.T, auth *taskauthority.Authority, taskID string) {
	t.Helper()
	if _, err := auth.BindWorktree(taskauthority.BindWorktreeRequest{
		OperationID: "op-bind-wt-" + taskID, Actor: taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID: taskID, ExpectedGeneration: 1,
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
	}); err != nil {
		t.Fatalf("BindWorktree(%s): %v", taskID, err)
	}
}

func TestEndpointBindingOrderingPersistsBindingMetadataThenWorking(t *testing.T) {
	homeDir := t.TempDir()
	auth := taskauthority.New(taskauthority.NewMemStore())
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "op-create-bind", Actor: taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID: "bind-task", Owner: "general", Description: "Ready", Kind: "ship", Project: "test-proj",
	}); err != nil {
		t.Fatal(err)
	}
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
	before, err := auth.Get("bind-task")
	if err != nil {
		t.Fatal(err)
	}
	if before.Phase == taskauthority.PhaseWorking || before.Endpoint != nil {
		t.Fatalf("meta write alone must not bind the endpoint or mark the task working: %+v", before)
	}
	// ConfirmSpawn commits the endpoint binding and the working transition
	// together in one Store transaction.
	if err := r.confirmSpawn(); err != nil {
		t.Fatalf("confirmSpawn: %v", err)
	}
	bound, err := auth.Get("bind-task")
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
	if err := r.confirmSpawn(); err == nil || !strings.Contains(err.Error(), "not composed") {
		t.Fatalf("confirmSpawn without Authority error = %v, want composition failure", err)
	}
}

func TestSpawnBindWorktreePersistsExactRepositoryIdentityAndLease(t *testing.T) {
	primary := initRepoForSpawnBinding(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSpawnBinding(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := t.TempDir()
	auth := taskauthority.New(mustNewFSAuthorityStore(t, homeDir))
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "op-create-bind-wt", Actor: taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID: "bind-wt", Owner: "general", Description: "Ready", Kind: "ship", Project: "test-proj",
	}); err != nil {
		t.Fatal(err)
	}
	r := &Runner{homeDir: homeDir, args: Args{ID: "bind-wt", ProjectName: "test-proj", Authority: auth}, projPath: primary, wtPath: worktree}
	if err := r.bindWorktree(); err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}
	agg, err := auth.Get("bind-wt")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree == nil || agg.Worktree.RepositoryIdentity == "" || agg.Worktree.Path == "" || agg.Worktree.GitDir == "" || agg.Worktree.CommonDir == "" || agg.Worktree.GitDir == agg.Worktree.CommonDir || agg.Worktree.Head == "" || agg.Worktree.LeaseID == "" || agg.Worktree.FenceToken == "" {
		t.Fatalf("worktree binding=%+v", agg.Worktree)
	}
	if agg.Phase == taskauthority.PhaseWorking {
		t.Fatal("worktree binding alone must not mark task working")
	}
	// The lease marker committed atomically with the binding and remains
	// readable by the legacy lease check on the exact home.
	if !home.TaskWorktreeLeaseActive(homeDir, "bind-wt", home.TaskWorktreeBinding{
		TaskGeneration: agg.Generation.String(),
		LeaseID:        agg.Worktree.LeaseID,
		FenceToken:     agg.Worktree.FenceToken,
	}) {
		t.Fatalf("lease marker not active for binding %+v", agg.Worktree)
	}
}

// mustNewFSAuthorityStore builds a filesystem Store over a temp home for
// spawn binding tests that must observe durable lease markers.
func mustNewFSAuthorityStore(t *testing.T, homeDir string) *taskauthorityfs.Store {
	t.Helper()
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	return store
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
	auth := taskauthority.New(taskauthority.NewMemStore())
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "op-create-meta", Actor: taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID: "bind-task", Owner: "general", Description: "Ready", Kind: "ship", Project: "test-proj",
	}); err != nil {
		t.Fatal(err)
	}
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
	agg, err := auth.Get("bind-task")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase == taskauthority.PhaseWorking || agg.Endpoint != nil {
		t.Fatalf("metadata write failure must leave task non-working: %+v", agg)
	}
}

func TestEndpointBindingFailureLeavesTaskNonWorking(t *testing.T) {
	homeDir := t.TempDir()
	auth := taskauthority.New(taskauthority.NewMemStore())
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "op-create-fail", Actor: taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID: "bind-task", Owner: "general", Description: "Ready", Kind: "ship", Project: "test-proj",
	}); err != nil {
		t.Fatal(err)
	}
	bindWorktreeForSpawnFixture(t, auth, "bind-task")
	r := &Runner{homeDir: homeDir, args: Args{ID: "bind-task", Authority: auth}, endpoint: CreatedEndpoint{Backend: "tmux"}}
	if err := r.confirmSpawn(); err == nil {
		t.Fatal("expected binding failure for incomplete endpoint")
	}
	agg, err := auth.Get("bind-task")
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

func (s *sequenceEndpointCapabilities) Create(CreateRequest) (CreatedEndpoint, error) {
	return s.created, nil
}
func (s *sequenceEndpointCapabilities) Submit(CreatedEndpoint, string) error { return nil }
func (s *sequenceEndpointCapabilities) Probe(CreatedEndpoint) (SpawnEndpointObservation, error) {
	if len(s.probes) == 0 {
		return SpawnEndpointObservation{State: EndpointUnknown}, nil
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
		probes:  []SpawnEndpointObservation{{State: EndpointStarting}},
	}
	r := &Runner{homeDir: t.TempDir(), endpoints: caps}
	if err := r.createSession(); err != nil {
		t.Fatalf("createSession should accept starting endpoint: %v", err)
	}
}

func TestFinalEndpointVerificationRejectsStartingObservation(t *testing.T) {
	caps := &sequenceEndpointCapabilities{
		created: CreatedEndpoint{Backend: "herdr", Handle: "session:pane-1"},
		probes:  []SpawnEndpointObservation{{State: EndpointStarting}},
	}
	r := &Runner{homeDir: t.TempDir(), endpoints: caps, endpoint: caps.created, windowID: caps.created.Handle}
	if err := r.verifyEndpointReadyBeforePersist(); err == nil {
		t.Fatal("final verification should reject starting observation")
	}
}
