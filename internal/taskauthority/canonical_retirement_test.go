package taskauthority

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

func retireRequest(t *testing.T, c *Canonical, taskID string, prec domain.Precondition) CanonicalRetireRequest {
	return CanonicalRetireRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: prec,
		Reason:       "retire",
	}
}

func TestCanonicalRetireReachesRetiredPhase(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := retireRequest(t, c, "t1", preconditionOf(1, 1))
	out, err := c.Retire(mustOperation(t, "op-retire-1", req), req)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if out.Phase != PhaseRetired || out.Revision != 2 {
		t.Fatalf("retire outcome = %+v", out)
	}

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseRetired || agg.Revision != 2 {
		t.Fatalf("retired aggregate = %+v", agg)
	}
	if !agg.Phase.terminal() {
		t.Fatalf("retired phase %q is not terminal", agg.Phase)
	}
}

func TestCanonicalRetireIsExactGeneration(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Start to advance to revision 2.
	start := startWithRev(c, "t1", 1)
	if _, err := c.Start(mustOperation(t, "op-start-pre", start), start); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Retire with a stale precondition (expect revision 1, current is 2).
	req := retireRequest(t, c, "t1", preconditionOf(1, 1))
	if _, err := c.Retire(mustOperation(t, "op-retire-stale", req), req); !errors.Is(err, domain.ErrStalePrecondition) {
		t.Fatalf("stale retire = %v, want domain.ErrStalePrecondition", err)
	}
}

func TestCanonicalRetireAlreadyRetiredConflicts(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := retireRequest(t, c, "t1", preconditionOf(1, 1))
	if _, err := c.Retire(mustOperation(t, "op-retire-1", req), req); err != nil {
		t.Fatalf("Retire: %v", err)
	}

	// A fresh operation on the now-retired generation (revision 2) conflicts.
	again := retireRequest(t, c, "t1", preconditionOf(1, 2))
	if _, err := c.Retire(mustOperation(t, "op-retire-2", again), again); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-retire = %v, want ErrConflict", err)
	}
}

