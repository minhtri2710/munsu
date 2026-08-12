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
	Incarnation    string
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

// AuthorizedAbsence concludes NEGATIVE authorization for this raw retirement
// probe against the exact canonical EndpointBinding proof (BEO-16/P1a): only a
// narrow exact structured absence of the exact bound handle, revalidated under
// the current generation/revision, may conclude AuthoritativeAbsent(). A raw
// probe is fresh-unknown; an incomplete/stale proof or a non-absence reading
// fails closed to unknown/stale and is never Absent()/Live().
func (s RetirementEndpointStatus) AuthorizedAbsence(proof exactEndpointProof) RetirementEndpointStatus {
	return RetirementEndpointStatus(authorizeAbsence(EndpointStatus(s), proof))
}

// AuthorizedLive concludes POSITIVE authorization for this raw retirement
// probe (BEO-16/P1a): raw probe liveness is promoted to Live() only with
// explicit acquisition evidence tying the exact handle to the incarnation
// (proof.acquired — the canonical EndpointBinding is the acquisition receipt)
// plus a complete proof revalidated under current generation/revision.
// Without the evidence, positive freshness stays unknown and Live() is
// unavailable — the retirement step stays pending and never disposes.
func (s RetirementEndpointStatus) AuthorizedLive(proof exactEndpointProof) RetirementEndpointStatus {
	return RetirementEndpointStatus(authorizeLive(EndpointStatus(s), proof))
}
