package spawn

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/session"
)

type sessionEndpoints struct {
	injected session.Backend
	resolve  func(string, string) (session.Backend, string, error)
	bound    map[string]session.Backend
}

func newSessionEndpoints(injected session.Backend) EndpointCapabilities {
	return &sessionEndpoints{injected: injected, resolve: session.Resolve, bound: map[string]session.Backend{}}
}

func (s *sessionEndpoints) Create(req CreateRequest) (CreatedEndpoint, error) {
	bk, name := s.injected, "test"
	var err error
	if bk == nil {
		bk, name, err = s.resolve(req.Home, req.PreferredBackend)
		if err != nil {
			return CreatedEndpoint{}, err
		}
	}
	if hb, ok := bk.(*session.HerdrBackend); ok {
		hb.Cwd = req.Cwd
	}
	handle, err := bk.NewWindow(req.WorkspaceName, req.TabName)
	if err != nil {
		return CreatedEndpoint{}, fmt.Errorf("backend %q not available: %w. Configure via --backend flag, config/backend file, or HERDR_ENV env", name, err)
	}
	ep := CreatedEndpoint{Backend: name, Handle: handle, Metadata: map[string]string{}}
	if ex, ok := bk.(session.BackendMetaExtras); ok {
		for k, v := range ex.MetaExtras() {
			ep.Metadata[k] = v
		}
		ep.SessionOwner = ep.Metadata["herdr_session"]
		ep.WorkspaceID = ep.Metadata["herdr_workspace_id"]
		ep.TabID = ep.Metadata["herdr_tab_id"]
	}
	s.bound[endpointKey(ep)] = bk
	return ep, nil
}
func endpointKey(ep CreatedEndpoint) string { return ep.Backend + "\x00" + ep.Handle }
func (s *sessionEndpoints) backend(ep CreatedEndpoint) (session.Backend, error) {
	bk, ok := s.bound[endpointKey(ep)]
	if !ok {
		return nil, fmt.Errorf("endpoint %q on backend %q is not bound", ep.Handle, ep.Backend)
	}
	return bk, nil
}
func (s *sessionEndpoints) Submit(ep CreatedEndpoint, text string) error {
	bk, err := s.backend(ep)
	if err != nil {
		return err
	}
	return bk.SendKeys(ep.Handle, text)
}
func (s *sessionEndpoints) Probe(ep CreatedEndpoint) (EndpointStatus, error) {
	bk, err := s.backend(ep)
	if err != nil {
		return EndpointStatus{}, err
	}
	return EndpointStatus{Alive: bk.Alive(ep.Handle)}, nil
}
func (s *sessionEndpoints) Capture(ep CreatedEndpoint, n int) (string, error) {
	bk, err := s.backend(ep)
	if err != nil {
		return "", err
	}
	return bk.Capture(ep.Handle, n)
}
func (s *sessionEndpoints) Dispose(ep CreatedEndpoint) error {
	bk, err := s.backend(ep)
	if err != nil {
		return err
	}
	err = bk.Teardown(ep.Handle)
	if err == nil {
		delete(s.bound, endpointKey(ep))
	}
	return err
}
