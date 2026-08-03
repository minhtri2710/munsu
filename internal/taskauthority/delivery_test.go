package taskauthority

import (
	"errors"
	"strings"
	"testing"
)

// mustPrepareRequest builds a valid PrepareDelivery request for one task.
func mustPrepareRequest(taskID string, generation Generation, headSHA string) PrepareDeliveryRequest {
	return PrepareDeliveryRequest{
		OperationID:        "op-prepare-" + taskID,
		Actor:              Actor{ID: "test", Rank: "general"},
		TaskID:             taskID,
		ExpectedGeneration: generation,
		State:              DeliveryPrepareStateReviewReady,
		HeadSHA:            headSHA,
		Identity: ProviderIdentitySnapshot{
			Provider: "github",
			Owner:    "owner",
			Repo:     "repo",
			Number:   42,
			URL:      "https://github.com/owner/repo/pull/42",
			BaseRef:  "main",
			HeadRef:  "feature/test",
			HeadSHA:  headSHA,
		},
		Reason: "pr-check",
	}
}

// mustCompleteRequest builds a valid CompleteDelivery request for one task
// carrying the given terminal transition and head.
func mustCompleteRequest(taskID string, generation Generation, terminal, headSHA string) CompleteDeliveryRequest {
	return CompleteDeliveryRequest{
		OperationID:        "op-complete-" + taskID,
		Actor:              Actor{ID: "test", Rank: "general"},
		TaskID:             taskID,
		ExpectedGeneration: generation,
		Terminal:           terminal,
		HeadSHA:            headSHA,
		Identity: ProviderIdentitySnapshot{
			Provider: "github",
			Owner:    "owner",
			Repo:     "repo",
			Number:   42,
			URL:      "https://github.com/owner/repo/pull/42",
			BaseRef:  "main",
			HeadRef:  "feature/test",
			HeadSHA:  headSHA,
		},
		Reason: "terminal report",
	}
}

// preparedTask seeds one queued task and a delivery preparation at head.
func preparedTask(t *testing.T, a *Authority, taskID, headSHA string) {
	t.Helper()
	createTask(t, a, taskID)
	if _, err := a.PrepareDelivery(mustPrepareRequest(taskID, 1, headSHA)); err != nil {
		t.Fatalf("PrepareDelivery: %v", err)
	}
}

// --- PrepareDelivery ---

func TestPrepareDeliveryCommitsGenerationBoundRecord(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	res, err := a.PrepareDelivery(mustPrepareRequest("t1", 1, head))
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != "t1" || res.Generation != 1 || res.Revision != 2 || res.Phase != PhaseQueued || res.Replayed {
		t.Fatalf("prepare result = %+v, want revision 2 queued", res)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("revision = %d, want 2", agg.Revision)
	}
	if agg.Phase != PhaseQueued {
		t.Fatalf("phase changed to %s, want queued unchanged", agg.Phase)
	}
	if agg.DeliveryPrepare == nil {
		t.Fatal("delivery prepare record missing after prepare")
	}
	if agg.DeliveryPrepare.State != DeliveryPrepareStateReviewReady {
		t.Fatalf("prepare state = %q, want %q", agg.DeliveryPrepare.State, DeliveryPrepareStateReviewReady)
	}
	if agg.DeliveryPrepare.HeadSHA != head {
		t.Fatalf("prepared head = %q, want %q", agg.DeliveryPrepare.HeadSHA, head)
	}
	if agg.DeliveryPrepare.Identity.Provider != "github" ||
		agg.DeliveryPrepare.Identity.URL != "https://github.com/owner/repo/pull/42" ||
		agg.DeliveryPrepare.Identity.HeadSHA != head {
		t.Fatalf("provider identity = %+v", agg.DeliveryPrepare.Identity)
	}
	if agg.DeliveryPrepare.Preparer != "test" || agg.DeliveryPrepare.PreparedAt <= 0 {
		t.Fatalf("preparer/prepared-at = %+v", agg.DeliveryPrepare)
	}

	// One typed delivery-prepare audit event committed with the mutation.
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var prepareEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.Kind == AuditDeliveryPrepare {
			prepareEvents = append(prepareEvents, ev)
		}
	}
	if len(prepareEvents) != 1 {
		t.Fatalf("delivery-prepare audit events = %d, want 1", len(prepareEvents))
	}
	if prepareEvents[0].OperationID != "op-prepare-t1" || prepareEvents[0].Actor.ID != "test" ||
		prepareEvents[0].TaskID != "t1" || prepareEvents[0].Generation != 1 {
		t.Fatalf("delivery-prepare audit event = %+v", prepareEvents[0])
	}

	// A durable receipt pins the operation.
	var pinned *Receipt
	for i := range v.Receipts {
		if v.Receipts[i].OperationID == "op-prepare-t1" {
			pinned = &v.Receipts[i]
		}
	}
	if pinned == nil || pinned.Revision != 2 || pinned.Generation != 1 {
		t.Fatalf("receipts = %+v, want pinned op-prepare-t1 revision 2", v.Receipts)
	}
}

