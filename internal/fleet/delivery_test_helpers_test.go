package fleet

import (
	"fmt"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// newFleetCanonical builds a canonical Task Authority over a fresh real
// temporary home and returns the canonical plus the home directory.
func newFleetCanonical(t *testing.T) (*taskauthority.Canonical, string) {
	t.Helper()
	root := t.TempDir()
	h, err := home.Init(root)
	if err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	return c, root
}

// mustFleetOperation builds a validated Operation from a typed intent.
func mustFleetOperation(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID(id)
	if err != nil {
		t.Fatalf("NewOperationID(%s): %v", id, err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		t.Fatalf("NewOperation(%s): %v", id, err)
	}
	return op
}

// mustFleetTaskID builds a validated task identity.
func mustFleetTaskID(t *testing.T, value string) domain.TaskID {
	t.Helper()
	id, err := domain.NewTaskID(value)
	if err != nil {
		t.Fatalf("NewTaskID(%s): %v", value, err)
	}
	return id
}

// mustFleetCreate creates a queued task through the canonical authority.
func mustFleetCreate(t *testing.T, c *taskauthority.Canonical, taskID string) taskauthority.Outcome {
	t.Helper()
	tid := mustFleetTaskID(t, taskID)
	project, err := domain.NewProjectID("project")
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID: c.HomeID(), TaskID: tid, Owner: "owner", Description: "work",
		Kind: "ship", Project: project, Reason: "create",
	}
	out, err := c.Create(mustFleetOperation(t, "op-create-"+taskID, req), req)
	if err != nil {
		t.Fatalf("Create(%s): %v", taskID, err)
	}
	return out
}

// launchStep runs one launch-chain operation against the task's current
// generation and revision, failing the test on refusal.
type launchStep struct {
	t      *testing.T
	c      *taskauthority.Canonical
	taskID string
	tid    domain.TaskID
}

func newLaunchStep(t *testing.T, c *taskauthority.Canonical, taskID string) launchStep {
	return launchStep{t: t, c: c, taskID: taskID, tid: mustFleetTaskID(t, taskID)}
}

func (s launchStep) current() taskauthority.Aggregate {
	s.t.Helper()
	agg, err := s.c.Get(s.tid)
	if err != nil {
		s.t.Fatal(err)
	}
	return agg
}

func (s launchStep) precondition() domain.Precondition {
	s.t.Helper()
	agg := s.current()
	return domain.Of(uint64(agg.Generation), uint64(agg.Revision))
}

func (s launchStep) opID(name string) string {
	return fmt.Sprintf("op-l2w-%s-%s-g%d", name, s.taskID, s.precondition().Generation)
}

func (s launchStep) must(name string, err error) {
	s.t.Helper()
	if err != nil {
		s.t.Fatalf("%s(%s): %v", name, s.taskID, err)
	}
}

// launchBindWorktree commits the launch intent for the task's current queued
// generation and binds wt under it. The intent reserves wt's lease and fence and
// ep's backend, lease, fence and incarnation, so the committed bindings carry
// exactly the fixture's values.
func launchBindWorktree(t *testing.T, c *taskauthority.Canonical, taskID string, wt taskauthority.WorktreeBinding, ep taskauthority.EndpointBinding) {
	t.Helper()
	s := newLaunchStep(t, c, taskID)
	if s.current().Launch == nil {
		launchBegin(s, wt, ep)
	}
	bw := taskauthority.CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: s.tid, Precondition: s.precondition(), Binding: wt, Reason: "spawn"}
	_, err := c.BindWorktree(mustFleetOperation(t, s.opID("bindwt"), bw), bw)
	s.must("BindWorktree", err)
}

func launchBegin(s launchStep, wt taskauthority.WorktreeBinding, ep taskauthority.EndpointBinding) {
	s.t.Helper()
	t, c, taskID := s.t, s.c, s.taskID
	def := s.current().Definition
	kind, project := def.Kind, def.Project
	if kind == "" {
		kind = taskauthority.KindShip
	}
	if project == "" {
		project = "project"
	}
	gen := s.precondition().Generation
	begin := taskauthority.CanonicalBeginSpawnRequest{
		HomeID: c.HomeID(), TaskID: s.tid, Precondition: s.precondition(), SnapshotDigest: strings.Repeat("a", 64),
		Backend: ep.Backend, Harness: "pi", Model: "opus", Effort: "high", Mode: "direct-PR", Kind: kind,
		Project: project, LaunchID: fmt.Sprintf("launch-%s-g%d", taskID, gen), WindowLabel: fmt.Sprintf("window-%s-g%d", taskID, gen),
		WorktreeReservationID: wt.LeaseID, WorktreeFenceToken: wt.FenceToken,
		EndpointReservationID: ep.LeaseID, EndpointFenceToken: ep.FenceToken, EndpointIncarnation: ep.Incarnation,
		SeatPathDirs: []string{"/usr/bin"}, SeatEnv: testSeatEnv(), Reason: "spawn",
	}
	_, err := c.BeginSpawn(mustFleetOperation(t, s.opID("begin"), begin), begin)
	s.must("BeginSpawn", err)
}

