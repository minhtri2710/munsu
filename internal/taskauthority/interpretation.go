package taskauthority

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// DispatchAutonomy is the configured autonomy under which a safe dependency
// reinterpretation may report-and-proceed (ADR-0004 §7). Under manual
// autonomy, any divergence from the requested order requires a Decision.
type DispatchAutonomy string

const (
	// DispatchAutonomyManual means the requested order is authoritative:
	// divergence requires a Decision.
	DispatchAutonomyManual DispatchAutonomy = "manual"
	// DispatchAutonomySafeReinterpretation allows a safe parent-spec-to-
	// executable-child order to report-and-proceed without a Decision.
	DispatchAutonomySafeReinterpretation DispatchAutonomy = "safe-reinterpretation"
)

// DispatchDependency is one task's dependency edge in a dispatch
// interpretation: the task, its prerequisites, and the state recorded in the
// directive snapshot the edge was derived from. A divergence between the
// recorded state and the current canonical state is material ambiguity.
type DispatchDependency struct {
	TaskID    string   `json:"task_id"`
	DependsOn []string `json:"depends_on,omitempty"`
	State     string   `json:"state,omitempty"`
}

// InterpretationInput carries the facts one interpretation evaluation reads.
// The Authority gathers them from its Store inside the transaction; adapters
// gather them from their own canonical reads. The interpretation rules
// themselves (dependency derivation, divergence classification, deterministic
// identity, and outcome) live in this module, never in the adapter.
type InterpretationInput struct {
	RequestedOrder         []string
	Dependencies           []DispatchDependency // nil derives from Aggregates
	Autonomy               DispatchAutonomy
	ParentInterpretationID string
	Evidence               []DispatchEvidence
	ComputedReadiness      []DispatchReadiness
	SelectedTasks          []string // empty computes the stable topological order
	SafeReinterpretation   bool
	MaterialAmbiguity      bool
	Aggregates             map[string]Aggregate // current canonical state per task
}

// InterpretationResult is the rule outcome of one interpretation evaluation.
// Decision and Hold are non-nil exactly when the outcome is decision-required:
// the caller stages them atomically with the record.
type InterpretationResult struct {
	Record        DispatchInterpretation
	SelectedTasks []string
	Decision      *DispatchDecision
	Hold          *DispatchHold
}

// EvaluateInterpretation computes the full interpretation: dependency
// derivation (when not supplied), material ambiguity (state divergence,
// missing prerequisites, cycles), the stable topological selection, the
// deterministic identity and dependency snapshot digest, and the outcome
// classification. It is pure: all state is supplied by the caller.
func EvaluateInterpretation(input InterpretationInput) (InterpretationResult, error) {
	if len(input.RequestedOrder) == 0 {
		return InterpretationResult{}, validationError("dispatch interpretation requires requested tasks")
	}
	dependencies := input.Dependencies
	if dependencies == nil {
		var err error
		dependencies, err = deriveDependencies(input.Aggregates, input.RequestedOrder)
		if err != nil {
			return InterpretationResult{}, err
		}
	}
	selected, cycle := stableTopologicalOrder(input.RequestedOrder, dependencies)
	if len(selected) == 0 {
		selected = append([]string(nil), input.RequestedOrder...)
	}
	input.Dependencies = dependencies
	input.SelectedTasks = selected
	input.MaterialAmbiguity = input.MaterialAmbiguity || cycle || dependencySnapshotAmbiguous(input.Aggregates, dependencies)
	return classifyInterpretation(input)
}

// ClassifyInterpretation computes the deterministic record identity, digest,
// and outcome for a fully-formed interpretation input whose readiness,
// selected tasks, and material ambiguity were computed by the caller.
func ClassifyInterpretation(input InterpretationInput) (InterpretationResult, error) {
	if len(input.RequestedOrder) == 0 {
		return InterpretationResult{}, validationError("dispatch interpretation requires requested tasks")
	}
	if len(input.SelectedTasks) == 0 {
		input.SelectedTasks = append([]string(nil), input.RequestedOrder...)
	}
	return classifyInterpretation(input)
}