func TestPrepareDeliveryGenerationFence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.PrepareDelivery(mustPrepareRequest("t1", 7, head)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation error = %v, want ErrConflict", err)
	}
	if _, err := a.PrepareDelivery(mustPrepareRequest("missing", 1, head)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryPrepare != nil || agg.Revision != 1 {
		t.Fatalf("failed prepare mutated the aggregate: %+v", agg)
	}
}

// TestPrepareDeliveryFailedVerificationLeavesPhaseUnchanged proves a request
// that fails validation (the caller-side provider verification analogue)
// commits nothing: the aggregate keeps its prior phase, revision, and no
// prepare record.
func TestPrepareDeliveryFailedVerificationLeavesPhaseUnchanged(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	bad := mustPrepareRequest("t1", 1, strings.Repeat("a", 40))
	bad.Identity.HeadSHA = strings.Repeat("b", 40) // identity head disagrees with requested head
	if _, err := a.PrepareDelivery(bad); err == nil {
		t.Fatal("expected validation error for mismatched identity head")
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseQueued || agg.Revision != 1 || agg.DeliveryPrepare != nil {
		t.Fatalf("failed verification changed the prior authoritative state: %+v", agg)
	}
}

func TestPrepareDeliverySameOpReplayIdempotent(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	res1, err := a.PrepareDelivery(mustPrepareRequest("t1", 1, head))
	if err != nil {
		t.Fatal(err)
	}
	res2, err := a.PrepareDelivery(mustPrepareRequest("t1", 1, head))
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Replayed || res2.Revision != res1.Revision {
		t.Fatalf("replay result = %+v, want replayed at original revision", res2)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("replay advanced revision to %d, want 2", agg.Revision)
	}
	v, _ := a.store.View()
	var prepareEvents int
	for _, ev := range v.Audit {
		if ev.Kind == AuditDeliveryPrepare {
			prepareEvents++
		}
	}
	if prepareEvents != 1 {
		t.Fatalf("delivery-prepare audit events after replay = %d, want 1", prepareEvents)
	}
}

func TestPrepareDeliveryChangedDigestNonRetryableConflict(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.PrepareDelivery(mustPrepareRequest("t1", 1, head)); err != nil {
		t.Fatal(err)
	}
	changed := mustPrepareRequest("t1", 1, strings.Repeat("b", 40)) // same op ID, different intent
	if _, err := a.PrepareDelivery(changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed digest error = %v, want ErrOperationConflict", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryPrepare.HeadSHA != head {
		t.Fatalf("changed-digest conflict mutated the record head to %q", agg.DeliveryPrepare.HeadSHA)
	}
}

// TestPrepareDeliveryRePrepareRequiresAcknowledgedPriorHead proves a changed
// head must be re-prepared explicitly (expected prior head acknowledged);
// silent reuse fails closed.
func TestPrepareDeliveryRePrepareRequiresAcknowledgedPriorHead(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	oldHead := strings.Repeat("a", 40)
	newHead := strings.Repeat("b", 40)

	if _, err := a.PrepareDelivery(mustPrepareRequest("t1", 1, oldHead)); err != nil {
		t.Fatal(err)
	}
	// Changed head without acknowledging the prior head conflicts.
	silent := mustPrepareRequest("t1", 1, newHead)
	silent.OperationID = "op-prepare-t1-silent"
	if _, err := a.PrepareDelivery(silent); !errors.Is(err, ErrConflict) {
		t.Fatalf("silent re-prepare error = %v, want ErrConflict", err)
	}

	// Acknowledging the prior head re-prepares explicitly.
	req := mustPrepareRequest("t1", 1, newHead)
	req.OperationID = "op-prepare-t1-again"
	req.ExpectedPriorHead = oldHead
	res, err := a.PrepareDelivery(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 3 {
		t.Fatalf("re-prepare revision = %d, want 3", res.Revision)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryPrepare.HeadSHA != newHead {
		t.Fatalf("re-prepare head = %q, want %q", agg.DeliveryPrepare.HeadSHA, newHead)
	}
}

// TestPrepareDeliverySameIdentityNoOp proves re-preparing the identical
// identity and head is an in-value no-op that does not advance the Revision.
func TestPrepareDeliverySameIdentityNoOp(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.PrepareDelivery(mustPrepareRequest("t1", 1, head)); err != nil {
		t.Fatal(err)
	}
	req := mustPrepareRequest("t1", 1, head)
	req.OperationID = "op-prepare-t1-again"
	req.ExpectedPriorHead = head
	res, err := a.PrepareDelivery(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 2 {
		t.Fatalf("no-op re-prepare advanced revision to %d, want 2", res.Revision)
	}
	agg, _ := a.Get("t1")
	if agg.Revision != 2 {
		t.Fatalf("aggregate revision = %d, want 2", agg.Revision)
	}
	v, _ := a.store.View()
	var prepareEvents int
	for _, ev := range v.Audit {
		if ev.Kind == AuditDeliveryPrepare {
			prepareEvents++
		}
	}
	if prepareEvents != 1 {
		t.Fatalf("delivery-prepare audit events after no-op = %d, want 1", prepareEvents)
	}
}

// --- CompleteDelivery ---

func TestCompleteDeliveryCommitsTerminalRecord(t *testing.T) {
	a := newTestAuthority(t)
	head := strings.Repeat("a", 40)
	preparedTask(t, a, "t1", head)

	res, err := a.CompleteDelivery(mustCompleteRequest("t1", 1, DeliveryTerminalDone, head))
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != "t1" || res.Generation != 1 || res.Revision != 3 || res.Phase != PhaseQueued || res.Replayed {
		t.Fatalf("complete result = %+v, want revision 3 queued", res)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 3 {
		t.Fatalf("revision = %d, want 3", agg.Revision)
	}
	if agg.Phase != PhaseQueued {
		t.Fatalf("phase changed to %s, want queued unchanged (delivery evidence is not a competing phase mutation)", agg.Phase)
	}
	if agg.DeliveryTerminal == nil {
		t.Fatal("delivery terminal record missing after complete")
	}
	if agg.DeliveryTerminal.Terminal != DeliveryTerminalDone {
		t.Fatalf("terminal = %q, want %q", agg.DeliveryTerminal.Terminal, DeliveryTerminalDone)
	}
	if agg.DeliveryTerminal.HeadSHA != head {
		t.Fatalf("terminal head = %q, want %q", agg.DeliveryTerminal.HeadSHA, head)
	}
	if agg.DeliveryTerminal.Identity.URL != "https://github.com/owner/repo/pull/42" {
		t.Fatalf("terminal identity = %+v", agg.DeliveryTerminal.Identity)
	}
	if agg.DeliveryTerminal.Completer != "test" || agg.DeliveryTerminal.CompletedAt <= 0 {
		t.Fatalf("completer/completed-at = %+v", agg.DeliveryTerminal)
	}

	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var terminalEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.Kind == AuditDeliveryTerminal {
			terminalEvents = append(terminalEvents, ev)
		}
	}
	if len(terminalEvents) != 1 {
		t.Fatalf("delivery-terminal audit events = %d, want 1", len(terminalEvents))
	}
	if terminalEvents[0].OperationID != "op-complete-t1" || terminalEvents[0].TaskID != "t1" || terminalEvents[0].Generation != 1 {
		t.Fatalf("delivery-terminal audit event = %+v", terminalEvents[0])
	}
	var pinned *Receipt
	for i := range v.Receipts {
		if v.Receipts[i].OperationID == "op-complete-t1" {
			pinned = &v.Receipts[i]
		}
	}
	if pinned == nil || pinned.Revision != 3 || pinned.Generation != 1 {
		t.Fatalf("receipts = %+v, want pinned op-complete-t1 revision 3", v.Receipts)
	}
}

// TestCompleteDeliveryResolvedNeverCompletion proves resolved is never
// accepted as a delivery terminal state: the request validation rejects it,
// and completing delivery from a resolved/stale generation fails closed.
func TestCompleteDeliveryResolvedNeverCompletion(t *testing.T) {
	a := newTestAuthority(t)
	head := strings.Repeat("a", 40)
	preparedTask(t, a, "t1", head)

	// Target "resolved" is rejected as a delivery terminal transition.
	req := mustCompleteRequest("t1", 1, "resolved", head)
	if _, err := a.CompleteDelivery(req); err == nil {
		t.Fatal("expected validation error for resolved terminal state")
	}

	// A task resolved via the lifecycle (Supersede/reopen resolution) can
	// never complete delivery.
	createTask(t, a, "t2")
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-complete-t2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t2", ExpectedGeneration: 1, To: PhaseResolved, Reason: "resolve",
	}); err != nil {
		t.Fatalf("Complete(resolved): %v", err)
	}
	if _, err := a.PrepareDelivery(mustPrepareRequest("t2", 1, head)); err != nil {
		t.Fatalf("PrepareDelivery on resolved task: %v", err)
	}
	attempt := mustCompleteRequest("t2", 1, DeliveryTerminalDone, head)
	attempt.OperationID = "op-complete-t2-delivery"
	if _, err := a.CompleteDelivery(attempt); !errors.Is(err, ErrConflict) {
		t.Fatalf("complete from resolved error = %v, want ErrConflict", err)
	}

	agg, err := a.Get("t2")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseResolved || agg.DeliveryTerminal != nil {
		t.Fatalf("resolved task mutated by failed completion: %+v", agg)
	}
}

// TestCompleteDeliveryRetiredFailsClosed proves a retired generation never
// accepts delivery completion.
func TestCompleteDeliveryRetiredFailsClosed(t *testing.T) {
	a := newTestAuthority(t)
	head := strings.Repeat("a", 40)
	preparedTask(t, a, "t1", head)

	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-retire-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseResolved, Reason: "resolve",
	}); err != nil {
		t.Fatalf("Complete(resolved): %v", err)
	}
	// Supersede creates the resolution; the generation stays resolved.
	if _, err := a.CompleteDelivery(mustCompleteRequest("t1", 1, DeliveryTerminalDone, head)); !errors.Is(err, ErrConflict) {
		t.Fatalf("complete from retired/resolved error = %v, want ErrConflict", err)
	}
}

