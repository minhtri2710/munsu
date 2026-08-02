package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDispatchInterpretationPersistsCanonicalReadinessAndDependencyDigest(t *testing.T) {
	homeDir := t.TempDir()
	readiness := []DispatchReadiness{{TaskID: "child", Generation: "2", Ready: true}, {TaskID: "parent", Generation: "1", BlockingReasons: []string{"dependency"}}}
	deps := []DispatchDependency{{TaskID: "parent", DependsOn: []string{"child"}, State: "queued"}, {TaskID: "child", State: "working"}}
	input := DispatchInterpretationInput{
		RequestedOrder: []string{"parent", "child"}, ComputedReadiness: readiness,
		SelectedTasks: []string{"child"}, Evidence: []DispatchEvidence{{Source: "backlog", Path: "data/backlog.md", Field: "order", Value: "parent,child"}},
		Dependencies: deps, ParentInterpretationID: "parent-interpretation", SafeReinterpretation: true, Autonomy: DispatchAutonomySafeReinterpretation,
	}
	record, err := PersistDispatchInterpretation(homeDir, input)
	if err != nil {
		t.Fatal(err)
	}
	if record.DependencySnapshotDigest == "" || record.Outcome != DispatchInterpretationReinterpreted {
		t.Fatalf("record = %+v", record)
	}
	loaded, err := LoadDispatchInterpretation(homeDir, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DependencySnapshotDigest != record.DependencySnapshotDigest || loaded.ParentInterpretationID != "parent-interpretation" {
		t.Fatalf("loaded = %+v", loaded)
	}
	repeated, err := PersistDispatchInterpretation(homeDir, input)
	if err != nil || repeated.ID != record.ID {
		t.Fatalf("repeated = %+v err=%v, want same durable interpretation", repeated, err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", ".dispatch", "interpretations", record.ID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchInterpretationMaterialAmbiguityPersistsDecision(t *testing.T) {
	homeDir := t.TempDir()
	record, err := PersistDispatchInterpretation(homeDir, DispatchInterpretationInput{
		RequestedOrder: []string{"a", "b"}, SelectedTasks: []string{"b", "a"},
		MaterialAmbiguity: true, SafeReinterpretation: true, Autonomy: DispatchAutonomySafeReinterpretation,
	})
	if !errors.Is(err, ErrDispatchDecisionRequired) {
		t.Fatalf("err = %v, want decision required", err)
	}
	if record.Outcome != DispatchInterpretationDecisionRequired || record.DecisionKey == "" {
		t.Fatalf("record = %+v", record)
	}
	decision, err := LoadDispatchDecision(homeDir, record.DecisionKey)
	if err != nil || decision.InterpretationID != record.ID {
		t.Fatalf("decision = %+v err=%v", decision, err)
	}
	if err := CheckDispatchHold(homeDir, DispatchActionSpawn, "b", "", "", ""); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("ambiguity hold err = %v, want held", err)
	}
	// Resolution of the decision and release of the matching hold are one
	// atomic Authority operation now (Task 5.2 ResolveDecision); the legacy
	// home mutation ResolveDispatchDecision is deleted with zero production
	// callers.
}

func TestDispatchInterpretationRejectsDependencyUnsafeReinterpretation(t *testing.T) {
	record, err := PersistDispatchInterpretation(t.TempDir(), DispatchInterpretationInput{
		RequestedOrder: []string{"child", "parent"}, SelectedTasks: []string{"parent", "child"},
		Dependencies:         []DispatchDependency{{TaskID: "parent", DependsOn: []string{"child"}}},
		SafeReinterpretation: true, Autonomy: DispatchAutonomySafeReinterpretation,
	})
	if !errors.Is(err, ErrDispatchDecisionRequired) || record.DecisionKey == "" {
		t.Fatalf("record = %+v err=%v, want dependency decision", record, err)
	}
}

// seedHold writes one legacy v1 dispatch hold document directly. The home
// mutation CreateDispatchHold is deleted (Task 5.2: authoritative hold
// mutation flows through the Authority), so remaining home hold-evaluation
// tests seed the v1 read path the home adapter still serves.
func seedHold(t *testing.T, homeDir string, id string, scope DispatchHoldScope, actions []DispatchAction, reason string) {
	t.Helper()
	hold := DispatchHold{
		SchemaVersion: dispatchControlSchema,
		ID:            id,
		Scope:         scope,
		Actions:       append([]DispatchAction(nil), actions...),
		Reason:        reason,
		CreatedAt:     time.Now().UnixNano(),
	}
	if err := writeDispatchJSON(dispatchHoldPath(homeDir, id), hold); err != nil {
		t.Fatal(err)
	}
}

func TestQueryTaskReadinessWithHoldIsPure(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateTaskAggregate(homeDir, "task", "owner", "work", "ship", "project"); err != nil {
		t.Fatal(err)
	}
	seedHold(t, homeDir, "pause-ready", DispatchHoldScope{TaskIDs: []string{"task"}}, []DispatchAction{DispatchActionStart}, "pause")
	before, err := os.ReadDir(filepath.Join(homeDir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := QueryTaskReadiness(homeDir, "task")
	if err != nil || readiness.Ready || len(readiness.BlockingReasons) != 1 || readiness.BlockingReasons[0] != ReadinessDispatchHold {
		t.Fatalf("readiness = %+v err=%v", readiness, err)
	}
	after, err := os.ReadDir(filepath.Join(homeDir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("readiness created state entries: before=%v after=%v", before, after)
	}
}

func TestDispatchHoldSurvivesReload(t *testing.T) {
	homeDir := t.TempDir()
	seedHold(t, homeDir, "pause-home", DispatchHoldScope{}, []DispatchAction{DispatchActionHandoff}, "pause")
	if err := CheckDispatchHold(homeDir, DispatchActionHandoff, "other", "", "", ""); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("reloaded hold err = %v", err)
	}
}

func TestGenerationScopedHoldDoesNotBlockReopenedGeneration(t *testing.T) {
	homeDir := t.TempDir()
	seedHold(t, homeDir, "pause-generation", DispatchHoldScope{TaskIDs: []string{"task"}, Generations: []string{"1"}}, []DispatchAction{DispatchActionStart}, "pause generation 1")
	if err := CheckDispatchHold(homeDir, DispatchActionStart, "task", "", "1", ""); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("generation 1 err = %v, want held", err)
	}
	if err := CheckDispatchHold(homeDir, DispatchActionStart, "task", "", "2", ""); err != nil {
		t.Fatalf("generation 2 err = %v, want allowed", err)
	}
}

// TestCheckDispatchHoldIgnoresWatcherHealth proves CheckDispatchHold is
// holds-only: degraded supervision no longer blocks the durable dispatch
// check (Task 4.3). Supervision gating is the orchestration layer's job.
func TestCheckDispatchHoldIgnoresWatcherHealth(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "state"), 0755)

	// Claim lease with a dead PID — watcher is unhealthy.
	ClaimWatcherLease(homeDir, 9999999)

	// Without holds, all actions pass the holds-only check despite the
	// degraded watcher.
	for _, action := range []DispatchAction{DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn} {
		if err := CheckDispatchHold(homeDir, action, "", "", "", ""); err != nil {
			t.Errorf("action %s: CheckDispatchHold err = %v, want nil (holds-only)", action, err)
		}
	}

	// A matching hold is still enforced even with an unhealthy watcher.
	seedHold(t, homeDir, "pause-start", DispatchHoldScope{}, []DispatchAction{DispatchActionStart}, "pause all starts")
	if err := CheckDispatchHold(homeDir, DispatchActionStart, "", "", "", ""); !errors.Is(err, ErrDispatchHeld) {
		t.Errorf("held action with unhealthy watcher: err = %v, want ErrDispatchHeld", err)
	}
	if err := CheckDispatchHold(homeDir, DispatchActionSpawn, "", "", "", ""); err != nil {
		t.Errorf("non-held action with unhealthy watcher: unexpected error %v", err)
	}
}
