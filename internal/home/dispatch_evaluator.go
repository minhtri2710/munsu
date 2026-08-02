package home

import (
	"fmt"
	"strconv"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func EvaluateDispatch(homeDir string, requested []string, autonomy DispatchAutonomy) (DispatchInterpretation, []string, error) {
	return EvaluateDispatchWithDependencies(homeDir, requested, nil, autonomy)
}

// EvaluateDispatchWithDependencies is the home serialization adapter for
// dispatch interpretation. It gathers the canonical state facts (current
// aggregates and readiness) through the home queries, delegates every
// interpretation rule — dependency derivation, material ambiguity, stable
// topological selection, deterministic identity, and outcome classification —
// to internal/taskauthority, and persists the legacy home-path projection
// records. It contains no interpretation rules itself.
func EvaluateDispatchWithDependencies(homeDir string, requested []string, dependencies []DispatchDependency, autonomy DispatchAutonomy) (DispatchInterpretation, []string, error) {
	if len(requested) == 0 {
		return DispatchInterpretation{}, nil, fmt.Errorf("dispatch evaluation requires requested tasks")
	}
	aggregates, err := gatherInterpretationSnapshot(homeDir, requested, dependencies)
	if err != nil {
		return DispatchInterpretation{}, nil, err
	}
	readiness := make([]DispatchReadiness, 0, len(requested))
	for _, taskID := range requested {
		ready, err := QueryTaskReadiness(homeDir, taskID)
		if err != nil {
			return DispatchInterpretation{}, nil, err
		}
		readiness = append(readiness, DispatchReadiness{TaskID: ready.TaskID, Generation: ready.Generation, Ready: ready.Ready, BlockingReasons: readinessStrings(ready.BlockingReasons)})
	}
	result, err := taskauthority.EvaluateInterpretation(taskauthority.InterpretationInput{
		RequestedOrder:       requested,
		Dependencies:         toTaskAuthorityDependencies(dependencies),
		Autonomy:             taskauthority.DispatchAutonomy(autonomy),
		ComputedReadiness:    toTaskAuthorityReadiness(readiness),
		Aggregates:           aggregates,
		SafeReinterpretation: true,
	})
	if err != nil {
		return DispatchInterpretation{}, nil, err
	}
	record := toHomeInterpretation(result.Record)
	if err := persistDispatchInterpretationRecords(homeDir, record, result.Decision, result.Hold); err != nil {
		return record, result.SelectedTasks, err
	}
	if result.Record.Outcome == taskauthority.DispatchInterpretationDecisionRequired {
		return record, result.SelectedTasks, ErrDispatchDecisionRequired
	}
	return record, result.SelectedTasks, nil
}

// gatherInterpretationSnapshot loads the canonical aggregate of every task
// the interpretation rules may reference: the requested tasks plus every task
// named by the dependency snapshot — each dependency's task, each supplied
// prerequisite, or the parents of requested tasks when dependencies are
// derived. Existence-based ambiguity therefore evaluates against canonical
// state, not requested-set membership: a referenced task that exists is not
// missing; only truly absent tasks are material ambiguity.
func gatherInterpretationSnapshot(homeDir string, requested []string, dependencies []DispatchDependency) (map[string]taskauthority.Aggregate, error) {
	aggregates := make(map[string]taskauthority.Aggregate, len(requested))
	add := func(taskID string) error {
		if _, ok := aggregates[taskID]; ok {
			return nil
		}
		agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
		if err != nil {
			return err
		}
		if ok {
			aggregates[taskID] = taskAuthorityAggregate(*agg)
		}
		return nil
	}
	for _, taskID := range requested {
		agg, ok, err := ReadCurrentTaskAggregate(homeDir, taskID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("dispatch evaluation: task %s has no authoritative aggregate", taskID)
		}
		aggregates[taskID] = taskAuthorityAggregate(*agg)
	}
	if dependencies == nil {
		// Deriving: the snapshot edges are the parents of the requested tasks.
		for _, taskID := range requested {
			if parent := aggregates[taskID].Definition.ParentTaskID; parent != "" {
				if err := add(parent); err != nil {
					return nil, err
				}
			}
		}
		return aggregates, nil
	}
	// Supplied edges: mirror the canonical reads the pre-move ambiguity check
	// performed — each dependency's task and every prerequisite.
	for _, dependency := range dependencies {
		if err := add(dependency.TaskID); err != nil {
			return nil, err
		}
		for _, prerequisite := range dependency.DependsOn {
			if err := add(prerequisite); err != nil {
				return nil, err
			}
		}
	}
	return aggregates, nil
}

// taskAuthorityAggregate translates one legacy home aggregate into the
// canonical taskauthority record shape the interpretation rules evaluate.
// The rules read phase, parent linkage, and existence only.
func taskAuthorityAggregate(agg TaskAggregate) taskauthority.Aggregate {
	generation, _ := strconv.ParseUint(agg.Generation, 10, 64)
	return taskauthority.Aggregate{
		SchemaVersion: taskauthority.TaskAuthoritySchema,
		TaskID:        agg.TaskID,
		Generation:    taskauthority.Generation(generation),
		Revision:      taskauthority.FirstRevision,
		Current:       agg.Current,
		Definition: taskauthority.TaskDefinition{
			Owner:        agg.Owner,
			Description:  agg.Definition,
			Kind:         agg.Kind,
			Project:      agg.Project,
			ParentTaskID: agg.ParentTaskID,
		},
		Phase: taskauthority.Phase(agg.State),
	}
}

func toTaskAuthorityDependencies(dependencies []DispatchDependency) []taskauthority.DispatchDependency {
	if dependencies == nil {
		return nil
	}
	out := make([]taskauthority.DispatchDependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		out = append(out, taskauthority.DispatchDependency{
			TaskID:    dependency.TaskID,
			DependsOn: append([]string(nil), dependency.DependsOn...),
			State:     dependency.State,
		})
	}
	return out
}

func toTaskAuthorityReadiness(readiness []DispatchReadiness) []taskauthority.DispatchReadiness {
	out := make([]taskauthority.DispatchReadiness, 0, len(readiness))
	for _, item := range readiness {
		out = append(out, taskauthority.DispatchReadiness{
			TaskID:          item.TaskID,
			Generation:      item.Generation,
			Ready:           item.Ready,
			BlockingReasons: append([]string(nil), item.BlockingReasons...),
		})
	}
	return out
}

func toTaskAuthorityEvidence(evidence []DispatchEvidence) []taskauthority.DispatchEvidence {
	out := make([]taskauthority.DispatchEvidence, 0, len(evidence))
	for _, item := range evidence {
		out = append(out, taskauthority.DispatchEvidence{
			Source: item.Source,
			Path:   item.Path,
			Field:  item.Field,
			Value:  item.Value,
		})
	}
	return out
}

func readinessStrings(reasons []ReadinessReason) []string {
	result := make([]string, len(reasons))
	for i, reason := range reasons {
		result[i] = string(reason)
	}
	return result
}
