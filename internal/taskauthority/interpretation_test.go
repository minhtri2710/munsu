package taskauthority

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func interpretRequest(opID string, requested []string, deps []DispatchDependency, autonomy DispatchAutonomy) InterpretDispatchRequest {
	return InterpretDispatchRequest{
		OperationID:    opID,
		Actor:          Actor{ID: "test", Rank: "general"},
		RequestedOrder: requested,
		Dependencies:   deps,
		Autonomy:       autonomy,
	}
}

func TestAuthorityInterpretDispatchDeterministicIdentity(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "parent")
	createTask(t, a, "child")

	req := interpretRequest("op-1", []string{"parent", "child"}, []DispatchDependency{{TaskID: "parent", DependsOn: []string{"child"}}}, DispatchAutonomySafeReinterpretation)
	first, err := a.InterpretDispatch(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.Record.ID, "interpretation-") {
		t.Fatalf("record id = %q, want interpretation- prefix", first.Record.ID)
	}
	if len(first.Record.DependencySnapshotDigest) != 64 {
		t.Fatalf("dependency digest = %q, want 64-hex", first.Record.DependencySnapshotDigest)
	}

	// A different operation ID with the identical request must produce the
	// same durable interpretation identity: the identity is deterministic
	// across operations, so the second commit replaces the first record.
	second, err := a.InterpretDispatch(interpretRequest("op-2", []string{"parent", "child"}, []DispatchDependency{{TaskID: "parent", DependsOn: []string{"child"}}}, DispatchAutonomySafeReinterpretation))
	if err != nil {
		t.Fatal(err)
	}
	if second.Record.ID != first.Record.ID || second.Record.DependencySnapshotDigest != first.Record.DependencySnapshotDigest {
		t.Fatalf("identity not deterministic: %+v vs %+v", first.Record, second.Record)
	}

	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Interpretations) != 1 {
		t.Fatalf("interpretations = %d, want 1 (deterministic identity collapses identical requests)", len(v.Interpretations))
	}
	if len(v.Receipts) != 4 { // create parent, create child, and both interpretation operations
		t.Fatalf("receipts = %d, want 4", len(v.Receipts))
	}
}

func TestAuthorityInterpretDispatchAcceptsMatchingOrder(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "a")
	createTask(t, a, "b")

	res, err := a.InterpretDispatch(interpretRequest("op-1", []string{"a", "b"}, []DispatchDependency{{TaskID: "b", DependsOn: []string{"a"}}}, DispatchAutonomySafeReinterpretation))
	if err != nil {
		t.Fatal(err)
	}
	if res.Record.Outcome != DispatchInterpretationAccepted {
		t.Fatalf("outcome = %s, want accepted", res.Record.Outcome)
	}
	if res.Record.DecisionKey != "" {
		t.Fatalf("accepted record staged a decision key %q", res.Record.DecisionKey)
	}
	if len(res.SelectedTasks) != 2 || res.SelectedTasks[0] != "a" || res.SelectedTasks[1] != "b" {
		t.Fatalf("selected = %v", res.SelectedTasks)
	}
}

func TestAuthorityInterpretDispatchReinterpretsSafely(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "parent")
	createTask(t, a, "child")

	res, err := a.InterpretDispatch(interpretRequest("op-1", []string{"parent", "child"}, []DispatchDependency{{TaskID: "parent", DependsOn: []string{"child"}}}, DispatchAutonomySafeReinterpretation))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SelectedTasks) != 2 || res.SelectedTasks[0] != "child" || res.SelectedTasks[1] != "parent" {
		t.Fatalf("selected = %v, want dependency-ordered child,parent", res.SelectedTasks)
	}
	if res.Record.Outcome != DispatchInterpretationReinterpreted {
		t.Fatalf("outcome = %s, want reinterpreted under safe autonomy", res.Record.Outcome)
	}
	if res.Record.DecisionKey != "" {
		t.Fatalf("safe reinterpretation staged a decision %q", res.Record.DecisionKey)
	}
}

