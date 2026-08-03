package taskauthority

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// mustRetireRequest builds a valid Retire request for one task. Verified
// delivery is required by default so prerequisite enforcement is exercised;
// tests that need the baseline mode set it explicitly. The Operation ID is
// stable per task generation, mirroring the fleet's deterministic
// per-generation retirement identity.
func mustRetireRequest(taskID string, generation Generation, requireVerified bool) RetireRequest {
	return RetireRequest{
		OperationID:             fmt.Sprintf("op-retire-%s-%s", taskID, generation),
		Actor:                   Actor{ID: "test", Rank: "general"},
		TaskID:                  taskID,
		ExpectedGeneration:      generation,
		RequireVerifiedDelivery: requireVerified,
		Reason:                  "retirement",
	}
}

// retirementAuditCount counts committed retirement audit events for the task
// from the Store view (one typed audit event per committed transition).
func retirementAuditCount(t *testing.T, a *Authority, taskID string) int {
	t.Helper()
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range v.Audit {
		if ev.Kind == AuditRetirement && ev.TaskID == taskID {
			n++
		}
	}
	return n
}

// --- Retire ---

func TestRetireCommitsRetiredPhaseWithDurableReceipt(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	res, err := a.Retire(mustRetireRequest("t1", 1, false))
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != "t1" || res.Generation != 1 || res.Revision != 2 || res.Phase != PhaseRetired {
		t.Fatalf("retire result = %+v, want revision 2 retired", res)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseRetired || agg.Revision != 2 {
		t.Fatalf("aggregate = phase %q revision %d, want retired revision 2", agg.Phase, agg.Revision)
	}
	if retirementAuditCount(t, a, "t1") != 1 {
		t.Fatalf("expected one retirement audit event")
	}

	// The durable idempotency receipt pins the committed outcome.
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Receipts) != 2 { // create + retire
		t.Fatalf("receipts = %d, want 2", len(v.Receipts))
	}
}

func TestRetireGenerationFence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	if _, err := a.Retire(mustRetireRequest("t1", 1, false)); err != nil {
		t.Fatal(err)
	}
	// A stale generation fence fails closed and mutates nothing.
	if _, err := a.Retire(mustRetireRequest("t1", 2, false)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation error = %v, want ErrConflict", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("stale retire advanced revision to %d, want 2", agg.Revision)
	}
	if retirementAuditCount(t, a, "t1") != 1 {
		t.Fatalf("stale retire appended a second audit event")
	}
}

func TestRetireMissingTaskFailsClosed(t *testing.T) {
	a := newTestAuthority(t)
	if _, err := a.Retire(mustRetireRequest("missing", 1, false)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}
}

func TestRetirePrerequisiteRequiresVerifiedDeliveryEvidence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	// A task with no committed merge/delivered evidence is not
	// retired-eligible when the calling flow requires verified delivery:
	// the operation fails closed with a typed precondition error and
	// mutates nothing.
	if _, err := a.Retire(mustRetireRequest("t1", 1, true)); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("prerequisite error = %v, want ErrPrecondition", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseQueued || agg.Revision != 1 {
		t.Fatalf("failed retire mutated the aggregate: phase %q revision %d", agg.Phase, agg.Revision)
	}
	if retirementAuditCount(t, a, "t1") != 0 {
		t.Fatalf("failed retire appended an audit event")
	}
}

func TestRetirePrerequisiteSatisfiedByMergedAttempt(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)
	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeMerged, head)); err != nil {
		t.Fatal(err)
	}

	res, err := a.Retire(mustRetireRequest("t1", 1, true))
	if err != nil {
		t.Fatal(err)
	}
	if res.Phase != PhaseRetired || res.Revision != 3 {
		t.Fatalf("retire result = %+v, want retired revision 3", res)
	}
	// The verified merged truth is retained inside the retired aggregate.
	agg, _ := a.Get("t1")
	if agg.MergeAttempt == nil || agg.MergeAttempt.Outcome != MergeOutcomeMerged {
		t.Fatalf("retire erased the verified merged truth: %+v", agg.MergeAttempt)
	}
}

func TestRetirePrerequisiteSatisfiedByAlreadyMerged(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)
	if _, err := a.RecordMergeAttempt(mustMergeAttemptRequest("t1", 1, MergeOutcomeAlreadyMerged, head)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Retire(mustRetireRequest("t1", 1, true)); err != nil {
		t.Fatalf("already-merged evidence must satisfy the prerequisite: %v", err)
	}
}

