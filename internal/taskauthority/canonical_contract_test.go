package taskauthority

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

func contractRequest(c *Canonical, taskID string, prec domain.Precondition, mode string) CanonicalRecordDeliveryContractRequest {
	id, _ := domain.NewTaskID(taskID)
	req := CanonicalRecordDeliveryContractRequest{
		HomeID:       c.HomeID(),
		TaskID:       id,
		Precondition: prec,
		Mode:         mode,
	}
	if mode == "direct-PR" {
		req.Forge = DeliveryStep{Adapter: "gitlab", Path: "/bin/glab", ProbeState: "ready"}
	} else {
		req.Forge = DeliveryStep{Baseline: true, ProbeState: "baseline"}
	}
	return req
}

func mustRecordContract(t *testing.T, c *Canonical, opID, taskID string, prec domain.Precondition, mode string) Outcome {
	t.Helper()
	req := contractRequest(c, taskID, prec, mode)
	out, err := c.RecordDeliveryContract(mustOperation(t, opID, req), req)
	if err != nil {
		t.Fatalf("RecordDeliveryContract(%s, %s): %v", taskID, mode, err)
	}
	return out
}

func TestCanonicalRecordDeliveryContract(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	out := mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "direct-PR")
	if out.Generation != 1 || out.Revision != 2 {
		t.Fatalf("record outcome = %+v", out)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract == nil {
		t.Fatal("no delivery contract recorded")
	}
	if agg.DeliveryContract.Mode != "direct-PR" {
		t.Fatalf("contract mode = %q", agg.DeliveryContract.Mode)
	}
	if agg.DeliveryContract.OperationID != "op-contract-1" || agg.DeliveryContract.RecordedAt <= 0 {
		t.Fatalf("contract = %+v", *agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryContractReplaysByOperationID pins idempotent
// replay: the same Operation ID and digest returns the durable prior outcome.
func TestCanonicalRecordDeliveryContractReplaysByOperationID(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	first := mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "direct-PR")
	replay := mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "direct-PR")
	if replay.Revision != first.Revision || replay.Generation != first.Generation {
		t.Fatalf("replay = %+v, first = %+v", replay, first)
	}
}

// TestCanonicalRecordDeliveryContractRejectsUnknownMode keeps an
// unenforceable mode out of the durable record.
func TestCanonicalRecordDeliveryContractRejectsUnknownMode(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")

	for _, mode := range []string{"", "direct-pr", "yolo"} {
		req := contractRequest(c, "t1", preconditionOf(1, 1), mode)
		_, err := c.RecordDeliveryContract(mustOperation(t, "op-contract-bad-"+mode, req), req)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("mode %q = %v, want ErrInvalidInput", mode, err)
		}
	}
}

func TestCanonicalDeliveryContractIsPerGeneration(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "local-only")

	complete := CanonicalCompleteRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 2),
		To:           PhaseDone,
		Reason:       "done",
	}
	if _, err := c.Complete(mustOperation(t, "op-complete-c", complete), complete); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	reopen := CanonicalReopenRequest{
		HomeID:       c.HomeID(),
		TaskID:       mustTaskID(t, "t1"),
		Precondition: preconditionOf(1, 3),
		Reason:       "reopen",
	}
	if _, err := c.Reopen(mustOperation(t, "op-reopen-c", reopen), reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Generation != 2 || agg.DeliveryContract != nil {
		t.Fatalf("reopened generation = %d contract=%+v, want an uncaptured generation 2", agg.Generation, agg.DeliveryContract)
	}
	mustRecordContract(t, c, "op-contract-2", "t1", preconditionOf(2, 1), "direct-PR")
	current, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if current.DeliveryContract == nil || current.DeliveryContract.Mode != "direct-PR" || current.DeliveryContract.Forge.Adapter != "gitlab" {
		t.Fatalf("new generation contract = %+v", current.DeliveryContract)
	}
	hist, err := c.GetGeneration(mustTaskID(t, "t1"), 1)
	if err != nil {
		t.Fatalf("GetGeneration(1): %v", err)
	}
	if hist.DeliveryContract == nil || hist.DeliveryContract.Mode != "local-only" {
		t.Fatalf("historical generation contract = %+v", hist.DeliveryContract)
	}
}
