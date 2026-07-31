package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestEndpointBindingOrderingPersistsBindingMetadataThenWorking(t *testing.T) {
	homeDir := t.TempDir()
	agg, err := home.CreateTaskAggregate(homeDir, "bind-task", "general", "Ready", "ship", "test-proj")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{
		homeDir:       homeDir,
		args:          Args{ID: "bind-task", ProjectName: "test-proj", Kind: "ship"},
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
	if err := r.bindEndpoint(); err != nil {
		t.Fatalf("bindEndpoint: %v", err)
	}
	bound, ok, err := home.ReadCurrentTaskAggregate(homeDir, "bind-task")
	if err != nil || !ok {
		t.Fatalf("ReadCurrentTaskAggregate ok=%v err=%v", ok, err)
	}
	if bound.State == "working" {
		t.Fatal("binding alone must not mark task working")
	}
	if bound.Endpoint == nil || bound.Endpoint.TaskGeneration != agg.Generation || bound.Endpoint.Backend != "herdr" || bound.Endpoint.Handle != "session:pane-1" || bound.Endpoint.LeaseID == "" || bound.Endpoint.FenceToken == "" {
		t.Fatalf("binding=%+v generation=%s", bound.Endpoint, agg.Generation)
	}
	if err := r.writeTaskMeta(); err != nil {
		t.Fatalf("writeTaskMeta: %v", err)
	}
	reloadedMeta, err := home.ReadMeta(homeDir, "bind-task")
	if err != nil {
		t.Fatal(err)
	}
	if reloadedMeta["backend"] != "herdr" || reloadedMeta["window"] != "session:pane-1" || reloadedMeta["herdr_session"] != "session" {
		t.Fatalf("meta=%v", reloadedMeta)
	}
	if err := r.markWorkingAfterBinding(); err != nil {
		t.Fatalf("markWorkingAfterBinding: %v", err)
	}
	working, _, err := home.ReadCurrentTaskAggregate(homeDir, "bind-task")
	if err != nil {
		t.Fatal(err)
	}
	if working.State != "working" {
		t.Fatalf("state=%q want working", working.State)
	}
}

func TestBindWorktreePersistsExactRepositoryIdentityAndLease(t *testing.T) {
	primary := initRepoForSpawnBinding(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSpawnBinding(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := t.TempDir()
	agg, err := home.CreateTaskAggregate(homeDir, "bind-wt", "general", "Ready", "ship", "test-proj")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{homeDir: homeDir, args: Args{ID: "bind-wt", ProjectName: "test-proj"}, projPath: primary, wtPath: worktree}
	if err := r.bindWorktree(); err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}
	bound, ok, err := home.ReadCurrentTaskAggregate(homeDir, "bind-wt")
	if err != nil || !ok {
		t.Fatalf("ReadCurrentTaskAggregate ok=%v err=%v", ok, err)
	}
	if bound.Worktree == nil || bound.Worktree.TaskGeneration != agg.Generation || bound.Worktree.RepositoryIdentity == "" || bound.Worktree.Path == "" || bound.Worktree.GitDir == "" || bound.Worktree.CommonDir == "" || bound.Worktree.GitDir == bound.Worktree.CommonDir || bound.Worktree.Head == "" || bound.Worktree.LeaseID == "" || bound.Worktree.FenceToken == "" {
		t.Fatalf("worktree binding=%+v", bound.Worktree)
	}
	if bound.State == "working" {
		t.Fatal("worktree binding alone must not mark task working")
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
	if _, err := home.CreateTaskAggregate(homeDir, "bind-task", "general", "Ready", "ship", "test-proj"); err != nil {
		t.Fatal(err)
	}
	r := &Runner{homeDir: homeDir, args: Args{ID: "bind-task"}, endpoint: CreatedEndpoint{Backend: "tmux", Handle: "munsu:@1"}}
	if err := r.bindEndpoint(); err != nil {
		t.Fatal(err)
	}
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
	reloaded, _, err := home.ReadCurrentTaskAggregate(homeDir, "bind-task")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State == "working" {
		t.Fatal("metadata write failure must leave task non-working")
	}
}

func TestEndpointBindingFailureLeavesTaskNonWorking(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.CreateTaskAggregate(homeDir, "bind-task", "general", "Ready", "ship", "test-proj"); err != nil {
		t.Fatal(err)
	}
	r := &Runner{homeDir: homeDir, args: Args{ID: "bind-task"}, endpoint: CreatedEndpoint{Backend: "tmux"}}
	if err := r.bindEndpoint(); err == nil {
		t.Fatal("expected binding failure for incomplete endpoint")
	}
	reloaded, _, err := home.ReadCurrentTaskAggregate(homeDir, "bind-task")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State == "working" || reloaded.Endpoint != nil {
		t.Fatalf("aggregate after failed bind=%+v", reloaded)
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
