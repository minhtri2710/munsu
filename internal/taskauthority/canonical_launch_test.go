package taskauthority

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
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

	release := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "spawn-hold", Reason: "resume"}
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
			Head: "sha", LeaseID: req.WorktreeReservationID, FenceToken: req.WorktreeFenceToken, BoundAtUnix: 2000,
		},
		Reason: "spawn",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-1", bw), bw); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}

	// Acquire the endpoint with the opaque incarnation.
	attach := attachRequest(c, "t1", preconditionOf(1, rev+1), req, "handle-1")
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
	foreign := attachRequest(c, "t1", preconditionOf(1, rev+2), req, "handle-1")
	foreign.Incarnation = "inc-foreign"
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-foreign", foreign), foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("attach with different incarnation = %v, want ErrConflict", err)
	}

	// Record launch evidence, then bind the endpoint.
	record := recordLaunchRequest(c, "t1", preconditionOf(1, rev+2), req)
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-1", record), record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	be := bindEndpointRequest(c, "t1", preconditionOf(1, rev+3))
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
