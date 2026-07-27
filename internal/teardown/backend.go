package teardown

import (
	"fmt"
	"github.com/minhtri2710/munsu/internal/session"
)

type EndpointStatus struct{ Alive bool }
type DisposeRequest struct {
	Backend, Handle, SessionOwner, WorkspaceID, TabID, Home, TaskID string
	DenyWorkspaceClose                                              bool
}
type BoundTeardown interface {
	Probe(homeDir string, meta map[string]string) (EndpointStatus, error)
	Dispose(homeDir string, meta map[string]string, request DisposeRequest) error
}
type sessionTeardown struct {
	resolve func(string, map[string]string) (session.Backend, string, error)
}

func (s sessionTeardown) resolveBound(home string, meta map[string]string) (session.Backend, string, error) {
	if home == "" || meta["backend"] == "" || meta["window"] == "" {
		return nil, "", fmt.Errorf("bound teardown identity is incomplete")
	}
	bk, name, err := s.resolve(home, meta)
	if err != nil {
		return nil, "", err
	}
	if name != meta["backend"] {
		return nil, "", fmt.Errorf("bound backend resolved as %q", name)
	}
	if name == "herdr" && meta["herdr_session"] != "" {
		hs, _ := session.ParseWindow(meta["window"])
		if hs != "" && hs != meta["herdr_session"] {
			return nil, "", fmt.Errorf("herdr session ownership mismatch")
		}
	}
	return bk, name, nil
}
func (s sessionTeardown) Probe(home string, meta map[string]string) (EndpointStatus, error) {
	bk, _, err := s.resolveBound(home, meta)
	if err != nil {
		return EndpointStatus{}, err
	}
	return EndpointStatus{Alive: bk.Alive(meta["window"])}, nil
}
func (s sessionTeardown) Dispose(home string, meta map[string]string, req DisposeRequest) error {
	bk, name, err := s.resolveBound(home, meta)
	if err != nil {
		return err
	}
	if req.DenyWorkspaceClose && name == "herdr" {
		if hb, ok := bk.(*session.HerdrBackend); ok {
			hb.DenyCloseWorkspaceIDs = []string{req.WorkspaceID}
		}
	}
	return bk.Teardown(req.Handle)
}

var defaultTeardown BoundTeardown = sessionTeardown{resolve: session.BackendForTask}
