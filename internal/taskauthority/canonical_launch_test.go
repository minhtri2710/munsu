package taskauthority

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// launchRequest builds a valid BeginSpawn request for a task. The reservation
// identities are deterministic per task so bindings can be built from the same
// request.
func launchRequest(c *Canonical, taskID string, prec domain.Precondition) CanonicalBeginSpawnRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalBeginSpawnRequest{
		HomeID:                c.HomeID(),
		TaskID:                id,
		Precondition:          prec,
		SnapshotDigest:        digestOf("snapshot:" + taskID),
		Backend:               "claude",
		Harness:               "pi",
		Model:                 "opus",
		Effort:                "high",
		Mode:                  "direct-PR",
		Kind:                  "ship",
		Project:               "proj",
		ParentTaskID:          "parent",
		LaunchID:              "launch-" + taskID,
		WindowLabel:           "window-" + taskID,
		WorktreeReservationID: "wt-res-" + taskID,
		WorktreeFenceToken:    "wt-fence-" + taskID,
		EndpointReservationID: "ep-res-" + taskID,
		EndpointFenceToken:    "ep-fence-" + taskID,
		EndpointIncarnation:   "inc-" + taskID,
		Reason:                "spawn",
	}
}

// launchWorktreeBinding builds a worktree binding carrying the launch intent's
// reserved worktree lease/fence identities.
func launchWorktreeBinding(req CanonicalBeginSpawnRequest) WorktreeBinding {
	b := worktreeBinding()
	b.LeaseID = req.WorktreeReservationID
	b.FenceToken = req.WorktreeFenceToken
	return b
}

// launchEndpointBinding builds an endpoint binding carrying the launch
// intent's reserved endpoint lease/fence identities and the acquired handle.
func launchEndpointBinding(req CanonicalBeginSpawnRequest, handle string) EndpointBinding {
	b := endpointBinding()
	b.Backend = req.Backend
	b.Handle = handle
	b.LeaseID = req.EndpointReservationID
	b.FenceToken = req.EndpointFenceToken
	b.Incarnation = req.EndpointIncarnation
	return b
}

// attachRequest builds an AttachEndpoint request matching a launch intent's
// backend and endpoint reservation fence.
func attachRequest(c *Canonical, taskID string, prec domain.Precondition, req CanonicalBeginSpawnRequest, handle string) CanonicalAttachEndpointRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalAttachEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       id,
		Precondition: prec,
		Backend:      req.Backend,
		Handle:       handle,
		LeaseID:      req.EndpointReservationID,
		FenceToken:   req.EndpointFenceToken,
		SessionOwner: "owner",
		WorkspaceID:  "ws",
		TabID:        "tab",
		Incarnation:  req.EndpointIncarnation,
		Reason:       "attach",
	}
}

// recordLaunchRequest builds a RecordLaunch request matching a launch
// intent's deterministic launch identity.
func recordLaunchRequest(c *Canonical, taskID string, prec domain.Precondition, req CanonicalBeginSpawnRequest) CanonicalRecordLaunchRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalRecordLaunchRequest{
		HomeID:        c.HomeID(),
		TaskID:        id,
		Precondition:  prec,
		LaunchID:      req.LaunchID,
		CommandDigest: digestOf("launch:" + taskID),
		Seat:          testSeat(),
		Reason:        "record",
	}
}

// mustBeginSpawn commits a launch intent and returns the aggregate revision.
func mustBeginSpawn(t *testing.T, c *Canonical, taskID string, prec domain.Precondition) (CanonicalBeginSpawnRequest, uint64) {
	t.Helper()
	req := launchRequest(c, taskID, prec)
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-"+taskID, req), req); err != nil {
		t.Fatalf("BeginSpawn(%s): %v", taskID, err)
	}
	return req, uint64(prec.Revision) + 1
}

func TestCanonicalBeginSpawnCommitsLaunchIntent(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := launchRequest(c, "t1", preconditionOf(1, 1))
	out, err := c.BeginSpawn(mustOperation(t, "op-begin-1", req), req)
	if err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}
	if out.Revision != 2 || out.Phase != PhaseQueued {
		t.Fatalf("begin spawn outcome = %+v, want queued rev 2", out)
	}

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Launch == nil {
		t.Fatalf("launch intent missing after BeginSpawn")
	}
	if agg.Phase != PhaseQueued || agg.Revision != 2 {
		t.Fatalf("aggregate = phase %s rev %d, want queued/2", agg.Phase, agg.Revision)
	}
	// Pre-acquisition contract: the committed state carries the intent and no
	// acquired resource.
	if agg.Worktree != nil || agg.Endpoint != nil || agg.AcquiredEndpoint != nil || agg.LaunchEvidence != nil {
		t.Fatalf("pre-acquisition state carries acquired resources: worktree=%+v endpoint=%+v acquired=%+v evidence=%+v", agg.Worktree, agg.Endpoint, agg.AcquiredEndpoint, agg.LaunchEvidence)
	}
	l := agg.Launch
	if l.OperationID != "op-begin-1" {
		t.Fatalf("launch operation id = %q, want op-begin-1", l.OperationID)
	}
	if l.SnapshotDigest != req.SnapshotDigest || l.Backend != "claude" || l.Harness != "pi" {
		t.Fatalf("launch identity = %+v", l)
	}
	if l.Model != "opus" || l.Effort != "high" || l.Mode != "direct-PR" || l.Kind != "ship" || l.Project != "proj" || l.ParentTaskID != "parent" || l.WindowLabel != "window-t1" {
		t.Fatalf("launch optional identity = %+v", l)
	}
	if l.LaunchID != "launch-t1" || l.WorktreeReservationID != "wt-res-t1" || l.WorktreeFenceToken != "wt-fence-t1" || l.EndpointReservationID != "ep-res-t1" || l.EndpointFenceToken != "ep-fence-t1" {
		t.Fatalf("launch reservations = %+v", l)
	}
	if l.PlannedAt <= 0 {
		t.Fatalf("launch planned timestamp missing: %+v", l)
	}
}