// recordLaunchManifestAnchor anchors digest as the launch manifest of the
// task's bound worktree under its committed intent.
func recordLaunchManifestAnchor(t *testing.T, c *taskauthority.Canonical, taskID, digest string) {
	t.Helper()
	s := newLaunchStep(t, c, taskID)
	l := s.current().Launch
	manifest := taskauthority.CanonicalRecordLaunchManifestRequest{
		HomeID: c.HomeID(), TaskID: s.tid, Precondition: s.precondition(), LaunchID: l.LaunchID,
		WorktreeLeaseID: l.WorktreeReservationID, WorktreeFenceToken: l.WorktreeFenceToken, ManifestSHA256: digest, Reason: "manifest prepared",
	}
	_, err := c.RecordLaunchManifest(mustFleetOperation(t, s.opID("manifest"), manifest), manifest)
	s.must("RecordLaunchManifest", err)
}

// launchBindEndpoint takes a task launchBindWorktree left queued through the
// manifest anchor, the acquired endpoint and the launch evidence, then binds ep
// under the intent, which makes the task working.
func launchBindEndpoint(t *testing.T, c *taskauthority.Canonical, taskID string, ep taskauthority.EndpointBinding) {
	t.Helper()
	s := newLaunchStep(t, c, taskID)
	agg := s.current()
	if agg.Launch == nil || agg.Worktree == nil {
		t.Fatalf("launchBindEndpoint(%s): no launch intent or worktree binding to launch from", taskID)
	}
	l := agg.Launch
	if agg.Worktree.LaunchManifest == nil {
		recordLaunchManifestAnchor(t, c, taskID, strings.Repeat("b", 64))
	}
	if agg.AcquiredEndpoint == nil {
		attach := taskauthority.CanonicalAttachEndpointRequest{
			HomeID: c.HomeID(), TaskID: s.tid, Precondition: s.precondition(), Backend: ep.Backend, Handle: ep.Handle,
			LeaseID: ep.LeaseID, FenceToken: ep.FenceToken, SessionOwner: ep.SessionOwner, WorkspaceID: ep.WorkspaceID,
			TabID: ep.TabID, Incarnation: ep.Incarnation, Reason: "attach",
		}
		_, err := c.AttachEndpoint(mustFleetOperation(t, s.opID("attach"), attach), attach)
		s.must("AttachEndpoint", err)
	}
	if agg.LaunchEvidence == nil {
		record := taskauthority.CanonicalRecordLaunchRequest{
			HomeID: c.HomeID(), TaskID: s.tid, Precondition: s.precondition(), LaunchID: l.LaunchID, CommandDigest: strings.Repeat("c", 64), Reason: "record",
			Seat: taskauthority.LaunchSeat{Argv: []string{"pi"}, PromptDigest: strings.Repeat("d", 64), Fence: taskauthority.FenceRecord{Reason: "no fence on this host"}},
		}
		_, err := c.RecordLaunch(mustFleetOperation(t, s.opID("record"), record), record)
		s.must("RecordLaunch", err)
	}
	bind := taskauthority.CanonicalBindEndpointRequest{HomeID: c.HomeID(), TaskID: s.tid, Precondition: s.precondition(), Binding: ep, Reason: "spawn"}
	_, err := c.BindEndpoint(mustFleetOperation(t, s.opID("bindep"), bind), bind)
	s.must("BindEndpoint", err)
}

// launchToWorking drives the task's current queued generation through the
// whole launch chain to working with exactly wt and ep bound.
func launchToWorking(t *testing.T, c *taskauthority.Canonical, taskID string, wt taskauthority.WorktreeBinding, ep taskauthority.EndpointBinding) {
	t.Helper()
	launchBindWorktree(t, c, taskID, wt, ep)
	launchBindEndpoint(t, c, taskID, ep)
}
