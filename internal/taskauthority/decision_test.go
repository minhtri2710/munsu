package taskauthority

import (
	"errors"
	"testing"
)

// stageDecisionRequired stages a decision-required interpretation for the
// given task through the real Authority and returns the staged record. The
// decision's matching hold (Key + "-hold") is committed in the same Store
// transaction (Task 5.1).
func stageDecisionRequired(t *testing.T, a *Authority, taskID string) DispatchInterpretation {
	t.Helper()
	res, err := a.InterpretDispatch(interpretRequest(
		"op-interpret", []string{taskID},
		[]DispatchDependency{{TaskID: taskID, DependsOn: []string{"missing"}, State: "queued"}},
		DispatchAutonomyManual,
	))
	if err != nil {
		t.Fatalf("InterpretDispatch: %v", err)
	}
	if res.Record.Outcome != DispatchInterpretationDecisionRequired || res.Record.DecisionKey == "" {
		t.Fatalf("record = %+v, want decision-required", res.Record)
	}
	return res.Record
}

// TestAuthorityResolveDecisionResolvesAndReleasesHoldAtomically proves one
// ResolveDecision operation marks the Dispatch Decision resolved and releases
// its matching task-scoped hold in the same Store transaction, and that
// resolving never auto-starts queued work (Task 5.2 acceptance 2): the task
// stays queued until an explicit Start after the hold is gone.
func TestAuthorityResolveDecisionResolvesAndReleasesHoldAtomically(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	record := stageDecisionRequired(t, a, "t1")

	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	hold, ok := v.Hold(record.DecisionKey + "-hold")
	if !ok || hold.ReleasedAt != 0 {
		t.Fatalf("staged hold = %+v ok=%v, want active", hold, ok)
	}
	if _, err := a.Start(StartRequest{
		OperationID: "op-start-before", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	}); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("start under decision hold = %v, want ErrDispatchHeld", err)
	}

	res, err := a.ResolveDecision(ResolveDecisionRequest{
		OperationID: "op-resolve", Actor: Actor{ID: "test", Rank: "general"},
		Key: record.DecisionKey, Answer: "run t1 first",
	})
	if err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}
	if res.Replayed {
		t.Fatal("first resolve marked as replayed")
	}

	v, err = a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	decision, ok := v.Decision(record.DecisionKey)
	if !ok || decision.ResolvedAt == 0 || decision.Answer != "run t1 first" {
		t.Fatalf("decision after resolve = %+v ok=%v", decision, ok)
	}
	hold, ok = v.Hold(record.DecisionKey + "-hold")
	if !ok || hold.ReleasedAt == 0 {
		t.Fatalf("matching hold after resolve = %+v ok=%v, want released", hold, ok)
	}

	// Resolving must not auto-start queued work.
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseQueued || agg.Revision != FirstRevision {
		t.Fatalf("resolve mutated queued task: %+v", agg)
	}

	// The released hold no longer blocks an explicit start.
	if _, err := a.Start(StartRequest{
		OperationID: "op-start-after", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	}); err != nil {
		t.Fatalf("start after resolve: %v", err)
	}
}

