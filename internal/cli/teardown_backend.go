package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type sessionBoundTeardown struct {
	resolve func(string, map[string]string) (backend.Backend, string, error)
}

func newSessionBoundTeardown() fleet.BoundTeardown {
	return sessionBoundTeardown{resolve: backend.BackendForTask}
}

func (s sessionBoundTeardown) resolveBound(home string, meta map[string]string) (backend.Backend, string, error) {
	if home == "" || meta["window"] == "" {
		return nil, "", fmt.Errorf("bound teardown identity is incomplete")
	}
	return boundBackend(home, meta, s.resolve)
}

func (s sessionBoundTeardown) Probe(home string, meta map[string]string) (fleet.RetirementEndpointStatus, error) {
	bk, _, err := s.resolveBound(home, meta)
	if err != nil {
		return fleet.RetirementEndpointStatus{}, err
	}
	obs := backend.ObserveEndpoint(bk, meta["window"], harnessProcessMatcher(meta["harness"]))
	return fleet.RetirementEndpointStatus{
		Lifecycle:      fleet.LifecycleState(obs.Lifecycle),
		Responsiveness: fleet.Responsiveness(obs.Responsiveness),
		Freshness:      fleet.Freshness(obs.Freshness),
		Activity:       fleet.Activity(obs.Activity),
		Source:         fleet.ObservationSource(obs.Source),
		ObservedAt:     obs.ObservedAt,
		Detail:         obs.Detail,
	}, nil
}

func (s sessionBoundTeardown) Dispose(home string, meta map[string]string, req fleet.DisposeRequest) error {
	if req.Home != home || req.Backend != meta["backend"] || req.Handle != meta["window"] || req.SessionOwner != meta[backend.MetaSessionOwner] || req.WorkspaceID != meta[backend.MetaWorkspaceID] || req.TabID != meta[backend.MetaTabID] {
		return fmt.Errorf("dispose request does not match bound endpoint metadata")
	}
	bk, name, err := s.resolveBound(home, meta)
	if err != nil {
		return err
	}
	if req.DenyWorkspaceClose {
		if name != "herdr" || req.WorkspaceID == "" {
			return fmt.Errorf("workspace-close denial is invalid for backend %q", name)
		}
		hb, ok := bk.(*backend.HerdrBackend)
		if !ok {
			return fmt.Errorf("herdr workspace-close policy is unsupported by resolved adapter")
		}
		hb.DenyCloseWorkspaceIDs = []string{req.WorkspaceID}
	}
	return bk.Teardown(req.Handle)
}

func (s sessionBoundTeardown) QueryMergeStatus(forge taskauthority.DeliveryStep, ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	return fleet.QueryDeliveryMergeStatus(forge, ident)
}

func (s sessionBoundTeardown) ReturnWorktree(homeDir, worktreePath string) error {
	return backend.ReturnWorktree(homeDir, worktreePath)
}