func TestCanonicalBeginSpawnRejectsAcquiredBindings(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// A bound worktree proves resource acquisition already happened: the
	// durable intent must precede it, so BeginSpawn fails closed.
	bw := bindWorktreeRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-1", bw), bw); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	req := launchRequest(c, "t1", preconditionOf(1, 2))
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-after-wt", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginSpawn after worktree binding = %v, want ErrConflict", err)
	}

	// Same for an endpoint-bound (working) task.
	c2, _, _ := newTestCanonical(t)
	mustCreate(t, c2, "t2")
	bw2 := bindWorktreeRequest(c2, "t2", preconditionOf(1, 1))
	if _, err := c2.BindWorktree(mustOperation(t, "op-wt-2", bw2), bw2); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	be2 := bindEndpointRequest(c2, "t2", preconditionOf(1, 2))
	if _, err := c2.BindEndpoint(mustOperation(t, "op-be-2", be2), be2); err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}
	req2 := launchRequest(c2, "t2", preconditionOf(1, 3))
	if _, err := c2.BeginSpawn(mustOperation(t, "op-begin-after-ep", req2), req2); !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginSpawn after endpoint binding = %v, want ErrConflict", err)
	}
}

func TestCanonicalBeginSpawnRequiresQueuedPhase(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	start := startWithRev(c, "t1", 1)
	if _, err := c.Start(mustOperation(t, "op-start-1", start), start); err != nil {
		t.Fatalf("Start: %v", err)
	}
	req := launchRequest(c, "t1", preconditionOf(1, 2))
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-working", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginSpawn on working task = %v, want ErrConflict", err)
	}
}

func TestCanonicalBeginSpawnSameOperationReplays(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := launchRequest(c, "t1", preconditionOf(1, 1))
	op := mustOperation(t, "op-begin-replay", req)
	first, err := c.BeginSpawn(op, req)
	if err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}
	second, err := c.BeginSpawn(op, req)
	if err != nil {
		t.Fatalf("replay BeginSpawn: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
	if second.Revision != first.Revision || second.Phase != first.Phase {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}
}

func TestCanonicalBeginSpawnChangedDigestConflicts(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := launchRequest(c, "t1", preconditionOf(1, 1))
	op := mustOperation(t, "op-shared-begin", req)
	if _, err := c.BeginSpawn(op, req); err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}

	diff := launchRequest(c, "t1", preconditionOf(1, 1))
	diff.LaunchID = "launch-different"
	reused, err := domain.NewOperation(op.ID, diff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.BeginSpawn(reused, diff); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

func TestCanonicalBeginSpawnSecondDistinctIntentConflicts(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := launchRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-1", req), req); err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}

	// A second distinct intent for the same generation conflicts: a different
	// backend with a different reservation fence is not the same launch.
	diff := launchRequest(c, "t1", preconditionOf(1, 2))
	diff.Backend = "pi"
	diff.EndpointReservationID = "ep-res-other"
	diff.EndpointFenceToken = "ep-fence-other"
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-2", diff), diff); !errors.Is(err, ErrConflict) {
		t.Fatalf("second distinct launch intent = %v, want ErrConflict", err)
	}
}

func TestCanonicalBeginSpawnIdenticalIntentRecommitsAsNoOp(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := launchRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-1", req), req); err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}
	// Same immutable launch identity under a fresh Operation ID and the current
	// revision is a no-op: one intent remains, the revision does not advance.
	again := req
	again.Precondition = preconditionOf(1, 2)
	out, err := c.BeginSpawn(mustOperation(t, "op-begin-1-again", again), again)
	if err != nil {
		t.Fatalf("identical intent recommit = %v, want no-op success", err)
	}
	if out.Replayed {
		t.Fatalf("fresh operation marked replayed: %+v", out)
	}
	if out.Revision != 2 {
		t.Fatalf("identical recommit advanced revision to %d, want 2", out.Revision)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 || agg.Launch == nil {
		t.Fatalf("aggregate after identical recommit = %+v", agg)
	}
}

func TestCanonicalBeginSpawnStaleGenerationConflicts(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := launchRequest(c, "t1", preconditionOf(1, 9))
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-stale", req), req); !errors.Is(err, domain.ErrStalePrecondition) {
		t.Fatalf("stale begin spawn = %v, want domain.ErrStalePrecondition", err)
	}
}

func TestCanonicalBeginSpawnBlockedBySpawnHold(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	hold := CanonicalAddHoldRequest{
		HomeID:  c.HomeID(),
		HoldID:  "spawn-hold",
		Scope:   DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionSpawn},
		Reason:  "freeze spawn",
	}
	if _, err := c.AddHold(mustOperation(t, "op-hold-spawn", hold), hold); err != nil {
		t.Fatalf("AddHold: %v", err)
	}

	req := launchRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-held", req), req); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("BeginSpawn held = %v, want ErrDispatchHeld", err)
	}

	release := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "spawn-hold", Reason: "resume", Words: testWords()}
	if _, err := c.ReleaseHold(mustOperation(t, "op-release-spawn", release), release); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-after-hold", req), req); err != nil {
		t.Fatalf("BeginSpawn after release: %v", err)
	}
}

