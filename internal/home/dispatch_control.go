package home

// This file retains only the legacy v1 dispatch-control record shapes
// decoded from legacy homes (state/.dispatch/interpretations, holds,
// decisions). The v1 serialization adapter (PersistDispatchInterpretation),
// the v1 read path
// (LoadDispatchInterpretation/LoadDispatchDecision), and the holds-only
// CheckDispatchHold were deleted in the same slice as their last production
// callers moved: the handoff saga now evaluates dispatch through the composed
// Authorities (Task 6.2, ADR-0007 §11).

type DispatchAction string

const (
	DispatchActionHandoff DispatchAction = "handoff"
	DispatchActionStart   DispatchAction = "start"
	DispatchActionSpawn   DispatchAction = "spawn"
)

type DispatchReadiness struct {
	TaskID          string   `json:"task_id"`
	Generation      string   `json:"generation,omitempty"`
	Ready           bool     `json:"ready"`
	BlockingReasons []string `json:"blocking_reasons,omitempty"`
}

type DispatchEvidence struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	Field  string `json:"field,omitempty"`
	Value  string `json:"value,omitempty"`
}

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

type DispatchHoldScope struct {
	ProjectIDs  []string `json:"projects,omitempty"`
	TaskIDs     []string `json:"tasks,omitempty"`
	Generations []string `json:"generations,omitempty"`
	ParentIDs   []string `json:"parents,omitempty"`
}

type DispatchHold struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	Scope         DispatchHoldScope `json:"scope,omitempty"`
	Actions       []DispatchAction  `json:"actions"`
	Reason        string            `json:"reason"`
	CreatedAt     int64             `json:"created_at"`
	ReleasedAt    int64             `json:"released_at,omitempty"`
}

type DispatchDecision struct {
	SchemaVersion    string `json:"schema_version"`
	Key              string `json:"key"`
	InterpretationID string `json:"interpretation_id"`
	Reason           string `json:"reason"`
	CreatedAt        int64  `json:"created_at"`
	ResolvedAt       int64  `json:"resolved_at,omitempty"`
	Answer           string `json:"answer,omitempty"`
}