func TestCompleteDeliveryExactHeadBinding(t *testing.T) {
	a := newTestAuthority(t)
	preparedHead := strings.Repeat("a", 40)
	preparedTask(t, a, "t1", preparedHead)

	// Terminal evidence with a different head fails closed (evidence binds
	// the exact prepared head).
	otherHead := strings.Repeat("b", 40)
	if _, err := a.CompleteDelivery(mustCompleteRequest("t1", 1, DeliveryTerminalDone, otherHead)); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed head error = %v, want ErrConflict", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryTerminal != nil || agg.Revision != 2 {
		t.Fatalf("changed-head completion mutated the aggregate: %+v", agg)
	}
}

// TestCompleteDeliveryRequiresPrepare proves delivery completion without a
// prior preparation fails closed (run pr-check first).
func TestCompleteDeliveryRequiresPrepare(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	head := strings.Repeat("a", 40)

	if _, err := a.CompleteDelivery(mustCompleteRequest("t1", 1, DeliveryTerminalDone, head)); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("unprepared completion error = %v, want ErrPrecondition", err)
	}
}

// TestCompleteDeliveryIdentityBindsSamePR proves terminal evidence must
// identify the same PR as the preparation.
func TestCompleteDeliveryIdentityBindsSamePR(t *testing.T) {
	a := newTestAuthority(t)
	head := strings.Repeat("a", 40)
	preparedTask(t, a, "t1", head)

	req := mustCompleteRequest("t1", 1, DeliveryTerminalDone, head)
	req.Identity.URL = "https://github.com/owner/repo/pull/99"
	if _, err := a.CompleteDelivery(req); !errors.Is(err, ErrConflict) {
		t.Fatalf("different PR error = %v, want ErrConflict", err)
	}
}