func TestCanonicalRetireIdempotentReplay(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := retireRequest(t, c, "t1", preconditionOf(1, 1))
	op := mustOperation(t, "op-retire-replay", req)
	first, err := c.Retire(op, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Retire(op, req)
	if err != nil {
		t.Fatalf("replay retire: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
	if second.Phase != PhaseRetired || second.Revision != first.Revision {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}
}

func TestCanonicalRetireReleasesResourceBindings(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	wt := bindWorktreeRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-retire", wt), wt); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}

	req := retireRequest(t, c, "t1", preconditionOf(1, 2))
	if _, err := c.Retire(mustOperation(t, "op-retire-wt", req), req); err != nil {
		t.Fatalf("Retire: %v", err)
	}

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseRetired {
		t.Fatalf("aggregate phase = %q, want retired", agg.Phase)
	}
	// Active owner is cleared: no active binding query reports an owned resource.
	if agg.Worktree != nil {
		t.Fatalf("retired generation still holds active worktree binding: %+v", agg.Worktree)
	}
	// The exact ownership evidence is preserved durably.
	if agg.Retirement == nil {
		t.Fatalf("retirement evidence missing")
	}
	if agg.Retirement.Worktree == nil || agg.Retirement.Worktree.LeaseID != "lease-wt" || agg.Retirement.Worktree.FenceToken != "fence-wt" {
		t.Fatalf("retirement evidence = %+v", agg.Retirement)
	}
	if agg.Retirement.OperationID != "op-retire-wt" {
		t.Fatalf("retirement operation id = %q", agg.Retirement.OperationID)
	}
	if agg.Retirement.Generation != 1 {
		t.Fatalf("retirement generation = %s", agg.Retirement.Generation)
	}
}

// TestCanonicalRetireEvidenceSurvivesReopen proves the exact lease and fence
// identities and retirement Operation ID survive a home reopen: the evidence
// is part of the Task generation document, durably rereadable.
func TestCanonicalRetireEvidenceSurvivesReopen(t *testing.T) {
	c, _, root := newTestCanonical(t)
	mustCreate(t, c, "t1")

	wt := bindWorktreeRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-ev-retire", wt), wt); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	ep := bindEndpointRequest(c, "t1", preconditionOf(1, 2))
	if _, err := c.BindEndpoint(mustOperation(t, "op-ep-ev-retire", ep), ep); err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}

	req := retireRequest(t, c, "t1", preconditionOf(1, 3))
	if _, err := c.Retire(mustOperation(t, "op-retire-ev", req), req); err != nil {
		t.Fatalf("Retire: %v", err)
	}

	h2, err := home.Open(root)
	if err != nil {
		t.Fatalf("home.Open: %v", err)
	}
	c2, err := NewCanonical(h2)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := c2.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseRetired || agg.Endpoint != nil || agg.Worktree != nil {
		t.Fatalf("retired aggregate = %+v", agg)
	}
	if agg.Retirement == nil {
		t.Fatalf("retirement evidence lost across reopen")
	}
	if agg.Retirement.Worktree == nil || agg.Retirement.Worktree.LeaseID != "lease-wt" || agg.Retirement.Worktree.FenceToken != "fence-wt" {
		t.Fatalf("worktree evidence = %+v", agg.Retirement.Worktree)
	}
	if agg.Retirement.Endpoint == nil || agg.Retirement.Endpoint.LeaseID != "lease-ep" || agg.Retirement.Endpoint.FenceToken != "fence-ep" {
		t.Fatalf("endpoint evidence = %+v", agg.Retirement.Endpoint)
	}
	if agg.Retirement.OperationID != "op-retire-ev" {
		t.Fatalf("retirement operation id = %q", agg.Retirement.OperationID)
	}
}

// TestCanonicalRetireReplayReturnsSameEvidence proves replaying the retire
// operation returns the same committed outcome and the preserved evidence is
// unchanged.
func TestCanonicalRetireReplayReturnsSameEvidence(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	wt := bindWorktreeRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BindWorktree(mustOperation(t, "op-wt-rp", wt), wt); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}

	req := retireRequest(t, c, "t1", preconditionOf(1, 2))
	op := mustOperation(t, "op-retire-rp", req)
	first, err := c.Retire(op, req)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	second, err := c.Retire(op, req)
	if err != nil {
		t.Fatalf("replay retire: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v", first.Replayed, second.Replayed)
	}
	if second.Phase != PhaseRetired || second.Revision != first.Revision {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}

	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Retirement == nil || agg.Retirement.Worktree == nil || agg.Retirement.Worktree.LeaseID != "lease-wt" {
		t.Fatalf("evidence after replay = %+v", agg.Retirement)
	}
}

// TestCanonicalRetireNonCurrentGenerationFailsClosed proves retiring a
// non-current generation fails closed: Retire applies only to the exact
// current generation.
func TestCanonicalRetireNonCurrentGenerationFailsClosed(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	// Move to generation 2 via complete + reopen, then try to retire the
	// stale generation-1 revision.
	complete := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 1), To: PhaseDone, Reason: "done"}
	if _, err := c.Complete(mustOperation(t, "op-c-pre", complete), complete); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "t1"), Precondition: preconditionOf(1, 2), Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-r-pre", reopen), reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	// Retire against generation 1 (stale, no longer current) fails closed.
	req := retireRequest(t, c, "t1", preconditionOf(1, 2))
	if _, err := c.Retire(mustOperation(t, "op-retire-stale-gen", req), req); !errors.Is(err, domain.ErrStalePrecondition) {
		t.Fatalf("retire non-current generation = %v, want domain.ErrStalePrecondition", err)
	}
}

