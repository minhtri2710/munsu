package orchestrator

type EndpointStatus struct{ Alive bool }
type DisposeRequest struct {
	Backend, Handle, SessionOwner, WorkspaceID, TabID, Home, TaskID string
	DenyWorkspaceClose                                              bool
}
type BoundTeardown interface {
	Probe(homeDir string, meta map[string]string) (EndpointStatus, error)
	Dispose(homeDir string, meta map[string]string, request DisposeRequest) error
	ReturnWorktree(homeDir, worktreePath string) error
}