// classifyInterpretation derives the deterministic interpretation identity
// from the requested order, selected tasks, dependency snapshot digest, and
// parent interpretation, then classifies the outcome per ADR-0004 §7: safe
// parent-to-child reinterpretation may report-and-proceed only under safe
// autonomy; material ambiguity or an unsafe order requires a Decision.
func classifyInterpretation(input InterpretationInput) (InterpretationResult, error) {
	depsDigest, err := dependencySnapshotDigest(input.Dependencies)
	if err != nil {
		return InterpretationResult{}, err
	}
	identity, err := json.Marshal(struct {
		Requested []string
		Selected  []string
		Digest    string
		Parent    string
	}{input.RequestedOrder, input.SelectedTasks, depsDigest, input.ParentInterpretationID})
	if err != nil {
		return InterpretationResult{}, err
	}
	identityDigest := sha256.Sum256(identity)
	record := DispatchInterpretation{
		SchemaVersion:            TaskAuthoritySchema,
		ID:                       "interpretation-" + hex.EncodeToString(identityDigest[:]),
		RequestedOrder:           append([]string(nil), input.RequestedOrder...),
		ComputedReadiness:        append([]DispatchReadiness(nil), input.ComputedReadiness...),
		SelectedTasks:            append([]string(nil), input.SelectedTasks...),
		Evidence:                 append([]DispatchEvidence(nil), input.Evidence...),
		DependencySnapshotDigest: depsDigest,
		ParentInterpretationID:   input.ParentInterpretationID,
		Outcome:                  DispatchInterpretationAccepted,
		CreatedAt:                time.Now().UnixNano(),
	}
	var decision *DispatchDecision
	var hold *DispatchHold
	diverged := !equalStrings(input.RequestedOrder, input.SelectedTasks)
	if diverged || input.MaterialAmbiguity {
		dependencySafe := isDependencySafeOrder(input.SelectedTasks, input.Dependencies)
		if input.MaterialAmbiguity || !dependencySafe || !input.SafeReinterpretation || input.Autonomy != DispatchAutonomySafeReinterpretation {
			record.Outcome = DispatchInterpretationDecisionRequired
			record.DecisionKey = record.ID + "-decision"
			decision = &DispatchDecision{
				SchemaVersion:    TaskAuthoritySchema,
				Key:              record.DecisionKey,
				InterpretationID: record.ID,
				Reason:           "material dispatch ambiguity",
				CreatedAt:        time.Now().UnixNano(),
			}
			hold = &DispatchHold{
				SchemaVersion: TaskAuthoritySchema,
				ID:            record.DecisionKey + "-hold",
				Scope:         DispatchHoldScope{TaskIDs: append([]string(nil), input.SelectedTasks...)},
				Actions:       []DispatchAction{DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn},
				Reason:        "dispatch decision required",
				CreatedAt:     time.Now().UnixNano(),
			}
		} else {
			record.Outcome = DispatchInterpretationReinterpreted
		}
	}
	return InterpretationResult{
		Record:        record,
		SelectedTasks: append([]string(nil), input.SelectedTasks...),
		Decision:      decision,
		Hold:          hold,
	}, nil
}

// InterpretDispatchRequest is the input of the named InterpretDispatch
// operation: the requested task order, optional explicit dependency edges,
// and the configured autonomy.
type InterpretDispatchRequest struct {
	OperationID            string
	Actor                  Actor
	RequestedOrder         []string
	Dependencies           []DispatchDependency // nil derives from canonical state
	Autonomy               DispatchAutonomy
	ParentInterpretationID string
	Evidence               []DispatchEvidence
}