func TestCanonicalBeginSpawnValidation(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := launchRequest(c, "t1", preconditionOf(1, 1))
	req.SnapshotDigest = "not-a-digest"
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-bad-digest", req), req); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad snapshot digest = %v, want ErrInvalidInput", err)
	}

	req = launchRequest(c, "t1", preconditionOf(1, 1))
	req.WorktreeFenceToken = "bad/token"
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-bad-fence", req), req); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe worktree fence token = %v, want ErrInvalidInput", err)
	}

	req = launchRequest(c, "t1", preconditionOf(1, 1))
	req.Backend = ""
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-no-backend", req), req); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing backend = %v, want ErrInvalidInput", err)
	}
}

func TestCanonicalAttachEndpointRecordsAcquiredEndpoint(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	out, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach)
	if err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	if out.Revision != Revision(rev+1) || out.Phase != PhaseQueued {
		t.Fatalf("attach outcome = %+v, want queued rev %d", out, rev+1)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.AcquiredEndpoint == nil {
		t.Fatalf("acquired endpoint missing after AttachEndpoint")
	}
	if agg.Phase != PhaseQueued || agg.Endpoint != nil {
		t.Fatalf("attach must not transition or bind: phase %s endpoint %+v", agg.Phase, agg.Endpoint)
	}
	a := agg.AcquiredEndpoint
	if a.Backend != "claude" || a.Handle != "handle-1" || a.LeaseID != "ep-res-t1" || a.FenceToken != "ep-fence-t1" {
		t.Fatalf("acquired endpoint = %+v", a)
	}
	if a.OperationID != "op-attach-1" || a.AcquiredAt <= 0 {
		t.Fatalf("acquired endpoint metadata = %+v", a)
	}
}

func TestCanonicalAttachEndpointRequiresLaunchIntent(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := launchRequest(c, "t1", preconditionOf(1, 1))
	attach := attachRequest(c, "t1", preconditionOf(1, 1), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-no-intent", attach), attach); !errors.Is(err, ErrConflict) {
		t.Fatalf("attach without launch intent = %v, want ErrConflict", err)
	}
}

func TestCanonicalAttachEndpointFenceMismatch(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	// Wrong lease/fence: not the reserved endpoint.
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	attach.LeaseID = "ep-res-other"
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-bad-fence", attach), attach); !errors.Is(err, ErrConflict) {
		t.Fatalf("attach with wrong endpoint fence = %v, want ErrConflict", err)
	}

	// Wrong backend: not the intent's explicit backend.
	attach = attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	attach.Backend = "pi"
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-bad-backend", attach), attach); !errors.Is(err, ErrConflict) {
		t.Fatalf("attach with wrong backend = %v, want ErrConflict", err)
	}
}

func TestCanonicalAttachEndpointDifferentRecordConflicts(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	// A different acquired endpoint identity (different handle) cannot
	// overwrite the committed record.
	diff := attachRequest(c, "t1", preconditionOf(1, rev+1), req, "handle-2")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-2", diff), diff); !errors.Is(err, ErrConflict) {
		t.Fatalf("different acquired endpoint = %v, want ErrConflict", err)
	}
}

func TestCanonicalAttachEndpointIdenticalReattachIsNoOp(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	// Same acquired endpoint identity under a fresh Operation ID and the
	// current revision is a no-op: the record is unchanged.
	again := attach
	again.Precondition = preconditionOf(1, rev+1)
	out, err := c.AttachEndpoint(mustOperation(t, "op-attach-1-again", again), again)
	if err != nil {
		t.Fatalf("identical reattach = %v, want no-op success", err)
	}
	if out.Replayed || out.Revision != Revision(rev+1) {
		t.Fatalf("identical reattach outcome = %+v, want fresh rev %d", out, rev+1)
	}
}

