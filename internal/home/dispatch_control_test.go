package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
	if err := ResolveDispatchDecision(homeDir, record.DecisionKey, "run child first"); err != nil {
		t.Fatal(err)
	}
	if err := CheckDispatchHold(homeDir, DispatchActionSpawn, "b", "", "", ""); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("resolved decision released hold: %v", err)
	}
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

func TestDispatchHoldBlocksScopedActionsAndReleaseDoesNotStart(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateTaskAggregate(homeDir, "task", "owner", "work", "ship", "project"); err != nil {
		t.Fatal(err)
	}
	before, _, err := ReadCurrentTaskAggregate(homeDir, "task")
	if err != nil {
		t.Fatal(err)
	}
	hold, err := CreateDispatchHold(homeDir, DispatchHoldInput{ID: "pause-task", Scope: DispatchHoldScope{TaskIDs: []string{"task"}}, Actions: []DispatchAction{DispatchActionStart, DispatchActionSpawn}, Reason: "wait for operator"})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []DispatchAction{DispatchActionStart, DispatchActionSpawn} {
		if err := CheckDispatchHold(homeDir, action, "task", "project", "1", ""); !errors.Is(err, ErrDispatchHeld) {
			t.Fatalf("action %s err = %v, want held", action, err)
		}
	}
	if err := ReleaseDispatchHold(homeDir, hold.ID); err != nil {
		t.Fatal(err)
	}
	if err := CheckDispatchHold(homeDir, DispatchActionStart, "task", "project", "1", ""); err != nil {
		t.Fatalf("released hold still blocks: %v", err)
	}
	after, _, err := ReadCurrentTaskAggregate(homeDir, "task")
	if err != nil {
		t.Fatal(err)
	}
	if after.State != before.State || after.Generation != before.Generation {
		t.Fatalf("hold changed queued aggregate: before=%+v after=%+v", before, after)
	}
}

func TestQueryTaskReadinessWithHoldIsPure(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateTaskAggregate(homeDir, "task", "owner", "work", "ship", "project"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateDispatchHold(homeDir, DispatchHoldInput{ID: "pause-ready", Scope: DispatchHoldScope{TaskIDs: []string{"task"}}, Actions: []DispatchAction{DispatchActionStart}, Reason: "pause"}); err != nil {
		t.Fatal(err)
	}
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
	if _, err := CreateDispatchHold(homeDir, DispatchHoldInput{ID: "pause-home", Actions: []DispatchAction{DispatchActionHandoff}, Reason: "pause"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckDispatchHold(homeDir, DispatchActionHandoff, "other", "", "", ""); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("reloaded hold err = %v", err)
	}
}

func TestGenerationScopedHoldDoesNotBlockReopenedGeneration(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateDispatchHold(homeDir, DispatchHoldInput{ID: "pause-generation", Scope: DispatchHoldScope{TaskIDs: []string{"task"}, Generations: []string{"1"}}, Actions: []DispatchAction{DispatchActionStart}, Reason: "pause generation 1"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckDispatchHold(homeDir, DispatchActionStart, "task", "", "1", ""); !errors.Is(err, ErrDispatchHeld) {
		t.Fatalf("generation 1 err = %v, want held", err)
	}
	if err := CheckDispatchHold(homeDir, DispatchActionStart, "task", "", "2", ""); err != nil {
		t.Fatalf("generation 2 err = %v, want allowed", err)
	}
}
