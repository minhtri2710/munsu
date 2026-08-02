package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestEvaluateDispatchUsesDependencyGraphForSafeReinterpretation(t *testing.T) {
	homeDir := t.TempDir()
	for _, taskID := range []string{"parent", "child"} {
		if _, err := CreateTaskAggregate(homeDir, taskID, "owner", taskID, "ship", "project"); err != nil {
			t.Fatal(err)
		}
	}
	record, selected, err := EvaluateDispatchWithDependencies(homeDir, []string{"child", "parent"}, []DispatchDependency{{TaskID: "parent", DependsOn: []string{"child"}}}, DispatchAutonomySafeReinterpretation)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0] != "child" || selected[1] != "parent" {
		t.Fatalf("selected = %v", selected)
	}
	if record.DependencySnapshotDigest == "" {
		t.Fatal("dependency digest is empty")
	}
}

func TestEvaluateDispatchMissingDependencyRequiresDecision(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateTaskAggregate(homeDir, "child", "owner", "child", "ship", "project"); err != nil {
		t.Fatal(err)
	}
	record, _, err := EvaluateDispatchWithDependencies(homeDir, []string{"child"}, []DispatchDependency{{TaskID: "child", DependsOn: []string{"missing"}, State: "queued"}}, DispatchAutonomySafeReinterpretation)
	if !errors.Is(err, ErrDispatchDecisionRequired) || record.DecisionKey == "" {
		t.Fatalf("record = %+v err=%v", record, err)
	}
}

func TestEvaluateDispatchCycleRequiresDecisionAndDoesNotMutateLifecycle(t *testing.T) {
	homeDir := t.TempDir()
	for _, taskID := range []string{"a", "b"} {
		if _, err := CreateTaskAggregate(homeDir, taskID, "owner", taskID, "ship", "project"); err != nil {
			t.Fatal(err)
		}
	}
	before, _, err := ReadCurrentTaskAggregate(homeDir, "a")
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := EvaluateDispatchWithDependencies(homeDir, []string{"a", "b"}, []DispatchDependency{{TaskID: "a", DependsOn: []string{"b"}}, {TaskID: "b", DependsOn: []string{"a"}}}, DispatchAutonomySafeReinterpretation)
	if !errors.Is(err, ErrDispatchDecisionRequired) || record.DecisionKey == "" {
		t.Fatalf("record = %+v err=%v", record, err)
	}
	after, _, err := ReadCurrentTaskAggregate(homeDir, "a")
	if err != nil || before.State != after.State || before.Generation != after.Generation {
		t.Fatalf("lifecycle mutated: before=%+v after=%+v err=%v", before, after, err)
	}
}

// TestEvaluateDispatchPersistsHomePathRecord pins the legacy home-path
// projection the handoff journal copies: after evaluation the interpretation
// record is readable from state/.dispatch/interpretations through the home
// loader with the identical deterministic identity.
func TestEvaluateDispatchPersistsHomePathRecord(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := CreateTaskAggregate(homeDir, "child", "owner", "child", "ship", "project"); err != nil {
		t.Fatal(err)
	}
	record, _, err := EvaluateDispatchWithDependencies(homeDir, []string{"child"}, nil, DispatchAutonomyManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", ".dispatch", "interpretations", record.ID+".json")); err != nil {
		t.Fatalf("home-path interpretation record missing: %v", err)
	}
	loaded, err := LoadDispatchInterpretation(homeDir, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != record.ID || loaded.DependencySnapshotDigest != record.DependencySnapshotDigest {
		t.Fatalf("loaded = %+v, want %+v", loaded, record)
	}
}

// TestEvaluateDispatchMatchesAuthorityInterpretation proves the home adapter
// and the Authority share one rules engine: the same request produces the
// same deterministic interpretation identity, digest, selection, and outcome
// whether evaluated through the home legacy path or through
// Authority.InterpretDispatch over a Store holding equivalent canonical
// state.
func TestEvaluateDispatchMatchesAuthorityInterpretation(t *testing.T) {
	homeDir := t.TempDir()
	for _, taskID := range []string{"parent", "child"} {
		if _, err := CreateTaskAggregate(homeDir, taskID, "owner", taskID, "ship", "project"); err != nil {
			t.Fatal(err)
		}
	}
	deps := []DispatchDependency{{TaskID: "parent", DependsOn: []string{"child"}, State: "queued"}}
	record, selected, err := EvaluateDispatchWithDependencies(homeDir, []string{"parent", "child"}, deps, DispatchAutonomySafeReinterpretation)
	if err != nil {
		t.Fatal(err)
	}

	auth := taskauthority.New(taskauthority.NewMemStore())
	actor := taskauthority.Actor{ID: "test", Rank: "general"}
	for _, taskID := range []string{"parent", "child"} {
		if _, err := auth.Create(taskauthority.CreateRequest{
			OperationID: "op-create-" + taskID, Actor: actor,
			TaskID: taskID, Owner: "owner", Description: taskID, Kind: "ship", Project: "project",
		}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := auth.InterpretDispatch(taskauthority.InterpretDispatchRequest{
		OperationID: "op-interpret", Actor: actor,
		RequestedOrder: []string{"parent", "child"},
		Dependencies:   []taskauthority.DispatchDependency{{TaskID: "parent", DependsOn: []string{"child"}, State: "queued"}},
		Autonomy:       taskauthority.DispatchAutonomySafeReinterpretation,
	})
	if err != nil {
		t.Fatal(err)
	}

	if record.ID != result.Record.ID || record.DependencySnapshotDigest != result.Record.DependencySnapshotDigest {
		t.Fatalf("adapter identity %q/%q differs from authority %q/%q", record.ID, record.DependencySnapshotDigest, result.Record.ID, result.Record.DependencySnapshotDigest)
	}
	if len(selected) != len(result.SelectedTasks) || selected[0] != result.SelectedTasks[0] || selected[1] != result.SelectedTasks[1] {
		t.Fatalf("adapter selection %v differs from authority %v", selected, result.SelectedTasks)
	}
	if record.Outcome != result.Record.Outcome {
		t.Fatalf("adapter outcome %q differs from authority %q", record.Outcome, result.Record.Outcome)
	}
	if len(record.ComputedReadiness) != len(result.Record.ComputedReadiness) {
		t.Fatalf("readiness differs: %+v vs %+v", record.ComputedReadiness, result.Record.ComputedReadiness)
	}
}
