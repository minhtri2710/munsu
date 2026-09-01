package taskauthority

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

func contractRequest(c *Canonical, taskID string, prec domain.Precondition, mode string) CanonicalRecordDeliveryContractRequest {
	id, _ := domain.NewTaskID(taskID)
	return CanonicalRecordDeliveryContractRequest{
		HomeID:       c.HomeID(),
		TaskID:       id,
		Precondition: prec,
		Mode:         mode,
		Reason:       "spawn",
	}
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

	out := mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")
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
	if agg.DeliveryContract.Mode != "no-mistakes" {
		t.Fatalf("contract mode = %q", agg.DeliveryContract.Mode)
	}
	if agg.DeliveryContract.OperationID != "op-contract-1" || agg.DeliveryContract.RecordedAt <= 0 {
		t.Fatalf("contract = %+v", *agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryContractRefusesSilentOverride builds the exact
// refused state the contract exists to prevent: a task that already contracts
// one delivery mode, asked to record a DIFFERENT one with no re-scaffold
// intent. The committed contract must survive intact.
func TestCanonicalRecordDeliveryContractRefusesSilentOverride(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")

	req := contractRequest(c, "t1", preconditionOf(1, 2), "direct-PR")
	if _, err := c.RecordDeliveryContract(mustOperation(t, "op-contract-override", req), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("silent override = %v, want ErrConflict", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract == nil || agg.DeliveryContract.Mode != "no-mistakes" {
		t.Fatalf("refused override mutated the contract: %+v", agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryContractRescaffoldReplaces pins the one
// sanctioned way a recorded contract changes.
func TestCanonicalRecordDeliveryContractRescaffoldReplaces(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")

	req := contractRequest(c, "t1", preconditionOf(1, 2), "local-only")
	req.Rescaffold = true
	if _, err := c.RecordDeliveryContract(mustOperation(t, "op-contract-rescaffold", req), req); err != nil {
		t.Fatalf("re-scaffold: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract == nil || agg.DeliveryContract.Mode != "local-only" {
		t.Fatalf("re-scaffold did not replace the contract: %+v", agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryContractSameModeIsNoOp keeps a re-entrant spawn
// from bumping the revision on a contract it already agrees with.
func TestCanonicalRecordDeliveryContractSameModeIsNoOp(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "direct-PR")

	out := mustRecordContract(t, c, "op-contract-2", "t1", preconditionOf(1, 2), "direct-PR")
	if out.Revision != 2 {
		t.Fatalf("re-record bumped the revision: %+v", out)
	}
	agg, err := c.Get(mustTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryContract.OperationID != "op-contract-1" {
		t.Fatalf("re-record rewrote the committed contract: %+v", *agg.DeliveryContract)
	}
}

// TestCanonicalRecordDeliveryContractReplaysByOperationID pins idempotent
// replay: the same Operation ID and digest returns the durable prior outcome.
func TestCanonicalRecordDeliveryContractReplaysByOperationID(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	mustCreate(t, c, "t1")
	first := mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")
	replay := mustRecordContract(t, c, "op-contract-1", "t1", preconditionOf(1, 1), "no-mistakes")
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

// TestCanonicalDeliveryContractSurvivesReopen pins the contract as per-TASK,
// not per-generation: the next generation inherits it so the next spawn reads
// it instead of re-resolving the mode fresh.
func TestCanonicalDeliveryContractSurvivesReopen(t *testing.T) {
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
	if agg.Generation != 2 {
		t.Fatalf("generation = %d", agg.Generation)
	}
	if agg.DeliveryContract == nil || agg.DeliveryContract.Mode != "local-only" {
		t.Fatalf("reopened generation lost the delivery contract: %+v", agg.DeliveryContract)
	}
	// The carried record must be a copy, never an alias of the historical
	// generation's pointer.
	hist, err := c.GetGeneration(mustTaskID(t, "t1"), 1)
	if err != nil {
		t.Fatalf("GetGeneration(1): %v", err)
	}
	if hist.DeliveryContract == agg.DeliveryContract {
		t.Fatal("reopened generation aliases the prior generation's contract pointer")
	}
}
