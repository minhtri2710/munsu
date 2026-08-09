package taskauthority

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

func promoteRequest(t *testing.T, c *Canonical, taskID string, prec domain.Precondition) CanonicalPromoteRequest {
	t.Helper()
	return CanonicalPromoteRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: prec,
		CurrentKind:  "scout",
		TargetKind:   "ship",
		Reason:       "promote",
	}
}

// mustDoneScout seeds one scout task and completes it into done at the
// returned revision.
func mustDoneScout(t *testing.T, c *Canonical, taskID string) domain.Precondition {
	t.Helper()
	create := createRequest(c, taskID)
	create.Kind = "scout"
	create.ScoutScope = "investigate the requested question"
	create.ScoutRuntimeBudgetSecs = 300
	create.ScoutScope = "investigate the requested question"
	create.ScoutRuntimeBudgetSecs = 300
	op := mustOperation(t, "op-create-"+taskID, create)
	if _, err := c.Create(op, create); err != nil {
		t.Fatal(err)
	}
	complete := CanonicalCompleteRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: preconditionOf(1, 1),
		To:           PhaseDone,
		Reason:       "complete",
	}
	if _, err := c.Complete(mustOperation(t, "op-done-"+taskID, complete), complete); err != nil {
		t.Fatal(err)
	}
	return preconditionOf(1, 2)
}

func TestCanonicalPromoteFlipsTerminalScoutToShip(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	prec := mustDoneScout(t, c, "scout-1")

	req := promoteRequest(t, c, "scout-1", prec)
	out, err := c.Promote(mustOperation(t, "op-promote-1", req), req)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if out.Revision != 3 || out.Phase != PhaseDone || out.Replayed {
		t.Fatalf("promote outcome = %+v", out)
	}
	agg, err := c.Get(mustTaskID(t, "scout-1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Definition.Kind != "ship" {
		t.Fatalf("promoted kind = %q, want ship", agg.Definition.Kind)
	}
	if agg.Phase != PhaseDone || agg.Revision != 3 {
		t.Fatalf("aggregate after promote = phase %s rev %d, want done rev 3", agg.Phase, uint64(agg.Revision))
	}
}

func TestCanonicalPromoteReplayIsIdempotent(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	prec := mustDoneScout(t, c, "scout-1")

	req := promoteRequest(t, c, "scout-1", prec)
	op := mustOperation(t, "op-promote-replay", req)
	first, err := c.Promote(op, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Promote(op, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed || first.Replayed {
		t.Fatalf("replay flags first=%v second=%v, want false/true", first.Replayed, second.Replayed)
	}
	if second.Revision != first.Revision || second.Phase != first.Phase {
		t.Fatalf("replay outcome differs: %+v vs %+v", second, first)
	}
	agg, err := c.Get(mustTaskID(t, "scout-1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 3 {
		t.Fatalf("replay advanced revision: %d", uint64(agg.Revision))
	}
}

func TestCanonicalPromoteReusedOperationConflicts(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	prec := mustDoneScout(t, c, "scout-1")

	req := promoteRequest(t, c, "scout-1", prec)
	op := mustOperation(t, "op-promote-shared", req)
	if _, err := c.Promote(op, req); err != nil {
		t.Fatal(err)
	}
	// Reuse the same Operation ID with a different intent (a start).
	start := CanonicalStartRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "scout-1"),
		Precondition: preconditionOf(1, 3),
		Reason:       "start",
	}
	reused := mustOperation(t, "op-promote-shared", start)
	if _, err := c.Start(reused, start); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

func TestCanonicalPromoteFailsClosedOnPreconditions(t *testing.T) {
	c, _, _ := newTestCanonical(t)

	// Non-scout kind: a done ship task cannot promote.
	ship := createRequest(c, "ship-1")
	op := mustOperation(t, "op-create-ship", ship)
	if _, err := c.Create(op, ship); err != nil {
		t.Fatal(err)
	}
	complete := CanonicalCompleteRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "ship-1"),
		Precondition: preconditionOf(1, 1),
		To:           PhaseDone,
		Reason:       "complete",
	}
	if _, err := c.Complete(mustOperation(t, "op-done-ship", complete), complete); err != nil {
		t.Fatal(err)
	}
	req := promoteRequest(t, c, "ship-1", preconditionOf(1, 2))
	if _, err := c.Promote(mustOperation(t, "op-promote-ship", req), req); err == nil || !errors.Is(err, ErrPrecondition) {
		t.Fatalf("promote non-scout = %v, want ErrPrecondition", err)
	}

	// Live phase: a queued scout cannot promote.
	live := createRequest(c, "live-1")
	live.Kind = "scout"
	live.ScoutScope = "investigate the requested question"
	live.ScoutRuntimeBudgetSecs = 300
	if _, err := c.Create(mustOperation(t, "op-create-live", live), live); err != nil {
		t.Fatal(err)
	}
	liveReq := promoteRequest(t, c, "live-1", preconditionOf(1, 1))
	if _, err := c.Promote(mustOperation(t, "op-promote-live", liveReq), liveReq); err == nil || !errors.Is(err, ErrPrecondition) {
		t.Fatalf("promote queued scout = %v, want ErrPrecondition", err)
	}

	// Retired phase: a retired scout never promotes.
	retired := createRequest(c, "retired-1")
	retired.Kind = "scout"
	retired.ScoutScope = "investigate the requested question"
	retired.ScoutRuntimeBudgetSecs = 300
	if _, err := c.Create(mustOperation(t, "op-create-retired", retired), retired); err != nil {
		t.Fatal(err)
	}
	retireReq := CanonicalRetireRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "retired-1"),
		Precondition: preconditionOf(1, 1),
		Reason:       "retire",
	}
	if _, err := c.Retire(mustOperation(t, "op-retire", retireReq), retireReq); err != nil {
		t.Fatal(err)
	}
	retiredPromote := promoteRequest(t, c, "retired-1", preconditionOf(1, 2))
	if _, err := c.Promote(mustOperation(t, "op-promote-retired", retiredPromote), retiredPromote); err == nil || !errors.Is(err, ErrPrecondition) {
		t.Fatalf("promote retired scout = %v, want ErrPrecondition", err)
	}
}

