package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
)

type spawnSessionEndpoints struct {
	resolve func(string, string) (backend.Backend, string, error)
	bound   map[string]backend.Backend
}

func newSpawnSessionEndpoints() fleet.EndpointCapabilities {
	return &spawnSessionEndpoints{resolve: backend.Resolve, bound: map[string]backend.Backend{}}
}

func (s *spawnSessionEndpoints) Create(req fleet.CreateRequest) (fleet.CreatedEndpoint, error) {
	bk, name, err := s.resolve(req.Home, req.PreferredBackend)
	if err != nil {
		return fleet.CreatedEndpoint{}, err
	}
	if hb, ok := bk.(*backend.HerdrBackend); ok {
		hb.Cwd = req.Cwd
	}
	handle, err := bk.NewWindow(backend.WorkspaceTag(req.Home), req.TabName)
	if err != nil {
		return fleet.CreatedEndpoint{}, fmt.Errorf("backend %q not available: %w. Configure via --backend flag, config/backend file, or HERDR_ENV env", name, err)
	}
	ep := fleet.CreatedEndpoint{Backend: name, Handle: handle, Metadata: map[string]string{}}
	if ex, ok := bk.(backend.BackendMetaExtras); ok {
		for k, v := range ex.MetaExtras() {
			ep.Metadata[k] = v
		}
		ep.SessionOwner = ep.Metadata["herdr_session"]
		ep.WorkspaceID = ep.Metadata["herdr_workspace_id"]
		ep.TabID = ep.Metadata["herdr_tab_id"]
	}
	s.bound[spawnEndpointKey(ep)] = bk
	return ep, nil
}

func spawnEndpointKey(ep fleet.CreatedEndpoint) string { return ep.Backend + "\x00" + ep.Handle }

func (s *spawnSessionEndpoints) backend(ep fleet.CreatedEndpoint) (backend.Backend, error) {
	bk, ok := s.bound[spawnEndpointKey(ep)]
	if !ok {
		return nil, fmt.Errorf("endpoint %q on backend %q is not bound", ep.Handle, ep.Backend)
	}
	return bk, nil
}

func (s *spawnSessionEndpoints) Submit(ep fleet.CreatedEndpoint, text string) error {
	bk, err := s.backend(ep)
	if err != nil {
		return err
	}
	return bk.SendKeys(ep.Handle, text)
}

func (s *spawnSessionEndpoints) Probe(ep fleet.CreatedEndpoint) (fleet.SpawnEndpointObservation, error) {
	bk, err := s.backend(ep)
	if err != nil {
		return fleet.SpawnEndpointObservation{}, err
	}
	observation := backend.ObserveBackendEndpoint(bk, ep.Handle)
	return fleet.SpawnEndpointObservation{State: observation.State, Detail: observation.Detail}, nil
}

func (s *spawnSessionEndpoints) Capture(ep fleet.CreatedEndpoint, lines int) (string, error) {
	bk, err := s.backend(ep)
	if err != nil {
		return "", err
	}
	return bk.Capture(ep.Handle, lines)
}

func (s *spawnSessionEndpoints) Dispose(ep fleet.CreatedEndpoint) error {
	bk, err := s.backend(ep)
	if err != nil {
		return err
	}
	if err := bk.Teardown(ep.Handle); err != nil {
		return err
	}
	delete(s.bound, spawnEndpointKey(ep))
	return nil
}