// TestCompleteDeliveryDeliveredAndDoneDistinct proves delivered and done are
// distinct terminal transitions and a generation accepts exactly one: the
// second distinct terminal transition conflicts while replaying the same
// terminal state for the same head is an idempotent no-op.
func TestCompleteDeliveryDeliveredAndDoneDistinct(t *testing.T) {
	a := newTestAuthority(t)
	head := strings.Repeat("a", 40)
	preparedTask(t, a, "t1", head)

	if _, err := a.CompleteDelivery(mustCompleteRequest("t1", 1, DeliveryTerminalDelivered, head)); err != nil {
		t.Fatalf("complete delivered: %v", err)
	}
	// Same terminal state + same head under a fresh op ID is a no-op.
	again := mustCompleteRequest("t1", 1, DeliveryTerminalDelivered, head)
	again.OperationID = "op-complete-t1-again"
	res, err := a.CompleteDelivery(again)
	if err != nil {
		t.Fatalf("no-op replay: %v", err)
	}
	if res.Revision != 3 {
		t.Fatalf("no-op replay advanced revision to %d, want 3", res.Revision)
	}
	// A distinct terminal transition (done) conflicts: one per generation.
	doneAttempt := mustCompleteRequest("t1", 1, DeliveryTerminalDone, head)
	doneAttempt.OperationID = "op-complete-t1-done"
	if _, err := a.CompleteDelivery(doneAttempt); !errors.Is(err, ErrConflict) {
		t.Fatalf("second terminal transition error = %v, want ErrConflict", err)
	}
}