func TestCanonicalAttachEndpointReplay(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	op := mustOperation(t, "op-attach-replay", attach)
	first, err := c.AttachEndpoint(op, attach)
	if err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	second, err := c.AttachEndpoint(op, attach)
	if err != nil {
		t.Fatalf("replay AttachEndpoint: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
	if second.Revision != first.Revision {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}
}

func TestCanonicalAttachEndpointOperationReusedConflict(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	op := mustOperation(t, "op-shared-attach", attach)
	if _, err := c.AttachEndpoint(op, attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	diff := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-2")
	reused, err := domain.NewOperation(op.ID, diff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AttachEndpoint(reused, diff); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

func TestCanonicalRecordLaunchRecordsEvidence(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	_, rev = bindAndRecordLaunchManifest(t, c, "t1", req, rev, "records-evidence")
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}

	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev+1), req)
	out, err := c.RecordLaunch(mustOperation(t, "op-record-1", record), record)
	if err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	if out.Revision != Revision(rev+2) || out.Phase != PhaseQueued {
		t.Fatalf("record outcome = %+v, want queued rev %d", out, rev+2)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.LaunchEvidence == nil {
		t.Fatalf("launch evidence missing after RecordLaunch")
	}
	if agg.Phase != PhaseQueued || agg.Endpoint != nil {
		t.Fatalf("record must not transition or bind: phase %s endpoint %+v", agg.Phase, agg.Endpoint)
	}
	e := agg.LaunchEvidence
	if e.LaunchID != "launch-t1" || e.CommandDigest != digestOf("launch:t1") {
		t.Fatalf("launch evidence = %+v", e)
	}
	if e.OperationID != "op-record-1" || e.SubmittedAt <= 0 {
		t.Fatalf("launch evidence metadata = %+v", e)
	}
}

func recordLaunchManifest(t *testing.T, c *Canonical, taskID string, prec domain.Precondition, intent CanonicalBeginSpawnRequest, binding WorktreeBinding, opID string) Outcome {
	t.Helper()
	req := CanonicalRecordLaunchManifestRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: prec,
		LaunchID: intent.LaunchID, WorktreeLeaseID: binding.LeaseID,
		WorktreeFenceToken: binding.FenceToken, ManifestSHA256: digestOf("manifest:" + taskID), Reason: "manifest prepared",
	}
	out, err := c.RecordLaunchManifest(mustOperation(t, opID, req), req)
	if err != nil {
		t.Fatalf("RecordLaunchManifest: %v", err)
	}
	return out
}

func mustRecordLaunchManifest(t *testing.T, c *Canonical, taskID string, intent CanonicalBeginSpawnRequest, binding WorktreeBinding, revision uint64) uint64 {
	t.Helper()
	recordLaunchManifest(t, c, taskID, preconditionOf(1, revision), intent, binding, "op-manifest-"+intent.LaunchID)
	return revision + 1
}

func bindAndRecordLaunchManifest(t *testing.T, c *Canonical, taskID string, intent CanonicalBeginSpawnRequest, revision uint64, opSuffix string) (WorktreeBinding, uint64) {
	t.Helper()
	binding := launchWorktreeBinding(intent)
	bind := CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: preconditionOf(1, revision), Binding: binding, Reason: "bind worktree"}
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-manifest-"+opSuffix, bind), bind); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	revision++
	req := CanonicalRecordLaunchManifestRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: preconditionOf(1, revision),
		LaunchID: intent.LaunchID, WorktreeLeaseID: binding.LeaseID, WorktreeFenceToken: binding.FenceToken,
		ManifestSHA256: digestOf("manifest:" + taskID), Reason: "manifest prepared",
	}
	if _, err := c.RecordLaunchManifest(mustOperation(t, "op-manifest-"+opSuffix, req), req); err != nil {
		t.Fatalf("RecordLaunchManifest: %v", err)
	}
	return binding, revision + 1
}

func TestCanonicalRecordLaunchManifestCommitsEvidence(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")
	intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	binding := launchWorktreeBinding(intent)
	bind := CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev), Binding: binding, Reason: "bind"}
	if _, err := c.BindWorktree(mustOperation(t, "op-bind-manifest", bind), bind); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	rev++
	first := recordLaunchManifest(t, c, "t1", preconditionOf(1, rev), intent, binding, "op-record-manifest")
	if first.Revision != Revision(rev+1) || first.Phase != PhaseQueued {
		t.Fatalf("RecordLaunchManifest outcome = %+v", first)
	}
	req := CanonicalRecordLaunchManifestRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev),
		LaunchID: intent.LaunchID, WorktreeLeaseID: binding.LeaseID, WorktreeFenceToken: binding.FenceToken,
		ManifestSHA256: digestOf("manifest:t1"), Reason: "manifest prepared",
	}
	op := mustOperation(t, "op-record-manifest", req)
	replayed, err := c.RecordLaunchManifest(op, req)
	if err != nil || !replayed.Replayed || replayed.Revision != first.Revision {
		t.Fatalf("RecordLaunchManifest replay = %+v, %v", replayed, err)
	}
	changed := req
	changed.ManifestSHA256 = digestOf("different manifest")
	reused, err := domain.NewOperation(op.ID, changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RecordLaunchManifest(reused, changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed operation digest = %v, want ErrOperationConflict", err)
	}

	c2 := reopenCanonical(t, root)
	agg, err := c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Worktree == nil || agg.Worktree.LaunchManifest == nil || agg.Worktree.LaunchManifest.ManifestSHA256 != digestOf("manifest:t1") {
		t.Fatalf("canonical manifest evidence = %+v", agg.Worktree)
	}

	retire := retireRequest(t, c2, "t1", preconditionOf(1, uint64(agg.Revision)))
	if _, err := c2.Retire(mustOperation(t, "op-retire-manifest", retire), retire); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	agg, err = c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Retirement == nil || agg.Retirement.Worktree == nil || agg.Retirement.Worktree.LaunchManifest == nil || agg.Retirement.Worktree.LaunchManifest.ManifestSHA256 != digestOf("manifest:t1") {
		t.Fatalf("retirement did not preserve exact manifest evidence: %+v", agg.Retirement)
	}
}

func TestValidateRecordLaunchManifestRequestRefusesUnusableEvidence(t *testing.T) {
	valid := func() CanonicalRecordLaunchManifestRequest {
		return CanonicalRecordLaunchManifestRequest{
			TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1),
			LaunchID: "launch-1", WorktreeLeaseID: "wt-res-1", WorktreeFenceToken: "wt-fence-1",
			ManifestSHA256: testSHA256Hex,
		}
	}
	runGuardCases(t, valid, validateRecordLaunchManifestRequest, []guardCase[CanonicalRecordLaunchManifestRequest]{
		{"no launch identity", func(r *CanonicalRecordLaunchManifestRequest) { r.LaunchID = "" }, "requires a deterministic launch identity"},
		{"path-separating launch identity", func(r *CanonicalRecordLaunchManifestRequest) { r.LaunchID = "launch/1" }, "requires a deterministic launch identity"},
		{"no worktree lease identity", func(r *CanonicalRecordLaunchManifestRequest) { r.WorktreeLeaseID = " " }, "requires the worktree lease identity"},
		{"path-separating worktree lease identity", func(r *CanonicalRecordLaunchManifestRequest) { r.WorktreeLeaseID = `wt\\res` }, "requires the worktree lease identity"},
		{"no worktree fence identity", func(r *CanonicalRecordLaunchManifestRequest) { r.WorktreeFenceToken = " " }, "requires the worktree fence identity"},
		{"path-separating worktree fence identity", func(r *CanonicalRecordLaunchManifestRequest) { r.WorktreeFenceToken = "wt/fence" }, "requires the worktree fence identity"},
		{"digest is not a sha256", func(r *CanonicalRecordLaunchManifestRequest) { r.ManifestSHA256 = "not-a-digest" }, "digest must be a 64-hex sha256 digest"},
	})
}

