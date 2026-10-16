package cli

import (
	"fmt"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// initHomeWithTask seeds a canonical queued task with the given kind into the
// home (clean break: guards and snapshots read only authoritative Task
// Authority records).
func initHomeWithTask(t *testing.T, homeDir, id, kind string) {
	t.Helper()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	cliSeedCanonicalTask(t, homeDir, id, kind)
}

// cliSeedCanonicalTask creates one queued canonical task of the given kind.
func cliSeedCanonicalTask(t *testing.T, homeDir, id, kind string) {
	t.Helper()
	tid, err := domain.NewTaskID(id)
	if err != nil {
		t.Fatal(err)
	}
	auth := cliCanonicalForHome(t, homeDir)
	project, err := domain.NewProjectID("munsu")
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: tid, Owner: "owner",
		Description: "work", Kind: kind, Project: project, Reason: "test",
	}
	if kind == "scout" {
		req.ScoutScope = "investigate scope"
	}
	opID, err := domain.NewOperationID("op-create-" + id)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, req); err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
}

// cliCanonicalForHome opens the canonical Task Authority over homeDir.
func cliCanonicalForHome(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func cliPrecondition(t *testing.T, auth *taskauthority.Canonical, tid domain.TaskID) domain.Precondition {
	t.Helper()
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Of(uint64(agg.Generation), uint64(agg.Revision))
}

// beginTestLaunch commits a launch intent on id's current generation.
func beginTestLaunch(t *testing.T, auth *taskauthority.Canonical, id, backend string) taskauthority.CanonicalBeginSpawnRequest {
	t.Helper()
	tid := mustTaskIDFor(t, id)
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	g := fmt.Sprintf("%s-g%d", id, agg.Generation)
	begin := taskauthority.CanonicalBeginSpawnRequest{
		HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), SnapshotDigest: guardDigest("snapshot:" + g),
		Backend: backend, Harness: "pi", Model: "opus", Effort: "high", Mode: "direct-PR", Kind: agg.Definition.Kind, Project: agg.Definition.Project,
		LaunchID: "launch-" + g, WindowLabel: "window-" + g,
		WorktreeReservationID: "wt-res-" + g, WorktreeFenceToken: "wt-fence-" + g,
		EndpointReservationID: "ep-res-" + g, EndpointFenceToken: "ep-fence-" + g, EndpointIncarnation: "inc-" + g,
		SeatPathDirs: []string{"/usr/bin"}, SeatEnv: taskauthority.SeatEnv{TempDir: "/tmp", UserHome: "/home/tester"}, Reason: "spawn",
	}
	if _, err := auth.BeginSpawn(mustCanonicalOp(t, "op-begin-"+g, begin), begin); err != nil {
		t.Fatalf("BeginSpawn(%s): %v", g, err)
	}
	return begin
}

// bindLaunchWorktree binds wt under begin's worktree reservation fence.
func bindLaunchWorktree(t *testing.T, auth *taskauthority.Canonical, begin taskauthority.CanonicalBeginSpawnRequest, wt taskauthority.WorktreeBinding) {
	t.Helper()
	wt.LeaseID, wt.FenceToken = begin.WorktreeReservationID, begin.WorktreeFenceToken
	req := taskauthority.CanonicalBindWorktreeRequest{HomeID: auth.HomeID(), TaskID: begin.TaskID, Precondition: cliPrecondition(t, auth, begin.TaskID), Binding: wt, Reason: "spawn"}
	if _, err := auth.BindWorktree(mustCanonicalOp(t, "op-bindwt-"+begin.LaunchID, req), req); err != nil {
		t.Fatalf("BindWorktree(%s): %v", begin.LaunchID, err)
	}
}

// launchToWorking records the manifest, acquired endpoint and launch evidence
// of begin's bound worktree, then binds ep under the endpoint fence.
func launchToWorking(t *testing.T, auth *taskauthority.Canonical, begin taskauthority.CanonicalBeginSpawnRequest, ep taskauthority.EndpointBinding) {
	t.Helper()
	tid, id := begin.TaskID, begin.LaunchID
	manifest := taskauthority.CanonicalRecordLaunchManifestRequest{
		HomeID: auth.HomeID(), TaskID: tid, Precondition: cliPrecondition(t, auth, tid), LaunchID: begin.LaunchID,
		WorktreeLeaseID: begin.WorktreeReservationID, WorktreeFenceToken: begin.WorktreeFenceToken,
		ManifestSHA256: guardDigest("manifest:" + id), Reason: "manifest prepared",
	}
	if _, err := auth.RecordLaunchManifest(mustCanonicalOp(t, "op-manifest-"+id, manifest), manifest); err != nil {
		t.Fatalf("RecordLaunchManifest(%s): %v", id, err)
	}
	ep.Backend, ep.LeaseID, ep.FenceToken, ep.Incarnation = begin.Backend, begin.EndpointReservationID, begin.EndpointFenceToken, begin.EndpointIncarnation
	attach := taskauthority.CanonicalAttachEndpointRequest{
		HomeID: auth.HomeID(), TaskID: tid, Precondition: cliPrecondition(t, auth, tid), Backend: ep.Backend, Handle: ep.Handle,
		LeaseID: ep.LeaseID, FenceToken: ep.FenceToken, SessionOwner: ep.SessionOwner, WorkspaceID: ep.WorkspaceID, TabID: ep.TabID,
		Incarnation: ep.Incarnation, Reason: "attach",
	}
	if _, err := auth.AttachEndpoint(mustCanonicalOp(t, "op-attach-"+id, attach), attach); err != nil {
		t.Fatalf("AttachEndpoint(%s): %v", id, err)
	}
	record := taskauthority.CanonicalRecordLaunchRequest{
		HomeID: auth.HomeID(), TaskID: tid, Precondition: cliPrecondition(t, auth, tid), LaunchID: begin.LaunchID,
		CommandDigest: guardDigest("launch:" + id), Reason: "record",
		Seat: taskauthority.LaunchSeat{Argv: []string{begin.Harness}, PromptDigest: guardDigest("prompt"), Fence: taskauthority.FenceRecord{Reason: "no fence on this host"}},
	}
	if _, err := auth.RecordLaunch(mustCanonicalOp(t, "op-record-"+id, record), record); err != nil {
		t.Fatalf("RecordLaunch(%s): %v", id, err)
	}
	bind := taskauthority.CanonicalBindEndpointRequest{HomeID: auth.HomeID(), TaskID: tid, Precondition: cliPrecondition(t, auth, tid), Binding: ep, Reason: "spawn"}
	if _, err := auth.BindEndpoint(mustCanonicalOp(t, "op-bindep-"+id, bind), bind); err != nil {
		t.Fatalf("BindEndpoint(%s): %v", id, err)
	}
}
