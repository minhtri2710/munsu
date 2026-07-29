package fleet

import "github.com/minhtri2710/munsu/internal/domain"

type RetirementEndpointStatus struct{ Alive bool }
type DisposeRequest struct {
	Backend, Handle, SessionOwner, WorkspaceID, TabID, Home, TaskID string
	DenyWorkspaceClose                                              bool
}
type BoundTeardown interface {
	RefuseGate() error
	Probe(homeDir string, meta map[string]string) (RetirementEndpointStatus, error)
	Dispose(homeDir string, meta map[string]string, request DisposeRequest) error
	ReturnWorktree(homeDir, worktreePath string) error
	QueryMergeStatus(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error)
}

type RetirementJournalPort interface {
	VerifyRetirementContinuity(homeDir, taskID string) error
	PrepareForcedRetirementEvidence(homeDir, taskID string) ([]string, error)
	FinalizeRetirementJournals(homeDir, taskID string) ([]string, error)
}