func TestCanonicalRecordLaunchManifestRefusesUnmatchedCanonicalState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Canonical) (CanonicalRecordLaunchManifestRequest, string)
		want  string
	}{
		{
			name: "review task",
			setup: func(t *testing.T, c *Canonical) (CanonicalRecordLaunchManifestRequest, string) {
				workingShip(t, c, "ship-target")
				if err := createReview(t, c, "review-manifest", "ship-target", "head"); err != nil {
					t.Fatalf("create review: %v", err)
				}
				return CanonicalRecordLaunchManifestRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "review-manifest"), Precondition: preconditionOf(1, 1),
					LaunchID: "launch-review", WorktreeLeaseID: "wt-res-review", WorktreeFenceToken: "wt-fence-review",
					ManifestSHA256: testSHA256Hex,
				}, "op-manifest-review"
			},
			want: "is a review task",
		},
		{
			name: "not queued",
			setup: func(t *testing.T, c *Canonical) (CanonicalRecordLaunchManifestRequest, string) {
				mustCreate(t, c, "t1")
				start := startWithRev(c, "t1", 1)
				if _, err := c.Start(mustOperation(t, "op-start-manifest", start), start); err != nil {
					t.Fatalf("Start: %v", err)
				}
				return CanonicalRecordLaunchManifestRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 2),
					LaunchID: "launch-t1", WorktreeLeaseID: "wt-res-t1", WorktreeFenceToken: "wt-fence-t1",
					ManifestSHA256: testSHA256Hex,
				}, "op-manifest-phase"
			},
			want: "record launch manifest requires queued",
		},
		{
			name: "no launch intent",
			setup: func(t *testing.T, c *Canonical) (CanonicalRecordLaunchManifestRequest, string) {
				mustCreate(t, c, "t1")
				return CanonicalRecordLaunchManifestRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1),
					LaunchID: "launch-t1", WorktreeLeaseID: "wt-res-t1", WorktreeFenceToken: "wt-fence-t1",
					ManifestSHA256: testSHA256Hex,
				}, "op-manifest-no-intent"
			},
			want: "has no committed launch intent",
		},
		{
			name: "no bound worktree",
			setup: func(t *testing.T, c *Canonical) (CanonicalRecordLaunchManifestRequest, string) {
				mustCreate(t, c, "t1")
				intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
				return CanonicalRecordLaunchManifestRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev),
					LaunchID: intent.LaunchID, WorktreeLeaseID: intent.WorktreeReservationID, WorktreeFenceToken: intent.WorktreeFenceToken,
					ManifestSHA256: testSHA256Hex,
				}, "op-manifest-no-worktree"
			},
			want: "has no bound worktree",
		},
		{
			name: "different launch identity",
			setup: func(t *testing.T, c *Canonical) (CanonicalRecordLaunchManifestRequest, string) {
				mustCreate(t, c, "t1")
				intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
				binding := launchWorktreeBinding(intent)
				bind := CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev), Binding: binding, Reason: "bind"}
				if _, err := c.BindWorktree(mustOperation(t, "op-bind-manifest-mismatch", bind), bind); err != nil {
					t.Fatalf("BindWorktree: %v", err)
				}
				req := CanonicalRecordLaunchManifestRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev+1),
					LaunchID: "another-launch", WorktreeLeaseID: binding.LeaseID, WorktreeFenceToken: binding.FenceToken,
					ManifestSHA256: testSHA256Hex,
				}
				return req, "op-manifest-launch-mismatch"
			},
			want: "does not match launch intent",
		},
		{
			name: "different worktree fence",
			setup: func(t *testing.T, c *Canonical) (CanonicalRecordLaunchManifestRequest, string) {
				mustCreate(t, c, "t1")
				intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
				binding := launchWorktreeBinding(intent)
				bind := CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev), Binding: binding, Reason: "bind"}
				if _, err := c.BindWorktree(mustOperation(t, "op-bind-manifest-fence", bind), bind); err != nil {
					t.Fatalf("BindWorktree: %v", err)
				}
				req := CanonicalRecordLaunchManifestRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev+1),
					LaunchID: intent.LaunchID, WorktreeLeaseID: binding.LeaseID, WorktreeFenceToken: "foreign-fence",
					ManifestSHA256: testSHA256Hex,
				}
				return req, "op-manifest-fence-mismatch"
			},
			want: "does not match the bound worktree lease/fence",
		},
		{
			name: "wrong generation",
			setup: func(t *testing.T, c *Canonical) (CanonicalRecordLaunchManifestRequest, string) {
				mustCreate(t, c, "t1")
				intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
				binding := launchWorktreeBinding(intent)
				bind := CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev), Binding: binding, Reason: "bind"}
				if _, err := c.BindWorktree(mustOperation(t, "op-bind-manifest-generation", bind), bind); err != nil {
					t.Fatalf("BindWorktree: %v", err)
				}
				req := CanonicalRecordLaunchManifestRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(2, rev+1),
					LaunchID: intent.LaunchID, WorktreeLeaseID: binding.LeaseID, WorktreeFenceToken: binding.FenceToken,
					ManifestSHA256: testSHA256Hex,
				}
				return req, "op-manifest-wrong-generation"
			},
			want: "stale precondition",
		},
		{
			name: "different existing evidence",
			setup: func(t *testing.T, c *Canonical) (CanonicalRecordLaunchManifestRequest, string) {
				mustCreate(t, c, "t1")
				intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
				binding := launchWorktreeBinding(intent)
				bind := CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev), Binding: binding, Reason: "bind"}
				if _, err := c.BindWorktree(mustOperation(t, "op-bind-manifest-existing", bind), bind); err != nil {
					t.Fatalf("BindWorktree: %v", err)
				}
				rev++
				first := CanonicalRecordLaunchManifestRequest{
					HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev),
					LaunchID: intent.LaunchID, WorktreeLeaseID: binding.LeaseID, WorktreeFenceToken: binding.FenceToken,
					ManifestSHA256: testSHA256Hex,
				}
				if _, err := c.RecordLaunchManifest(mustOperation(t, "op-manifest-existing", first), first); err != nil {
					t.Fatalf("RecordLaunchManifest first: %v", err)
				}
				first.Precondition = preconditionOf(1, rev+1)
				first.ManifestSHA256 = digestOf("different manifest")
				return first, "op-manifest-existing-different"
			},
			want: "already records different launch manifest evidence",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newTestCanonical(t)
			req, opID := tc.setup(t, c)
			if _, err := c.RecordLaunchManifest(mustOperation(t, opID, req), req); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RecordLaunchManifest error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCanonicalBindWorktreeCannotInjectManifestEvidence(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	binding := launchWorktreeBinding(intent)
	binding.LaunchManifest = &LaunchManifestEvidence{
		OperationID: "invented", LaunchID: intent.LaunchID, ManifestSHA256: digestOf("fabricated"), RecordedAt: 1,
	}
	req := CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev), Binding: binding, Reason: "bind"}
	if _, err := c.BindWorktree(mustOperation(t, "op-bind-injected-manifest", req), req); err == nil || !strings.Contains(err.Error(), "use RecordLaunchManifest") {
		t.Fatalf("BindWorktree with caller-supplied manifest evidence = %v, want single-writer refusal", err)
	}
}