func TestAuthorityInterpretDispatchRequiresDecisionUnderManualAutonomy(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "parent")
	createTask(t, a, "child")

	res, err := a.InterpretDispatch(interpretRequest("op-1", []string{"parent", "child"}, []DispatchDependency{{TaskID: "parent", DependsOn: []string{"child"}}}, DispatchAutonomyManual))
	if err != nil {
		t.Fatal(err)
	}
	if res.Record.Outcome != DispatchInterpretationDecisionRequired || res.Record.DecisionKey == "" {
		t.Fatalf("record = %+v, want decision-required", res.Record)
	}
}

func TestAuthorityInterpretDispatchMaterialAmbiguityStagesDecisionHoldAudit(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	res, err := a.InterpretDispatch(interpretRequest("op-1", []string{"t1"}, []DispatchDependency{{TaskID: "t1", DependsOn: []string{"missing"}, State: "queued"}}, DispatchAutonomyManual))
	if err != nil {
		t.Fatal(err)
	}
	record := res.Record
	if record.Outcome != DispatchInterpretationDecisionRequired || record.DecisionKey == "" {
		t.Fatalf("record = %+v, want decision-required", record)
	}

	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Interpretations) != 1 {
		t.Fatalf("interpretations = %d, want 1", len(v.Interpretations))
	}

	var decision DispatchDecision
	for _, candidate := range v.Decisions {
		if candidate.Key == record.DecisionKey {
			decision = candidate
		}
	}
	if decision.Key == "" {
		t.Fatalf("decision %q missing from committed view", record.DecisionKey)
	}
	if decision.InterpretationID != record.ID || decision.ResolvedAt != 0 {
		t.Fatalf("decision = %+v", decision)
	}

	hold, ok := v.Hold(record.DecisionKey + "-hold")
	if !ok {
		t.Fatalf("hold %q missing from committed view", record.DecisionKey+"-hold")
	}
	if len(hold.Scope.TaskIDs) != 1 || hold.Scope.TaskIDs[0] != "t1" || hold.ReleasedAt != 0 {
		t.Fatalf("hold = %+v", hold)
	}
	for _, action := range []DispatchAction{DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn} {
		if !containsAction(hold.Actions, action) {
			t.Fatalf("hold actions = %v, missing %s", hold.Actions, action)
		}
	}

	if len(v.Audit) != 2 { // create task + interpretation decision audit
		t.Fatalf("audit = %d events, want 2", len(v.Audit))
	}
	var audit AuditEvent
	for _, ev := range v.Audit {
		if ev.OperationID == "op-1" {
			audit = ev
		}
	}
	if audit.Kind != AuditDispatch || audit.OperationID != "op-1" {
		t.Fatalf("audit = %+v, want one dispatch audit for op-1", v.Audit)
	}
}

func TestAuthorityInterpretDispatchCycleRequiresDecisionWithoutLifecycleMutation(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "a")
	createTask(t, a, "b")

	res, err := a.InterpretDispatch(interpretRequest("op-1", []string{"a", "b"}, []DispatchDependency{{TaskID: "a", DependsOn: []string{"b"}}, {TaskID: "b", DependsOn: []string{"a"}}}, DispatchAutonomySafeReinterpretation))
	if err != nil {
		t.Fatal(err)
	}
	if res.Record.Outcome != DispatchInterpretationDecisionRequired {
		t.Fatalf("cycle outcome = %s, want decision-required", res.Record.Outcome)
	}
	for _, taskID := range []string{"a", "b"} {
		agg, err := a.Get(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if agg.Phase != PhaseQueued || agg.Revision != FirstRevision {
			t.Fatalf("task %s mutated by interpretation: %+v", taskID, agg)
		}
	}
}