func (r InterpretDispatchRequest) digestPayload() any {
	return struct {
		RequestedOrder         []string
		Dependencies           []DispatchDependency
		Autonomy               DispatchAutonomy
		ParentInterpretationID string
		Evidence               []DispatchEvidence
	}{r.RequestedOrder, r.Dependencies, r.Autonomy, r.ParentInterpretationID, r.Evidence}
}

// InterpretDispatchResult is the caller-visible outcome of InterpretDispatch.
type InterpretDispatchResult struct {
	Record        DispatchInterpretation
	SelectedTasks []string
	Replayed      bool
}

// InterpretDispatch is the named semantic operation that evaluates one
// dispatch interpretation against fresh canonical state inside one Store
// transaction, persists the Dispatch Interpretation record, and — when the
// outcome is decision-required — stages the Dispatch Decision, the matching
// task-scoped Dispatch Hold, and the typed audit event atomically in the same
// transaction (no check-commit race). Repeating the same Operation ID with
// the same intent digest replays the original receipt and committed record.
func (a *Authority) InterpretDispatch(req InterpretDispatchRequest) (InterpretDispatchResult, error) {
	if len(req.RequestedOrder) == 0 {
		return InterpretDispatchResult{}, validationError("dispatch interpretation requires requested tasks")
	}
	op, err := a.operation(req.OperationID, req.Actor, req.digestPayload())
	if err != nil {
		return InterpretDispatchResult{}, err
	}
	var staged InterpretationResult
	receipt, err := a.store.Update(op, func(tx *Tx) error {
		result, err := a.interpretInTx(tx, req)
		if err != nil {
			return err
		}
		staged = result
		if err := tx.PutInterpretation(result.Record); err != nil {
			return err
		}
		if result.Decision == nil {
			return nil
		}
		if err := tx.PutDecision(*result.Decision); err != nil {
			return err
		}
		if err := tx.PutHold(*result.Hold); err != nil {
			return err
		}
		return tx.AppendAudit(a.dispatchAudit(op, "interpretation requires decision: "+result.Record.ID))
	})
	if err != nil {
		return InterpretDispatchResult{}, err
	}
	if receipt.Replayed {
		return a.interpretationFromReceipt(receipt)
	}
	return InterpretDispatchResult{Record: staged.Record, SelectedTasks: staged.SelectedTasks}, nil
}

// interpretInTx gathers the fresh canonical state for every requested task
// from the transaction view — current aggregates and readiness evaluated
// against the same committed holds — and delegates the rules to
// EvaluateInterpretation.
func (a *Authority) interpretInTx(tx *Tx, req InterpretDispatchRequest) (InterpretationResult, error) {
	aggregates := make(map[string]Aggregate, len(req.RequestedOrder))
	readiness := make([]DispatchReadiness, 0, len(req.RequestedOrder))
	for _, taskID := range req.RequestedOrder {
		agg, ok := tx.Current(taskID)
		if !ok {
			return InterpretationResult{}, fmt.Errorf("dispatch evaluation: task %s has no authoritative aggregate", taskID)
		}
		aggregates[taskID] = agg
		ready := evaluateReadiness(tx.Holds(), agg)
		readiness = append(readiness, DispatchReadiness{
			TaskID:          ready.TaskID,
			Generation:      ready.Generation.String(),
			Ready:           ready.Ready,
			BlockingReasons: readinessReasonStrings(ready.BlockingReasons),
		})
	}
	return EvaluateInterpretation(InterpretationInput{
		RequestedOrder:         req.RequestedOrder,
		Dependencies:           req.Dependencies,
		Autonomy:               req.Autonomy,
		ParentInterpretationID: req.ParentInterpretationID,
		Evidence:               req.Evidence,
		ComputedReadiness:      readiness,
		Aggregates:             aggregates,
		SafeReinterpretation:   true,
	})
}