func TestCanonicalRetireOperationReusedConflict(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	req := retireRequest(t, c, "t1", preconditionOf(1, 1))
	op := mustOperation(t, "op-shared-retire", req)
	if _, err := c.Retire(op, req); err != nil {
		t.Fatal(err)
	}

	diff := retireRequest(t, c, "t1", preconditionOf(1, 1))
	diff.Reason = "other"
	reused, err := domain.NewOperation(op.ID, diff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Retire(reused, diff); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

// TestCanonicalRetirePreservesAcquiredEndpointEvidence proves High-2 closure
// (BEO-16/P1a): a retirement of a generation that acquired an endpoint
// pre-bind (launch intent + AttachEndpoint, never bound) durably preserves the
// exact acquired identity (backend/handle/lease/fence/incarnation +
// generation) as cleanup evidence, so the known externally held resource can
// never be completed-unresolved — cleanup must reconcile it.
func TestCanonicalRetirePreservesAcquiredEndpointEvidence(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	launch := launchRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BeginSpawn(mustOperation(t, "op-spawn-ae", launch), launch); err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}
	attach := attachRequest(c, "t1", preconditionOf(1, 2), launch, "@1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-ae", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.AcquiredEndpoint == nil || agg.AcquiredEndpoint.Handle != "@1" {
		t.Fatalf("acquired endpoint not recorded: %+v", agg.AcquiredEndpoint)
	}

	req := retireRequest(t, c, "t1", preconditionOf(1, 3))
	if _, err := c.Retire(mustOperation(t, "op-retire-ae", req), req); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	agg, err = c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Retirement == nil || agg.Retirement.Acquired == nil {
		t.Fatalf("acquired endpoint evidence not preserved on retirement: %+v", agg.Retirement)
	}
	ae := agg.Retirement.Acquired
	if ae.Backend != launch.Backend || ae.Handle != "@1" ||
		ae.LeaseID != launch.EndpointReservationID || ae.FenceToken != launch.EndpointFenceToken ||
		ae.Incarnation != launch.EndpointIncarnation {
		t.Fatalf("acquired evidence identity not exact: %+v", ae)
	}
	if agg.Retirement.Generation != 1 || agg.Retirement.OperationID != "op-retire-ae" {
		t.Fatalf("evidence identity = %+v, want generation 1 / op-retire-ae", agg.Retirement)
	}
	if agg.Retirement.Endpoint != nil {
		t.Fatalf("bound endpoint evidence present without a binding: %+v", agg.Retirement.Endpoint)
	}
	if agg.CleanupClaim == nil || agg.CleanupClaim.Status != CleanupActive {
		t.Fatalf("claim not active: %+v", agg.CleanupClaim)
	}
}

// TestCanonicalRetireBoundEndpointSubsumesAcquiredRecord proves the acquired
// record is NOT duplicated as cleanup evidence when the endpoint was bound:
// BindEndpoint enforces exact identity match, so the bound Endpoint evidence
// covers the same resource and the cleanup releases it exactly once.
func TestCanonicalRetireBoundEndpointSubsumesAcquiredRecord(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	launch := launchRequest(c, "t1", preconditionOf(1, 1))
	if _, err := c.BeginSpawn(mustOperation(t, "op-spawn-sub", launch), launch); err != nil {
		t.Fatalf("BeginSpawn: %v", err)
	}
	attach := attachRequest(c, "t1", preconditionOf(1, 2), launch, "@1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-sub", attach), attach); err != nil {
		t.Fatalf("AttachEndpoint: %v", err)
	}
	bindWT := CanonicalBindWorktreeRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 3),
		Binding:      launchWorktreeBinding(launch),
		Reason:       "spawn",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-bindwt-sub", bindWT), bindWT); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	record := recordLaunchRequest(c, "t1", preconditionOf(1, 4), launch)
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-sub", record), record); err != nil {
		t.Fatalf("RecordLaunch: %v", err)
	}
	bind := CanonicalBindEndpointRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 5),
		Binding:      launchEndpointBinding(launch, "@1"),
		Reason:       "spawn",
	}
	if _, err := c.BindEndpoint(mustOperation(t, "op-bind-sub", bind), bind); err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}

	req := retireRequest(t, c, "t1", preconditionOf(1, 6))
	if _, err := c.Retire(mustOperation(t, "op-retire-sub", req), req); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Retirement == nil || agg.Retirement.Endpoint == nil {
		t.Fatalf("bound endpoint evidence missing: %+v", agg.Retirement)
	}
	if agg.Retirement.Acquired != nil {
		t.Fatalf("acquired evidence duplicated alongside the bound endpoint (double-dispose risk): %+v", agg.Retirement.Acquired)
	}
	if agg.Retirement.Endpoint.Handle != "@1" || agg.Retirement.Endpoint.LeaseID != launch.EndpointReservationID {
		t.Fatalf("bound evidence identity: %+v", agg.Retirement.Endpoint)
	}
}