func TestCanonicalRecordLaunchRequiresManifestAnchor(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	intent, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	worktree := CanonicalBindWorktreeRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev),
		Binding: launchWorktreeBinding(intent), Reason: "bind worktree",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-manifest-required", worktree), worktree); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	rev++
	attach := attachRequest(c, "t1", preconditionOf(1, rev), intent, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-manifest-required", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	rev++

	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev), intent)
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-manifest-required", record), record); !errors.Is(err, ErrConflict) {
		t.Fatalf("RecordLaunch without canonical manifest anchor = %v, want ErrConflict", err)
	}
}

func TestCanonicalRecordLaunchRequiresAcquiredEndpoint(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev), req)
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-no-endpoint", record), record); !errors.Is(err, ErrConflict) {
		t.Fatalf("record launch without acquired endpoint = %v, want ErrConflict", err)
	}
}

func TestCanonicalRecordLaunchLaunchIDMismatch(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	_, rev = bindAndRecordLaunchManifest(t, c, "t1", req, rev, "launch-id-mismatch")
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}

	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev+1), req)
	record.LaunchID = "launch-other"
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-bad-launch", record), record); !errors.Is(err, ErrConflict) {
		t.Fatalf("record launch with mismatched launch identity = %v, want ErrConflict", err)
	}
}

func TestCanonicalRecordLaunchDifferentEvidenceConflicts(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	_, rev = bindAndRecordLaunchManifest(t, c, "t1", req, rev, "different-evidence")
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}

	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev+1), req)
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-1", record), record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	diff := recordLaunchRequest(c, "t1", preconditionOf(1, rev+2), req)
	diff.CommandDigest = digestOf("launch:other")
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-2", diff), diff); !errors.Is(err, ErrConflict) {
		t.Fatalf("different launch evidence = %v, want ErrConflict", err)
	}
}

