package orchestrator

import "github.com/minhtri2710/munsu/internal/domain"

type EndpointStatus struct{ Alive bool }
type DisposeRequest struct {
	Backend, Handle, SessionOwner, WorkspaceID, TabID, Home, TaskID string
	DenyWorkspaceClose                                              bool
}
type BoundTeardown interface {
	RefuseGate() error
	Probe(homeDir string, meta map[string]string) (EndpointStatus, error)
	Dispose(homeDir string, meta map[string]string, request DisposeRequest) error
	ReturnWorktree(homeDir, worktreePath string) error
	QueryMergeStatus(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error)
}
