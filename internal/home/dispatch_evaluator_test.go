package home

import (
	"errors"
	"testing"
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
