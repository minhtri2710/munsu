package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/teardown"
)

type sessionBoundTeardown struct {
	resolve func(string, map[string]string) (backend.Backend, string, error)
}

func newSessionBoundTeardown() teardown.BoundTeardown {
	return sessionBoundTeardown{resolve: backend.BackendForTask}
}

func (s sessionBoundTeardown) resolveBound(home string, meta map[string]string) (backend.Backend, string, error) {
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
		hs, _ := backend.ParseWindow(meta["window"])
		if hs != "" && hs != meta["herdr_session"] {
			return nil, "", fmt.Errorf("herdr session ownership mismatch")
		}
	}
	return bk, name, nil
}

func (s sessionBoundTeardown) Probe(home string, meta map[string]string) (teardown.EndpointStatus, error) {
	bk, _, err := s.resolveBound(home, meta)
	if err != nil {
		return teardown.EndpointStatus{}, err
	}
	return teardown.EndpointStatus{Alive: bk.Alive(meta["window"])}, nil
}

func (s sessionBoundTeardown) Dispose(home string, meta map[string]string, req teardown.DisposeRequest) error {
	if req.Home != home || req.Backend != meta["backend"] || req.Handle != meta["window"] || req.SessionOwner != meta["herdr_session"] || req.WorkspaceID != meta["herdr_workspace_id"] || req.TabID != meta["herdr_tab_id"] {
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