// interpretationFromReceipt returns the committed interpretation record for a
// replayed operation, reconstructing the typed result the original commit
// returned.
func (a *Authority) interpretationFromReceipt(receipt Receipt) (InterpretDispatchResult, error) {
	v, err := a.store.View()
	if err != nil {
		return InterpretDispatchResult{}, err
	}
	for _, rec := range v.Interpretations {
		if rec.ID == receipt.InterpretationID {
			return InterpretDispatchResult{Record: rec, SelectedTasks: append([]string(nil), rec.SelectedTasks...), Replayed: true}, nil
		}
	}
	return InterpretDispatchResult{}, internalError("operation %s has no dispatch interpretation outcome", receipt.OperationID)
}

// deriveDependencies builds the dependency snapshot from the current
// canonical aggregates when the caller did not supply one.
func deriveDependencies(aggregates map[string]Aggregate, requested []string) ([]DispatchDependency, error) {
	dependencies := make([]DispatchDependency, 0, len(requested))
	for _, taskID := range requested {
		agg, ok := aggregates[taskID]
		if !ok {
			return nil, fmt.Errorf("dispatch evaluation: task %s has no authoritative aggregate", taskID)
		}
		dependencies = append(dependencies, DispatchDependency{
			TaskID:    taskID,
			State:     string(agg.Phase),
			DependsOn: nonEmptyParent(agg.Definition.ParentTaskID),
		})
	}
	return dependencies, nil
}

// dependencySnapshotAmbiguous reports material ambiguity per ADR-0004 §7: a
// dependency whose recorded state diverges from the current canonical state,
// or a missing prerequisite aggregate.
func dependencySnapshotAmbiguous(aggregates map[string]Aggregate, dependencies []DispatchDependency) bool {
	for _, dependency := range dependencies {
		agg, ok := aggregates[dependency.TaskID]
		if !ok || (dependency.State != "" && dependency.State != string(agg.Phase)) {
			return true
		}
		for _, prerequisite := range dependency.DependsOn {
			if _, ok := aggregates[prerequisite]; !ok {
				return true
			}
		}
	}
	return false
}

// stableTopologicalOrder orders the requested tasks by dependency edges with
// a requested-order tiebreak, reporting a cycle when the selection is
// incomplete. The result is deterministic for a given request.
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

// isDependencySafeOrder reports whether the selected order places every
// prerequisite before its dependent and contains no duplicate task.
func isDependencySafeOrder(selected []string, dependencies []DispatchDependency) bool {
	positions := make(map[string]int, len(selected))
	for i, taskID := range selected {
		if _, exists := positions[taskID]; exists {
			return false
		}
		positions[taskID] = i
	}
	for _, dependency := range dependencies {
		dependentPosition, dependentExists := positions[dependency.TaskID]
		if !dependentExists {
			continue
		}
		for _, prerequisite := range dependency.DependsOn {
			prerequisitePosition, prerequisiteExists := positions[prerequisite]
			if prerequisiteExists && prerequisitePosition > dependentPosition {
				return false
			}
		}
	}
	return true
}

// dependencySnapshotDigest is the deterministic digest of a dependency
// snapshot: dependencies sorted by task id with sorted prerequisite lists,
// then hashed.
func dependencySnapshotDigest(dependencies []DispatchDependency) (string, error) {
	copyDeps := append([]DispatchDependency(nil), dependencies...)
	sort.Slice(copyDeps, func(i, j int) bool { return copyDeps[i].TaskID < copyDeps[j].TaskID })
	for i := range copyDeps {
		copyDeps[i].DependsOn = append([]string(nil), copyDeps[i].DependsOn...)
		sort.Strings(copyDeps[i].DependsOn)
	}
	data, err := json.Marshal(copyDeps)
	if err != nil {
		return "", fmt.Errorf("encoding dependency snapshot: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// readinessReasonStrings renders typed readiness reasons as their stable
// record representation.
func readinessReasonStrings(reasons []ReadinessReason) []string {
	out := make([]string, len(reasons))
	for i, reason := range reasons {
		out[i] = string(reason)
	}
	return out
}

func nonEmptyParent(parent string) []string {
	if parent == "" {
		return nil
	}
	return []string{parent}
}