func TestAuthorityInterpretDispatchDerivesDependencies(t *testing.T) {
	a := newTestAuthority(t)
	if _, err := a.Create(CreateRequest{
		OperationID: "op-create-parent", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "parent", Owner: "owner", Description: "p", Kind: "ship", Project: "proj",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Create(CreateRequest{
		OperationID: "op-create-child", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "child", Owner: "owner", Description: "c", Kind: "ship", Project: "proj", ParentTaskID: "parent",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := a.InterpretDispatch(interpretRequest("op-1", []string{"child", "parent"}, nil, DispatchAutonomySafeReinterpretation))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SelectedTasks) != 2 || res.SelectedTasks[0] != "parent" || res.SelectedTasks[1] != "child" {
		t.Fatalf("derived-dependency selected = %v, want parent,child", res.SelectedTasks)
	}
	if res.Record.Outcome != DispatchInterpretationReinterpreted {
		t.Fatalf("outcome = %s, want reinterpreted for derived parent-first order", res.Record.Outcome)
	}
}

func TestAuthorityInterpretDispatchReadinessReflectsCanonicalState(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t-queued")
	createTask(t, a, "t-blocked")
	if _, err := a.Block(BlockRequest{
		OperationID: "op-block", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t-blocked", ExpectedGeneration: 1, Detail: "dep", Reason: "dep",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := a.InterpretDispatch(interpretRequest("op-1", []string{"t-queued", "t-blocked"}, nil, DispatchAutonomyManual))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]DispatchReadiness{}
	for _, r := range res.Record.ComputedReadiness {
		byID[r.TaskID] = r
	}
	queued := byID["t-queued"]
	if !queued.Ready || queued.Generation != "1" || len(queued.BlockingReasons) != 0 {
		t.Fatalf("queued readiness = %+v", queued)
	}
	blocked := byID["t-blocked"]
	if blocked.Ready || len(blocked.BlockingReasons) != 1 || blocked.BlockingReasons[0] != string(ReadinessBlocked) {
		t.Fatalf("blocked readiness = %+v", blocked)
	}
}

func TestAuthorityInterpretDispatchMissingTaskFails(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	_, err := a.InterpretDispatch(interpretRequest("op-1", []string{"missing"}, nil, DispatchAutonomyManual))
	if err == nil || !strings.Contains(err.Error(), "no authoritative aggregate") {
		t.Fatalf("err = %v, want no authoritative aggregate", err)
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Interpretations) != 0 {
		t.Fatalf("failed interpretation must not stage a record: %+v", v.Interpretations)
	}
	for _, receipt := range v.Receipts {
		if receipt.OperationID == "op-1" {
			t.Fatalf("failed interpretation committed a receipt: %+v", receipt)
		}
	}
}

func TestAuthorityInterpretDispatchIdempotentReplay(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "parent")
	createTask(t, a, "child")

	req := interpretRequest("op-1", []string{"parent", "child"}, []DispatchDependency{{TaskID: "parent", DependsOn: []string{"child"}}}, DispatchAutonomySafeReinterpretation)
	first, err := a.InterpretDispatch(req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed {
		t.Fatal("first interpretation marked replayed")
	}
	second, err := a.InterpretDispatch(req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed {
		t.Fatal("repeated interpretation was not replayed")
	}
	if second.Record.ID != first.Record.ID || second.Record.CreatedAt != first.Record.CreatedAt {
		t.Fatalf("replayed record differs: %+v vs %+v", first.Record, second.Record)
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Interpretations) != 1 {
		t.Fatalf("replay created %d interpretation records, want 1", len(v.Interpretations))
	}
	var receipt Receipt
	for _, candidate := range v.Receipts {
		if candidate.OperationID == "op-1" {
			receipt = candidate
		}
	}
	if receipt.InterpretationID != first.Record.ID {
		t.Fatalf("receipt interpretation id = %q, want %q", receipt.InterpretationID, first.Record.ID)
	}
}

func TestAuthorityInterpretDispatchDecisionRequiredReplays(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	req := interpretRequest("op-1", []string{"t1"}, []DispatchDependency{{TaskID: "t1", DependsOn: []string{"missing"}}}, DispatchAutonomyManual)
	first, err := a.InterpretDispatch(req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Record.Outcome != DispatchInterpretationDecisionRequired {
		t.Fatalf("outcome = %s", first.Record.Outcome)
	}
	second, err := a.InterpretDispatch(req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.Record.ID != first.Record.ID {
		t.Fatalf("replay = %+v, want same record replayed", second)
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Decisions) != 1 || len(v.Holds) != 1 {
		t.Fatalf("replay duplicated staged records: %+v", v)
	}
	dispatchAudits := 0
	for _, ev := range v.Audit {
		if ev.OperationID == "op-1" {
			dispatchAudits++
		}
	}
	if dispatchAudits != 1 {
		t.Fatalf("op-1 audit events = %d, want 1 (no duplicate on replay)", dispatchAudits)
	}
}

func TestAuthorityInterpretDispatchOperationConflict(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")

	req := interpretRequest("op-1", []string{"t1"}, nil, DispatchAutonomyManual)
	if _, err := a.InterpretDispatch(req); err != nil {
		t.Fatal(err)
	}
	changed := req
	changed.Autonomy = DispatchAutonomySafeReinterpretation
	_, err := a.InterpretDispatch(changed)
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("reused op id with different intent = %v, want ErrOperationConflict", err)
	}
}

// TestAuthorityInterpretDispatchStagesAtomically is the deterministic barrier
// proof for the decision-required path: while the interpretation transaction
// is between its staged evaluation and commit, no concurrent dispatch
// mutation can observe or interleave with a partially staged Decision or
// Hold. The staged set (interpretation + decision + hold + audit) commits as
// one unit.
func TestAuthorityInterpretDispatchStagesAtomically(t *testing.T) {
	store := newMemStore()
	a := New(store)
	createTask(t, a, "t1")

	started := make(chan struct{})
	proceed := make(chan struct{})
	var signalOnce sync.Once
	store.beforeCommit = func() error {
		signalOnce.Do(func() { close(started) })
		<-proceed
		return nil
	}

	interpretDone := make(chan error, 1)
	go func() {
		_, err := a.InterpretDispatch(interpretRequest("op-1", []string{"t1"}, []DispatchDependency{{TaskID: "t1", DependsOn: []string{"missing"}}}, DispatchAutonomyManual))
		interpretDone <- err
	}()

	<-started // the interpretation staged its Decision/Hold/audit and is in the check-commit span.

	holdDone := make(chan error, 1)
	go func() {
		_, err := a.CreateHold(CreateHoldRequest{
			OperationID: "op-hold", Actor: Actor{ID: "test", Rank: "general"},
			ID: "hold-1", Scope: DispatchHoldScope{TaskIDs: []string{"t1"}},
			Actions: []DispatchAction{DispatchActionStart}, Reason: "review",
		})
		holdDone <- err
	}()

	select {
	case err := <-holdDone:
		t.Fatalf("hold committed inside the interpretation check-commit span: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(proceed)
	if err := <-interpretDone; err != nil {
		t.Fatalf("interpretation failed: %v", err)
	}
	if err := <-holdDone; err != nil {
		t.Fatalf("hold after interpretation failed: %v", err)
	}

	v, err := store.View()
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Interpretations) != 1 || len(v.Decisions) != 1 {
		t.Fatalf("decision-required set not committed atomically: %+v", v)
	}
	if len(v.Holds) != 2 {
		t.Fatalf("holds = %d, want 2 (decision hold + concurrent hold)", len(v.Holds))
	}
	dispatchAudits := 0
	for _, ev := range v.Audit {
		if ev.OperationID == "op-1" {
			dispatchAudits++
		}
	}
	if dispatchAudits != 1 {
		t.Fatalf("op-1 audit events = %d, want exactly 1", dispatchAudits)
	}
}