// TestAuthorityResolveDecisionReplayIsIdempotent proves repeating the same
// Operation ID with the same answer replays without a second mutation or a
// second audit event, and that an already-resolved decision with the same
// answer under a new Operation ID is a successful no-op.
func TestAuthorityResolveDecisionReplayIsIdempotent(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	record := stageDecisionRequired(t, a, "t1")

	req := ResolveDecisionRequest{
		OperationID: "op-resolve", Actor: Actor{ID: "test", Rank: "general"},
		Key: record.DecisionKey, Answer: "run t1 first",
	}
	if _, err := a.ResolveDecision(req); err != nil {
		t.Fatal(err)
	}
	replayed, err := a.ResolveDecision(req)
	if err != nil {
		t.Fatalf("repeated resolve: %v", err)
	}
	if !replayed.Replayed {
		t.Fatal("repeated identical resolve was not replayed")
	}

	// A fresh Operation ID with the same answer is a successful no-op.
	again, err := a.ResolveDecision(ResolveDecisionRequest{
		OperationID: "op-resolve-bis", Actor: Actor{ID: "test", Rank: "general"},
		Key: record.DecisionKey, Answer: "run t1 first",
	})
	if err != nil {
		t.Fatalf("re-resolve same answer: %v", err)
	}
	if again.Replayed {
		t.Fatal("fresh operation must not be marked replayed")
	}

	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	decision, _ := v.Decision(record.DecisionKey)
	if decision.Answer != "run t1 first" || decision.ResolvedAt == 0 {
		t.Fatalf("decision = %+v", decision)
	}
	// Exactly one decision-resolution audit event for the original operation.
	seen := 0
	for _, ev := range v.Audit {
		if ev.OperationID == "op-resolve" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("resolve audit events for op-resolve = %d, want 1", seen)
	}
}