func TestCanonicalRecordLaunchReplay(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	_, rev = bindAndRecordLaunchManifest(t, c, "t1", req, rev, "record-replay")
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}

	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev+1), req)
	op := mustOperation(t, "op-record-replay", record)
	first, err := c.RecordLaunch(op, record)
	if err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	second, err := c.RecordLaunch(op, record)
	if err != nil {
		t.Fatalf("replay RecordLaunch: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
	if second.Revision != first.Revision {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}
}

func TestCanonicalRecordLaunchOperationReusedConflict(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	_, rev = bindAndRecordLaunchManifest(t, c, "t1", req, rev, "record-reused-conflict")
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}

	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev+1), req)
	op := mustOperation(t, "op-shared-record", record)
	if _, err := c.RecordLaunch(op, record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	diff := recordLaunchRequest(c, "t1", preconditionOf(1, rev+1), req)
	diff.CommandDigest = digestOf("launch:other")
	reused, err := domain.NewOperation(op.ID, diff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RecordLaunch(reused, diff); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

// TestCanonicalLaunchIncarnationPersistsAndFencesBinds asserts the opaque
// incarnation minted by Fleet is persisted on the acquired endpoint and that
// BindEndpoint requires the EXACT incarnation of the acquired record — a
// mismatched (stale/foreign) incarnation never binds (BEO-16/P1a).
func TestCanonicalLaunchIncarnationPersistsAndFencesBinds(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	// Bind the worktree.
	bw := CanonicalBindWorktreeRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, rev),
		Binding: WorktreeBinding{
			RepositoryIdentity: "repo", Path: "/wt", GitDir: "/wt/.git", CommonDir: "/wt/.git",
			BaseHead: "sha", LeaseID: req.WorktreeReservationID, FenceToken: req.WorktreeFenceToken, BoundAtUnix: 2000,
		},
		Reason: "spawn",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-1", bw), bw); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	rev++
	rev = mustRecordLaunchManifest(t, c, "t1", req, bw.Binding, rev)

	// Acquire the endpoint with the opaque incarnation.
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	attach.Incarnation = req.EndpointIncarnation
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}

	agg0, _ := c.Get(mustTaskID(t, "t1"))
	if agg0.AcquiredEndpoint == nil || agg0.AcquiredEndpoint.Incarnation != req.EndpointIncarnation {
		t.Fatalf("acquired endpoint incarnation not persisted: %+v", agg0.AcquiredEndpoint)
	}

	// A different operation with a different (stale/foreign) incarnation must
	// conflict on the same acquired endpoint.
	foreign := attachRequest(c, "t1", preconditionOf(1, rev+1), req, "handle-1")
	foreign.Incarnation = "inc-foreign"
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-foreign", foreign), foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("attach with different incarnation = %v, want ErrConflict", err)
	}

	// Record launch evidence, then bind the endpoint.
	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev+1), req)
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-1", record), record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	be := bindEndpointRequest(c, "t1", preconditionOf(1, rev+2))
	be.Binding.Handle = "handle-1"
	be.Binding.LeaseID = req.EndpointReservationID
	be.Binding.FenceToken = req.EndpointFenceToken
	be.Binding.SessionOwner = "owner"
	be.Binding.WorkspaceID = "ws"
	be.Binding.TabID = "tab"
	// Wrong incarnation: must NOT bind (stale/foreign identity).
	be.Binding.Incarnation = "inc-foreign"
	if _, err := c.BindEndpoint(mustOperation(t, "op-be-foreign", be), be); !errors.Is(err, ErrConflict) {
		t.Fatalf("bind with mismatched incarnation = %v, want ErrConflict", err)
	}
	// Correct incarnation binds.
	be.Binding.Incarnation = req.EndpointIncarnation
	out, err := c.BindEndpoint(mustOperation(t, "op-be-1", be), be)
	if err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}
	if out.Phase != PhaseWorking {
		t.Fatalf("bind outcome phase = %s, want working", out.Phase)
	}

	agg, _ := c.Get(mustTaskID(t, "t1"))
	if agg.Endpoint == nil || agg.Endpoint.Incarnation != req.EndpointIncarnation {
		t.Fatalf("endpoint binding incarnation = %+v", agg.Endpoint)
	}
}

// — BeginSpawn, BindWorktree, AttachEndpoint, RecordLaunch, BindEndpoint — and
// proves the committed outcome: the phase transitions only at the final
// BindEndpoint, and the active bindings carry the exact reservation identities
// the launch intent reserved before acquisition.
func TestCanonicalLaunchFlowComposesToWorking(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))

	// Bind the worktree under the reserved worktree lease/fence.
	bw := CanonicalBindWorktreeRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev),
		Binding:      launchWorktreeBinding(req),
		Reason:       "bind worktree",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-launch", bw), bw); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	rev++
	rev = mustRecordLaunchManifest(t, c, "t1", req, bw.Binding, rev)

	// Record the acquired endpoint identity.
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-launch", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	rev++

	// Record the successful launch submission evidence.
	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev), req)
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-launch", record), record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	rev++

	// Bind the acquired endpoint into working under the reserved endpoint
	// lease/fence.
	be := CanonicalBindEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, rev),
		Binding:      launchEndpointBinding(req, "handle-1"),
		Reason:       "spawn",
	}
	out, err := c.BindEndpoint(mustOperation(t, "op-be-launch", be), be)
	if err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}
	if out.Phase != PhaseWorking || out.Revision != Revision(rev+1) {
		t.Fatalf("bind endpoint outcome = %+v, want working rev %d", out, rev+1)
	}

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseWorking || agg.Worktree == nil || agg.Endpoint == nil {
		t.Fatalf("final aggregate = phase %s worktree %+v endpoint %+v", agg.Phase, agg.Worktree, agg.Endpoint)
	}
	if agg.Worktree.LeaseID != req.WorktreeReservationID || agg.Worktree.FenceToken != req.WorktreeFenceToken {
		t.Fatalf("final worktree binding does not carry the intent-owned fence: %+v", agg.Worktree)
	}
	if agg.Endpoint.LeaseID != req.EndpointReservationID || agg.Endpoint.FenceToken != req.EndpointFenceToken {
		t.Fatalf("final endpoint binding does not carry the intent-owned fence: %+v", agg.Endpoint)
	}
	if agg.AcquiredEndpoint == nil || agg.LaunchEvidence == nil {
		t.Fatalf("launch records lost after working: acquired %+v evidence %+v", agg.AcquiredEndpoint, agg.LaunchEvidence)
	}
}

