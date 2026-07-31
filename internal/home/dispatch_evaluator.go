package home

import (
	"fmt"
	"sort"
)

func EvaluateDispatch(homeDir string, requested []string, autonomy DispatchAutonomy) (DispatchInterpretation, []string, error) {
	return EvaluateDispatchWithDependencies(homeDir, requested, nil, autonomy)
}

func EvaluateDispatchWithDependencies(homeDir string, requested []string, dependencies []DispatchDependency, autonomy DispatchAutonomy) (DispatchInterpretation, []string, error) {
	if len(requested) == 0 {
		return DispatchInterpretation{}, nil, fmt.Errorf("dispatch evaluation requires requested tasks")
	}
	readiness := make([]DispatchReadiness, 0, len(requested))
	deriveDependencies := dependencies == nil
	if deriveDependencies {
		dependencies = make([]DispatchDependency, 0, len(requested))
	}
	for _, taskID := range requested {
		agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
		if err != nil {
			return DispatchInterpretation{}, nil, err
		}
		if !ok {
			return DispatchInterpretation{}, nil, fmt.Errorf("dispatch evaluation: task %s has no authoritative aggregate", taskID)
		}
		ready, err := QueryTaskReadiness(homeDir, taskID)
		if err != nil {
			return DispatchInterpretation{}, nil, err
		}
		readiness = append(readiness, DispatchReadiness{TaskID: taskID, Generation: ready.Generation, Ready: ready.Ready, BlockingReasons: readinessStrings(ready.BlockingReasons)})
		if deriveDependencies {
			dependencies = append(dependencies, DispatchDependency{TaskID: taskID, State: agg.State, DependsOn: nonEmptyParent(agg.ParentTaskID)})
		}
	}
	materialAmbiguity := dependencySnapshotAmbiguous(homeDir, dependencies)
	selected, cycle := stableTopologicalOrder(requested, dependencies)
	materialAmbiguity = materialAmbiguity || cycle
	record, err := PersistDispatchInterpretation(homeDir, DispatchInterpretationInput{RequestedOrder: requested, ComputedReadiness: readiness, SelectedTasks: selected, Dependencies: dependencies, SafeReinterpretation: true, MaterialAmbiguity: materialAmbiguity, Autonomy: autonomy})
	return record, selected, err
}

func dependencySnapshotAmbiguous(homeDir string, dependencies []DispatchDependency) bool {
	for _, dependency := range dependencies {
		aggregate, ok, err := ReadCurrentTaskAggregate(homeDir, dependency.TaskID)
		if err != nil || !ok || (dependency.State != "" && dependency.State != aggregate.State) {
			return true
		}
		for _, prerequisite := range dependency.DependsOn {
			if _, ok, err := ReadCurrentTaskAggregate(homeDir, prerequisite); err != nil || !ok {
				return true
			}
		}
	}
	return false
}

func readinessStrings(reasons []ReadinessReason) []string {
	result := make([]string, len(reasons))
	for i, reason := range reasons {
		result[i] = string(reason)
	}
	return result
}
func nonEmptyParent(parent string) []string {
	if parent == "" {
		return nil
	}
	return []string{parent}
}
func stableTopologicalOrder(requested []string, dependencies []DispatchDependency) ([]string, bool) {
	positions := make(map[string]int, len(requested))
	for i, taskID := range requested {
		positions[taskID] = i
	}
	indegree := make(map[string]int, len(requested))
	out := make(map[string][]string, len(requested))
	for _, taskID := range requested {
		indegree[taskID] = 0
	}
	for _, dependency := range dependencies {
		for _, prerequisite := range dependency.DependsOn {
			if _, ok := positions[prerequisite]; !ok {
				continue
			}
			out[prerequisite] = append(out[prerequisite], dependency.TaskID)
			indegree[dependency.TaskID]++
		}
	}
	ready := make([]string, 0, len(requested))
	for _, taskID := range requested {
		if indegree[taskID] == 0 {
			ready = append(ready, taskID)
		}
	}
	selected := make([]string, 0, len(requested))
	for len(ready) > 0 {
		sort.SliceStable(ready, func(i, j int) bool { return positions[ready[i]] < positions[ready[j]] })
		taskID := ready[0]
		ready = ready[1:]
		selected = append(selected, taskID)
		for _, dependent := range out[taskID] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	return selected, len(selected) != len(requested)
}