func TestCanonicalPromoteStalePreconditionFailsClosed(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustDoneScout(t, c, "scout-1")

	req := promoteRequest(t, c, "scout-1", preconditionOf(1, 9))
	if _, err := c.Promote(mustOperation(t, "op-promote-stale", req), req); !errors.Is(err, domain.ErrStalePrecondition) {
		t.Fatalf("stale promote = %v, want domain.ErrStalePrecondition", err)
	}
}

func TestCanonicalPromoteMissingTaskFailsClosed(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	req := promoteRequest(t, c, "absent", preconditionOf(1, 1))
	if _, err := c.Promote(mustOperation(t, "op-promote-absent", req), req); !errors.Is(err, ErrNotFound) {
		t.Fatalf("promote absent = %v, want ErrNotFound", err)
	}
}

func TestCanonicalPromoteRefusesTransferReservation(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	prec := mustDoneScout(t, c, "scout-1")

	reserve := CanonicalReserveTransferRequest{
		HomeID:        c.HomeID(),
		TaskID:        mustTaskID(t, "scout-1"),
		Precondition:  prec,
		ReservationID: "res-1",
		Destination:   mustHomeID(t, "dest"),
		FenceToken:    "fence-1",
		Reason:        "transfer",
	}
	if _, err := c.ReserveTransfer(mustOperation(t, "op-reserve", reserve), reserve); err != nil {
		t.Fatal(err)
	}
	req := promoteRequest(t, c, "scout-1", preconditionOf(1, 3))
	if _, err := c.Promote(mustOperation(t, "op-promote-reserved", req), req); err == nil || !errors.Is(err, ErrConflict) {
		t.Fatalf("promote reserved task = %v, want ErrConflict", err)
	}
}

func TestCanonicalPromoteSurvivesReopen(t *testing.T) {
	c, _, root := newTestCanonical(t)
	prec := mustDoneScout(t, c, "scout-1")

	req := promoteRequest(t, c, "scout-1", prec)
	if _, err := c.Promote(mustOperation(t, "op-promote-durable", req), req); err != nil {
		t.Fatal(err)
	}

	// Reopening the home rereads the committed promoted aggregate and the
	// receipt-driven replay remains idempotent.
	h, err := home.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := reopened.Get(mustTaskID(t, "scout-1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Definition.Kind != "ship" || agg.Revision != 3 {
		t.Fatalf("reopened aggregate = kind %q rev %d", agg.Definition.Kind, uint64(agg.Revision))
	}
	replay, err := reopened.Promote(mustOperation(t, "op-promote-durable", req), req)
	if err != nil || !replay.Replayed {
		t.Fatalf("reopened replay = %+v err=%v, want replayed", replay, err)
	}
}
