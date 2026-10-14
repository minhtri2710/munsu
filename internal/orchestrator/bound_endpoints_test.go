package orchestrator

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// seedTask creates a queued ship task in the home's canonical store,
// initializing the home when it has none.
func seedTask(t *testing.T, homeDir, id string) (*taskauthority.Canonical, domain.TaskID) {
	t.Helper()
	h, err := mhome.Open(homeDir)
	if errors.Is(err, mhome.ErrNotInitialized) {
		h, err = mhome.Init(homeDir)
	}
	if err != nil {
		t.Fatal(err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	tid, err := domain.NewTaskID(id)
	if err != nil {
		t.Fatal(err)
	}
	create := taskauthority.CanonicalCreateRequest{HomeID: c.HomeID(), TaskID: tid, Owner: "owner", Description: "work", Kind: taskauthority.KindShip, Reason: "create"}
	if _, err := c.Create(testOperation(t, "create-"+id, create), create); err != nil {
		t.Fatal(err)
	}
	return c, tid
}

// seedBoundSoldier commits a live, bound ship task through the canonical
// operations: create, bind its worktree, bind ep as its endpoint.
func seedBoundSoldier(t *testing.T, homeDir, id string, ep taskauthority.EndpointBinding) {
	t.Helper()
	c, tid := seedTask(t, homeDir, id)
	precondition := func() domain.Precondition {
		agg, err := c.Get(tid)
		if err != nil {
			t.Fatal(err)
		}
		return domain.Of(uint64(agg.Generation), uint64(agg.Revision))
	}
	wt := taskauthority.CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: tid, Precondition: precondition(), Reason: "bind", Binding: taskauthority.WorktreeBinding{
		RepositoryIdentity: "repo", Path: "/wt/" + id, GitDir: "/repo/.git/worktrees/" + id, CommonDir: "/repo/.git",
		BaseHead: strings.Repeat("a", 40), LeaseID: "lease-wt", FenceToken: "fence-wt", BoundAtUnix: 1,
	}}
	if _, err := c.BindWorktree(testOperation(t, "bind-wt-"+id, wt), wt); err != nil {
		t.Fatal(err)
	}
	ep.LeaseID, ep.FenceToken, ep.Incarnation, ep.BoundAtUnix = "lease-ep", "fence-ep", "inc-ep", 1
	bind := taskauthority.CanonicalBindEndpointRequest{HomeID: c.HomeID(), TaskID: tid, Precondition: precondition(), Binding: ep, Reason: "bind"}
	if _, err := c.BindEndpoint(testOperation(t, "bind-ep-"+id, bind), bind); err != nil {
		t.Fatal(err)
	}
}

func testOperation(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID("op-" + id)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

// The probed endpoints are the canonical live, bound tasks with their bound
// identity; a queued task and a .meta window bind nothing.
func TestLiveBoundEndpoints(t *testing.T) {
	homeDir := t.TempDir()
	if got, err := LiveBoundEndpoints(homeDir); err != nil || got != nil {
		t.Fatalf("uninitialized home: %v, %v; want none", got, err)
	}
	seedBoundSoldier(t, homeDir, "bound", taskauthority.EndpointBinding{Backend: "herdr", Handle: "s:p1", SessionOwner: "s", WorkspaceID: "w1", TabID: "t1"})
	seedTask(t, homeDir, "queued")
	if err := mhome.WriteMeta(homeDir, "queued", map[string]string{"backend": "tmux", "window": "@9"}); err != nil {
		t.Fatal(err)
	}

	got, err := LiveBoundEndpoints(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []BoundEndpoint{{TaskID: "bound", Meta: map[string]string{"backend": "herdr", "window": "s:p1", "session_owner": "s", "workspace_id": "w1", "tab_id": "t1"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LiveBoundEndpoints = %+v, want %+v", got, want)
	}
}
