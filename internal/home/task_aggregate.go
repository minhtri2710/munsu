package home

// TaskAggregateEvidence is the v1 projection/audit-source evidence entry
// carried on TaskAggregate. It is part of the v1 decode shape retained for
// the task-authority migration; projection and audit-source lists are
// rebuildable and are never carried into v2.
type TaskAggregateEvidence struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Field string `json:"field"`
	Value string `json:"value"`
}

// TaskAggregate is the v1 authoritative task aggregate shape. It is
// decode-only (v1 decode shape): the v1 aggregate store and migration
// implementation were deleted in Task 8.2, and supported migration is owned
// by internal/taskauthorityfs, which unmarshals v1 aggregate documents into
// this shape and converts them (convertV1Aggregate). No reader or writer of
// the v1 aggregate store remains.
type TaskAggregate struct {
	SchemaVersion                string                  `json:"schema_version"`
	TaskID                       string                  `json:"task_id"`
	Generation                   string                  `json:"generation"`
	Current                      bool                    `json:"current"`
	Owner                        string                  `json:"owner"`
	Definition                   string                  `json:"definition,omitempty"`
	State                        string                  `json:"state,omitempty"`
	StateDetail                  string                  `json:"state_detail,omitempty"`
	Endpoint                     *TaskEndpointBinding    `json:"endpoint,omitempty"`
	Worktree                     *TaskWorktreeBinding    `json:"worktree,omitempty"`
	Project                      string                  `json:"project,omitempty"`
	ParentTaskID                 string                  `json:"parent_task_id,omitempty"`
	DispatchInterpretationID     string                  `json:"dispatch_interpretation_id,omitempty"`
	DispatchInterpretationDigest string                  `json:"dispatch_interpretation_digest,omitempty"`
	Kind                         string                  `json:"kind,omitempty"`
	Projections                  []TaskAggregateEvidence `json:"projections,omitempty"`
	AuditSources                 []TaskAggregateEvidence `json:"audit_sources,omitempty"`
}