// TestPremiseGetRefusesLaunchEvidenceWithNoSubmissionTimestamp pins the READ
// half of the premise waiving internal/cli/report_cmd.go's
// `agg.LaunchEvidence.SubmittedAt <= 0` branch. The writer-side test
// (TestCanonicalRecordLaunchRecordsEvidence) pins only that RecordLaunch
// stamps the field; it stays green if a second writer appears or if the read
// path stops validating. What actually keeps the CLI branch unreachable is
// that readTaskDoc runs validateAggregate and fails closed, so a record
// carrying SubmittedAt = 0 is never served to a caller no matter who wrote it.
// This test plants exactly that record under the aggregate on disk and
// asserts Get refuses it; it goes red the moment the read path stops
// validating or validateLaunchEvidence stops checking SubmittedAt.
func TestPremiseGetRefusesLaunchEvidenceWithNoSubmissionTimestamp(t *testing.T) {
	c, h, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	_, rev = bindAndRecordLaunchManifest(t, c, "t1", req, rev, "premise-timestamp")
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev+1), req)
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-1", record), record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}

	// Rewrite the committed document with the one field zeroed, bypassing the
	// commit path entirely: this is the state a second writer -- or a
	// caller-supplied timestamp -- would leave behind.
	path := mustPathForTest(t, h, taskCurrentKey("t1"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc taskDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Aggregate.LaunchEvidence == nil || doc.Aggregate.LaunchEvidence.SubmittedAt <= 0 {
		t.Fatalf("committed document carries no stamped launch evidence: %+v", doc.Aggregate.LaunchEvidence)
	}
	doc.Aggregate.LaunchEvidence.SubmittedAt = 0
	planted, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, planted, 0600); err != nil {
		t.Fatal(err)
	}

	_, err = c.Get(mustTaskID(t, "t1"))
	if err == nil {
		t.Fatalf("Get on launch evidence with SubmittedAt = 0 = nil error, want fail closed")
	}
	// The message must be the launch-evidence validator's own: any other
	// refusal would mean the record was rejected for an unrelated reason and
	// this test would prove nothing about the timestamp check.
	if !strings.Contains(err.Error(), "launch evidence missing submission timestamp") {
		t.Fatalf("Get refused for the wrong reason: %v", err)
	}
}

// committedLaunchRecord commits launch evidence for task t1 through RecordLaunch
// under operation op-record-1 and returns the handle with the committed revision.
func committedLaunchRecord(t *testing.T) (*Canonical, *home.Home, uint64) {
	t.Helper()
	c, h, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	req, rev := mustBeginSpawn(t, c, "t1", preconditionOf(1, 1))
	_, rev = bindAndRecordLaunchManifest(t, c, "t1", req, rev, "receipt-query")
	attach := attachRequest(c, "t1", preconditionOf(1, rev), req, "handle-1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-1", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev+1), req)
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-1", record), record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	return c, h, rev + 2
}

// The receipt query proves committed identity only: it reports the exact
// operation as committed for this task and generation, and it never changes the
// aggregate or its evidence.
func TestCommittedOperationReceiptProvesCommittedIdentity(t *testing.T) {
	c, _, rev := committedLaunchRecord(t)
	taskID := mustTaskID(t, "t1")
	found, err := c.CommittedOperationReceipt("op-record-1", taskID, 1)
	if err != nil || !found {
		t.Fatalf("committed RecordLaunch receipt = %v, %v; want found", found, err)
	}
	found, err = c.CommittedOperationReceipt("op-record-absent", taskID, 1)
	if err != nil || found {
		t.Fatalf("absent operation receipt = %v, %v; want not found without error", found, err)
	}
	agg, err := c.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(agg.Revision) != rev || agg.LaunchEvidence == nil {
		t.Fatalf("query changed the aggregate: revision %d want %d, evidence %+v", agg.Revision, rev, agg.LaunchEvidence)
	}
}

// A stored receipt that does not name the exact operation, task and generation
// the caller asked about, or that cannot be decoded, is an error. It is never
// reported as absent, which would let a caller resubmit.
func TestCommittedOperationReceiptRefusesMismatchedReceipt(t *testing.T) {
	c, h, _ := committedLaunchRecord(t)
	path := mustPathForTest(t, h, receiptKey("op-record-1"))
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec receipt
	if err := json.Unmarshal(stored, &rec); err != nil {
		t.Fatal(err)
	}
	rec.OperationID = "op-other"
	renamed, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		opID   string
		taskID string
		gen    Generation
		stored []byte
	}{
		{name: "other task", opID: "op-record-1", taskID: "t2", gen: 1},
		{name: "other generation", opID: "op-record-1", taskID: "t1", gen: 2},
		{name: "receipt names another operation", opID: "op-record-1", taskID: "t1", gen: 1, stored: renamed},
		{name: "malformed receipt", opID: "op-record-1", taskID: "t1", gen: 1, stored: []byte("{not json")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := stored
			if tc.stored != nil {
				data = tc.stored
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			found, err := c.CommittedOperationReceipt(tc.opID, mustTaskID(t, tc.taskID), tc.gen)
			if err == nil || found {
				t.Fatalf("CommittedOperationReceipt = %v, %v; want a fail-closed error", found, err)
			}
		})
	}
}

// Invalid identities fail before any receipt is read, so a bad input is never
// reported as an absent receipt.
func TestCommittedOperationReceiptRejectsInvalidIdentity(t *testing.T) {
	c, _, _ := committedLaunchRecord(t)
	t1 := mustTaskID(t, "t1")
	for _, tc := range []struct {
		name   string
		opID   string
		taskID domain.TaskID
		gen    Generation
		want   error
	}{
		{name: "empty operation id", opID: "", taskID: t1, gen: 1},
		{name: "operation id with separator", opID: "spawn/record", taskID: t1, gen: 1},
		{name: "zero task", opID: "op-record-1", taskID: domain.TaskID{}, gen: 1},
		{name: "zero generation", opID: "op-record-1", taskID: t1, gen: 0, want: ErrInvalidGeneration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, err := c.CommittedOperationReceipt(tc.opID, tc.taskID, tc.gen)
			if err == nil || found {
				t.Fatalf("CommittedOperationReceipt = %v, %v; want a validation error", found, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("CommittedOperationReceipt error = %v, want %v", err, tc.want)
			}
		})
	}
}
