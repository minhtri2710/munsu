package fleet

import (
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
)

// RetirementEndpointStatus is the typed endpoint observation reported by a
// teardown probe. It is the single decision input for whether a bound endpoint
// is already gone (authoritative dead/current) versus live or not-confirmable
// (must be disposed). It is never a boolean Alive policy.
type RetirementEndpointStatus struct {
	Lifecycle      LifecycleState
	Responsiveness Responsiveness
	Freshness      Freshness
	Activity       Activity
	Source         ObservationSource
	ObservedAt     time.Time
	Detail         string
}

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

// AuthoritativeAbsent reports structured authoritative absence of the bound
// endpoint (dead + current + probe/derived source). Only this means the
// endpoint is already gone (skip disposal).
func (s RetirementEndpointStatus) AuthoritativeAbsent() bool {
	return s.Lifecycle == LifecycleDead && s.Freshness == FreshnessCurrent &&
		(s.Source == SourceProbe || s.Source == SourceDerived)
}

// Live reports a confirmed-alive/current reading (dispose the endpoint).
func (s RetirementEndpointStatus) Live() bool {
	return s.Lifecycle == LifecycleAlive && s.Freshness == FreshnessCurrent
}
