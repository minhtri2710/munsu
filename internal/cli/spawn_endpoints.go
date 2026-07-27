package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/spawn"
)

type spawnSessionEndpoints struct {
	resolve func(string, string) (session.Backend, string, error)
	bound   map[string]session.Backend
}

func newSpawnSessionEndpoints() spawn.EndpointCapabilities {
	return &spawnSessionEndpoints{resolve: session.Resolve, bound: map[string]session.Backend{}}
}

func (s *spawnSessionEndpoints) Create(req spawn.CreateRequest) (spawn.CreatedEndpoint, error) {
	bk, name, err := s.resolve(req.Home, req.PreferredBackend)
	if err != nil {
		return spawn.CreatedEndpoint{}, err
	}
	if hb, ok := bk.(*session.HerdrBackend); ok {
		hb.Cwd = req.Cwd
	}
	handle, err := bk.NewWindow(req.WorkspaceName, req.TabName)
	if err != nil {
		return spawn.CreatedEndpoint{}, fmt.Errorf("backend %q not available: %w. Configure via --backend flag, config/backend file, or HERDR_ENV env", name, err)
	}
	ep := spawn.CreatedEndpoint{Backend: name, Handle: handle, Metadata: map[string]string{}}
	if ex, ok := bk.(session.BackendMetaExtras); ok {
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

func spawnEndpointKey(ep spawn.CreatedEndpoint) string { return ep.Backend + "\x00" + ep.Handle }

func (s *spawnSessionEndpoints) backend(ep spawn.CreatedEndpoint) (session.Backend, error) {
	bk, ok := s.bound[spawnEndpointKey(ep)]
	if !ok {
		return nil, fmt.Errorf("endpoint %q on backend %q is not bound", ep.Handle, ep.Backend)
	}
	return bk, nil
}

func (s *spawnSessionEndpoints) Submit(ep spawn.CreatedEndpoint, text string) error {
	bk, err := s.backend(ep)
	if err != nil {
		return err
	}
	return bk.SendKeys(ep.Handle, text)
}

func (s *spawnSessionEndpoints) Probe(ep spawn.CreatedEndpoint) (spawn.EndpointStatus, error) {
	bk, err := s.backend(ep)
	if err != nil {
		return spawn.EndpointStatus{}, err
	}
	return spawn.EndpointStatus{Alive: bk.Alive(ep.Handle)}, nil
}

func (s *spawnSessionEndpoints) Capture(ep spawn.CreatedEndpoint, lines int) (string, error) {
	bk, err := s.backend(ep)
	if err != nil {
		return "", err
	}
	return bk.Capture(ep.Handle, lines)
}

func (s *spawnSessionEndpoints) Dispose(ep spawn.CreatedEndpoint) error {
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