func TestCompleteDeliverySameOpReplayIdempotent(t *testing.T) {
	a := newTestAuthority(t)
	head := strings.Repeat("a", 40)
	preparedTask(t, a, "t1", head)

	res1, err := a.CompleteDelivery(mustCompleteRequest("t1", 1, DeliveryTerminalDone, head))
	if err != nil {
		t.Fatal(err)
	}
	res2, err := a.CompleteDelivery(mustCompleteRequest("t1", 1, DeliveryTerminalDone, head))
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Replayed || res2.Revision != res1.Revision {
		t.Fatalf("replay result = %+v, want replayed at original revision", res2)
	}
	v, _ := a.store.View()
	var terminalEvents int
	for _, ev := range v.Audit {
		if ev.Kind == AuditDeliveryTerminal {
			terminalEvents++
		}
	}
	if terminalEvents != 1 {
		t.Fatalf("delivery-terminal audit events after replay = %d, want 1", terminalEvents)
	}
}

func TestCompleteDeliveryChangedDigestConflict(t *testing.T) {
	a := newTestAuthority(t)
	head := strings.Repeat("a", 40)
	preparedTask(t, a, "t1", head)

	if _, err := a.CompleteDelivery(mustCompleteRequest("t1", 1, DeliveryTerminalDone, head)); err != nil {
		t.Fatal(err)
	}
	changed := mustCompleteRequest("t1", 1, DeliveryTerminalDelivered, head) // same op ID, different terminal
	if _, err := a.CompleteDelivery(changed); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed digest error = %v, want ErrOperationConflict", err)
	}
}

func TestCompleteDeliveryStaleGenerationFailsClosed(t *testing.T) {
	a := newTestAuthority(t)
	head := strings.Repeat("a", 40)
	preparedTask(t, a, "t1", head)

	if _, err := a.CompleteDelivery(mustCompleteRequest("t1", 7, DeliveryTerminalDone, head)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation error = %v, want ErrConflict", err)
	}
	if _, err := a.CompleteDelivery(mustCompleteRequest("missing", 1, DeliveryTerminalDone, head)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}
}
