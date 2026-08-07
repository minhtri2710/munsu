package home

// TaskEndpointBinding is a v1 endpoint binding shape retained as decode-only.
// The v1 aggregate store that produced it was deleted in Task 8.2, and v2
// bindings live on taskauthority.Aggregate.Endpoint.
type TaskEndpointBinding struct {
	TaskGeneration string `json:"task_generation"`
	Backend        string `json:"backend"`
	Handle         string `json:"handle"`
	LeaseID        string `json:"lease_id"`
	FenceToken     string `json:"fence_token"`
	SessionOwner   string `json:"session_owner,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	TabID          string `json:"tab_id,omitempty"`
	BoundAtUnix    int64  `json:"bound_at_unix"`
}
