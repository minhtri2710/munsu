package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type sessionBoundTeardown struct {
	resolve boundResolver
}

func newSessionBoundTeardown() fleet.BoundTeardown {
	return sessionBoundTeardown{resolve: backend.BackendForTask}
}

func (s sessionBoundTeardown) resolveBound(home string, ep taskauthority.EndpointBinding) (backend.Backend, string, error) {
	if home == "" || ep.Handle == "" {
		return nil, "", fmt.Errorf("bound teardown identity is incomplete")
	}
	return boundBackend(home, bindingRef(ep), s.resolve)
}

func (s sessionBoundTeardown) Probe(home string, ep taskauthority.EndpointBinding, harness string) (fleet.RetirementEndpointStatus, error) {
	bk, _, err := s.resolveBound(home, ep)
	if err != nil {
		return fleet.RetirementEndpointStatus{}, err
	}
	obs := backend.ObserveEndpoint(bk, ep.Handle, harnessProcessMatcher(harness))
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

func (s sessionBoundTeardown) Dispose(req fleet.DisposeRequest) error {
	bk, name, err := s.resolveBound(req.Home, req.Endpoint)
	if err != nil {
		return err
	}
	if req.DenyWorkspaceClose {
		if name != "herdr" || req.Endpoint.WorkspaceID == "" {
			return fmt.Errorf("workspace-close denial is invalid for backend %q", name)
		}
		hb, ok := bk.(*backend.HerdrBackend)
		if !ok {
			return fmt.Errorf("herdr workspace-close policy is unsupported by resolved adapter")
		}
		hb.DenyCloseWorkspaceIDs = []string{req.Endpoint.WorkspaceID}
	}
	return bk.Teardown(req.Endpoint.Handle)
}

func (s sessionBoundTeardown) QueryMergeStatus(forge taskauthority.DeliveryStep, ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	return fleet.QueryDeliveryMergeStatus(forge, ident)
}

func (s sessionBoundTeardown) ReturnWorktree(homeDir, treehouse, worktreePath string) error {
	return backend.ReturnWorktree(homeDir, treehouse, worktreePath)
}