// TestAuthorityResolveDecisionOperationConflict proves reusing the same
// Operation ID with a different intent digest (a changed answer) is a typed
// non-retryable conflict, and that resolving an already-resolved decision
// with a different answer under a new Operation ID conflicts.
func TestAuthorityResolveDecisionOperationConflict(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	record := stageDecisionRequired(t, a, "t1")

	if _, err := a.ResolveDecision(ResolveDecisionRequest{
		OperationID: "op-resolve", Actor: Actor{ID: "test", Rank: "general"},
		Key: record.DecisionKey, Answer: "run t1 first",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResolveDecision(ResolveDecisionRequest{
		OperationID: "op-resolve", Actor: Actor{ID: "test", Rank: "general"},
		Key: record.DecisionKey, Answer: "run t2 first",
	}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused operation with changed digest = %v, want ErrOperationConflict", err)
	}
	if _, err := a.ResolveDecision(ResolveDecisionRequest{
		OperationID: "op-resolve-2", Actor: Actor{ID: "test", Rank: "general"},
		Key: record.DecisionKey, Answer: "run t2 first",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("already-resolved with different answer = %v, want ErrConflict", err)
	}
}

// TestAuthorityResolveDecisionMissingDecision proves resolving an unknown
// decision key fails with the typed not-found error, even when a plain hold
// with the same identity exists: a plain hold is released by ReleaseHold, not
// by ResolveDecision.
func TestAuthorityResolveDecisionMissingDecision(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.CreateHold(CreateHoldRequest{
		OperationID: "op-hold", Actor: Actor{ID: "test", Rank: "general"},
		ID: "t1-decision-approach", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionStart, DispatchActionSpawn, DispatchActionHandoff},
		Reason:  "choose approach",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResolveDecision(ResolveDecisionRequest{
		OperationID: "op-resolve", Actor: Actor{ID: "test", Rank: "general"},
		Key: "t1-decision-approach", Answer: "choose react",
	}); !errors.Is(err, ErrDecisionNotFound) {
		t.Fatalf("resolve missing decision = %v, want ErrDecisionNotFound", err)
	}
	// The plain hold is untouched: releasing it is ReleaseHold's job.
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	hold, _ := v.Hold("t1-decision-approach")
	if hold.ReleasedAt != 0 {
		t.Fatal("ResolveDecision released a plain hold without a decision record")
	}
}

// TestAuthorityResolveDecisionWithoutMatchingHold proves a decision record
// without its matching hold still resolves successfully: releasing the
// matching hold is conditional on it existing.
func TestAuthorityResolveDecisionWithoutMatchingHold(t *testing.T) {
	store := newMemStore()
	a := New(store)
	seed := DispatchDecision{
		SchemaVersion:    TaskAuthoritySchema,
		Key:              "dec-without-hold",
		InterpretationID: "interpretation-x",
		Reason:           "material dispatch ambiguity",
		CreatedAt:        a.now().UnixNano(),
	}
	if _, err := store.Update(op("op-seed", "x"), func(tx *Tx) error {
		return tx.PutDecision(seed)
	}); err != nil {
		t.Fatal(err)
	}
	res, err := a.ResolveDecision(ResolveDecisionRequest{
		OperationID: "op-resolve", Actor: Actor{ID: "test", Rank: "general"},
		Key: "dec-without-hold", Answer: "approved",
	})
	if err != nil {
		t.Fatalf("ResolveDecision without hold: %v", err)
	}
	if res.Replayed {
		t.Fatal("first resolve marked as replayed")
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	decision, ok := v.Decision("dec-without-hold")
	if !ok || decision.ResolvedAt == 0 || decision.Answer != "approved" {
		t.Fatalf("decision = %+v ok=%v", decision, ok)
	}
}

// TestAllDispatchPathsEvaluateAuthorityHolds proves every human and automatic
// dispatch path evaluates durable Authority holds (Task 5.2 acceptance 3):
// Start, ConfirmSpawn, InterpretDispatch readiness, and the Readiness query
// all report the same active hold, and release unblocks them without
// auto-starting.
func TestAllDispatchPathsEvaluateAuthorityHolds(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.CreateHold(CreateHoldRequest{
		OperationID: "op-hold", Actor: Actor{ID: "test", Rank: "general"},
		ID: "pause-t1", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}},
		Actions: []DispatchAction{DispatchActionStart, DispatchActionSpawn, DispatchActionHandoff},
		Reason:  "pause",
	}); err != nil {
		t.Fatal(err)
	}

	// Start is blocked by the hold.
	if _, err := a.Start(StartRequest{
		OperationID: "op-start", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	}); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("start = %v, want ErrDispatchHeld", err)
	}

	// Readiness reports the hold as a blocking reason.
	ready, err := a.Readiness("t1")
	if err != nil {
		t.Fatal(err)
	}
	if ready.Ready || !containsReadinessReason(ready.BlockingReasons, ReadinessDispatchHold) {
		t.Fatalf("readiness = %+v, want dispatch-hold blocking", ready)
	}

	// InterpretDispatch readiness reflects the same hold inside its record.
	interp, err := a.InterpretDispatch(interpretRequest("op-interpret", []string{"t1"}, nil, DispatchAutonomySafeReinterpretation))
	if err != nil {
		t.Fatal(err)
	}
	if len(interp.Record.ComputedReadiness) != 1 ||
		!containsString(interp.Record.ComputedReadiness[0].BlockingReasons, string(ReadinessDispatchHold)) {
		t.Fatalf("interpretation readiness = %+v, want dispatch-hold blocking", interp.Record.ComputedReadiness)
	}

	// ConfirmSpawn is blocked by the hold (after a bound worktree).
	if _, err := a.BindWorktree(BindWorktreeRequest{
		OperationID: "op-bind", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustWorktreeBinding(t, "lease-1", "fence-1"), Reason: "bind",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmSpawn(ConfirmSpawnRequest{
		OperationID: "op-spawn", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1,
		Binding: mustEndpointBinding(t, "pane-1", "fence-1"), Reason: "spawn",
	}); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("confirm spawn = %v, want ErrDispatchHeld", err)
	}

	// Releasing the hold unblocks start but never auto-starts.
	if _, err := a.ReleaseHold(ReleaseHoldRequest{
		OperationID: "op-release", Actor: Actor{ID: "test", Rank: "general"},
		ID: "pause-t1", Reason: "approved",
	}); err != nil {
		t.Fatal(err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != PhaseQueued {
		t.Fatalf("release auto-started task: %+v", agg)
	}
	if _, err := a.Start(StartRequest{
		OperationID: "op-start-2", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, Reason: "go",
	}); err != nil {
		t.Fatalf("start after release: %v", err)
	}
}

func containsReadinessReason(reasons []ReadinessReason, want ReadinessReason) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}
