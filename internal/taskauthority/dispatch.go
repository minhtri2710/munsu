package taskauthority

import (
	"strings"
)

// DispatchAction is a durable-control gate applied to one class of mutation.
type DispatchAction string

const (
	DispatchActionHandoff DispatchAction = "handoff"
	DispatchActionStart   DispatchAction = "start"
	DispatchActionSpawn   DispatchAction = "spawn"
)

// Valid reports whether the action is a known dispatch action.
func (a DispatchAction) Valid() bool {
	switch a {
	case DispatchActionHandoff, DispatchActionStart, DispatchActionSpawn:
		return true
	}
	return false
}

// DispatchHoldScope scopes a hold conservatively: empty fields match all.
type DispatchHoldScope struct {
	ProjectIDs  []string `json:"projects,omitempty"`
	TaskIDs     []string `json:"tasks,omitempty"`
	Generations []string `json:"generations,omitempty"`
	ParentIDs   []string `json:"parents,omitempty"`
}

// clone deep-copies the scope slices.
func (s DispatchHoldScope) clone() DispatchHoldScope {
	out := DispatchHoldScope{
		ProjectIDs:  append([]string(nil), s.ProjectIDs...),
		TaskIDs:     append([]string(nil), s.TaskIDs...),
		Generations: append([]string(nil), s.Generations...),
		ParentIDs:   append([]string(nil), s.ParentIDs...),
	}
	return out
}

// DispatchHold is a durable control that blocks matching actions until
// released. It is a business decision record, not a runtime supervision flag.
type DispatchHold struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	Scope         DispatchHoldScope `json:"scope,omitempty"`
	Actions       []DispatchAction  `json:"actions"`
	Reason        string            `json:"reason"`
	CreatedAt     int64             `json:"created_at"`
	ReleasedAt    int64             `json:"released_at,omitempty"`
}

// validate stale-copies hold slices and returns a validated copy.
func (h DispatchHold) clone() DispatchHold {
	out := h
	out.Scope = h.Scope.clone()
	out.Actions = append([]DispatchAction(nil), h.Actions...)
	return out
}

// validate checks the record shape of a dispatch hold.
func validateHold(h DispatchHold) error {
	if h.SchemaVersion != taskAuthoritySchema {
		return validationError("invalid dispatch hold schema %q", h.SchemaVersion)
	}
	if h.ID == "" || strings.ContainsAny(h.ID, `/\\`) {
		return validationError("dispatch hold ID must be a safe non-empty value")
	}
	if len(h.Actions) == 0 {
		return validationError("dispatch hold requires at least one action")
	}
	for _, action := range h.Actions {
		if !action.Valid() {
			return validationError("dispatch hold has unknown action %q", action)
		}
	}
	if strings.TrimSpace(h.Reason) == "" {
		return validationError("dispatch hold requires a reason")
	}
	for _, g := range h.Scope.Generations {
		if _, err := ParseGeneration(g); err != nil {
			return err
		}
	}
	return nil
}

// Matches reports whether the hold gates the given action for the task.
func (h DispatchHold) Matches(action DispatchAction, taskID, projectID, generation, parentID string) bool {
	if h.ReleasedAt != 0 || !containsAction(h.Actions, action) {
		return false
	}
	if len(h.Scope.TaskIDs) > 0 && !containsString(h.Scope.TaskIDs, taskID) {
		return false
	}
	if len(h.Scope.ProjectIDs) > 0 && !containsString(h.Scope.ProjectIDs, projectID) {
		return false
	}
	if len(h.Scope.Generations) > 0 && !containsString(h.Scope.Generations, generation) {
		return false
	}
	if len(h.Scope.ParentIDs) > 0 && !containsString(h.Scope.ParentIDs, parentID) {
		return false
	}
	return true
}

// DispatchInterpretation is the durable outcome of one dispatch
// interpretation. Interpretation rule evaluation is migrated in a later slice;
// this file owns the record shape and validation.
type DispatchInterpretation struct {
	SchemaVersion            string              `json:"schema_version"`
	ID                       string              `json:"id"`
	RequestedOrder           []string            `json:"requested_order"`
	ComputedReadiness        []DispatchReadiness `json:"computed_readiness,omitempty"`
	SelectedTasks            []string            `json:"selected_tasks"`
	Evidence                 []DispatchEvidence  `json:"evidence,omitempty"`
	DependencySnapshotDigest string              `json:"dependency_snapshot_digest"`
	ParentInterpretationID   string              `json:"parent_interpretation_id,omitempty"`
	Outcome                  string              `json:"outcome"`
	DecisionKey              string              `json:"decision_key,omitempty"`
	CreatedAt                int64               `json:"created_at"`
}

// DispatchReadiness is one task's readiness contribution to an interpretation.
type DispatchReadiness struct {
	TaskID          string   `json:"task_id"`
	Generation      string   `json:"generation,omitempty"`
	Ready           bool     `json:"ready"`
	BlockingReasons []string `json:"blocking_reasons,omitempty"`
}

// DispatchEvidence records where a readiness or interpretation fact came from.
type DispatchEvidence struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	Field  string `json:"field,omitempty"`
	Value  string `json:"value,omitempty"`
}

// DispatchInterpretation outcomes.
const (
	DispatchInterpretationAccepted         = "accepted"
	DispatchInterpretationReinterpreted    = "reinterpreted"
	DispatchInterpretationDecisionRequired = "decision-required"
)

// DispatchDecision is a durable general decision attached to an interpretation.
type DispatchDecision struct {
	SchemaVersion    string `json:"schema_version"`
	Key              string `json:"key"`
	InterpretationID string `json:"interpretation_id"`
	Reason           string `json:"reason"`
	CreatedAt        int64  `json:"created_at"`
	ResolvedAt       int64  `json:"resolved_at,omitempty"`
	Answer           string `json:"answer,omitempty"`
}

// validate checks the record shape of a dispatch decision.
func validateDecision(d DispatchDecision) error {
	if d.SchemaVersion != taskAuthoritySchema {
		return validationError("invalid dispatch decision schema %q", d.SchemaVersion)
	}
	if d.Key == "" {
		return validationError("dispatch decision missing key")
	}
	if d.InterpretationID == "" {
		return validationError("dispatch decision missing interpretation id")
	}
	if d.CreatedAt <= 0 {
		return validationError("dispatch decision missing created timestamp")
	}
	return nil
}

func containsAction(actions []DispatchAction, action DispatchAction) bool {
	for _, candidate := range actions {
		if candidate == action {
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