func TestRetirePrerequisiteSatisfiedByDeliveryTerminal(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)
	preparedTask(t, a, "t1", head)
	if _, err := a.CompleteDelivery(mustCompleteRequest("t1", 1, DeliveryTerminalDelivered, head)); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Retire(mustRetireRequest("t1", 1, true)); err != nil {
		t.Fatalf("delivered terminal must satisfy the prerequisite: %v", err)
	}
	agg, _ := a.Get("t1")
	if agg.Phase != PhaseRetired {
		t.Fatalf("aggregate phase = %q, want retired", agg.Phase)
	}
}

func TestRetireSameOpReplayIsIdempotent(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	req := mustRetireRequest("t1", 1, false)
	req.OperationID = "op-stable-retire"

	first, err := a.Retire(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Retire(req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != first.Revision || second.Phase != first.Phase {
		t.Fatalf("replay = %+v, want original %+v", second, first)
	}
	if retirementAuditCount(t, a, "t1") != 1 {
		t.Fatalf("replay appended a second audit event")
	}
	agg, _ := a.Get("t1")
	if agg.Revision != 2 {
		t.Fatalf("replay advanced revision to %d, want 2", agg.Revision)
	}
}

func TestRetireChangedDigestConflicts(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	req := mustRetireRequest("t1", 1, false)
	req.OperationID = "op-stable-retire"
	if _, err := a.Retire(req); err != nil {
		t.Fatal(err)
	}

	// Same Operation ID with a different intent (verified delivery
	// requirement toggled) is a non-retryable conflict.
	changed := req
	changed.RequireVerifiedDelivery = true
	if _, err := a.Retire(changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed-digest error = %v, want ErrOperationConflict", err)
	}
	agg, _ := a.Get("t1")
	if agg.Phase != PhaseRetired || agg.Revision != 2 {
		t.Fatalf("conflict mutated the committed state: %+v", agg)
	}
}

func TestRetireAlreadyRetiredCommitsOnce(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.Retire(mustRetireRequest("t1", 1, false)); err != nil {
		t.Fatal(err)
	}

	// A fresh Operation identity targeting the already-retired generation is
	// refused: the retired phase and its audit event commit exactly once.
	fresh := mustRetireRequest("t1", 1, false)
	fresh.OperationID = "op-retire-fresh"
	if _, err := a.Retire(fresh); !errors.Is(err, ErrConflict) {
		t.Fatalf("already-retired error = %v, want ErrConflict", err)
	}
	agg, _ := a.Get("t1")
	if agg.Revision != 2 {
		t.Fatalf("second retire advanced revision to %d, want 2", agg.Revision)
	}
	if retirementAuditCount(t, a, "t1") != 1 {
		t.Fatalf("second retire appended a second audit event")
	}
}

func TestRetireAfterReopenCommitsNextGeneration(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	if _, err := a.Retire(mustRetireRequest("t1", 1, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reopen(ReopenRequest{
		OperationID:        "op-reopen-t1",
		Actor:              Actor{ID: "test", Rank: "general"},
		TaskID:             "t1",
		ExpectedGeneration: 1,
		Reason:             "reopen",
	}); err != nil {
		t.Fatal(err)
	}

	// The reopened generation 2 retires under its own operation identity
	// (per-generation stable ID): the historical generation 1 stays retired.
	res, err := a.Retire(mustRetireRequest("t1", 2, false))
	if err != nil {
		t.Fatal(err)
	}
	if res.Generation != 2 || res.Revision != 2 || res.Phase != PhaseRetired {
		t.Fatalf("generation 2 retire result = %+v", res)
	}
	if retirementAuditCount(t, a, "t1") != 2 {
		t.Fatalf("expected two retirement audit events (one per generation)")
	}
	agg, _ := a.Get("t1")
	if agg.Generation != 2 || agg.Phase != PhaseRetired {
		t.Fatalf("current aggregate = %+v, want generation 2 retired", agg)
	}
}

func TestRetireValidation(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	if _, err := a.Retire(mustRetireRequest("t1", 0, false)); !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("zero generation error = %v, want ErrInvalidGeneration", err)
	}
	agg, _ := a.Get("t1")
	if agg.Phase != PhaseQueued {
		t.Fatalf("invalid request mutated the aggregate: %+v", agg)
	}
}
