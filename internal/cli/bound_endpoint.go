package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// boundResolver resolves the session backend of one bound endpoint;
// backend.BackendForTask in production.
type boundResolver func(labelHome string, ep backend.EndpointRef) (backend.Backend, string, error)

// boundBackend resolves the backend of a task's bound pane and refuses unless
// the binding records that backend, the resolver agrees with the record, and a
// herdr handle sits in the session the binding records as its owner.
func boundBackend(labelHome string, ep backend.EndpointRef, resolve boundResolver) (backend.Backend, string, error) {
	if ep.Backend == "" {
		return nil, "", fmt.Errorf("task does not record the pane's backend")
	}
	bk, name, err := resolve(labelHome, ep)
	if err != nil {
		return nil, "", err
	}
	if name != ep.Backend {
		return nil, "", fmt.Errorf("bound backend resolved as %q, task records %q", name, ep.Backend)
	}
	if name == "herdr" && backend.SessionOwnerMismatch(ep.Handle, ep.SessionOwner) {
		return nil, "", fmt.Errorf("herdr session ownership mismatch")
	}
	return bk, name, nil
}

// captainBinding is the endpoint binding a captain's .meta records.
func captainBinding(meta map[string]string) taskauthority.EndpointBinding {
	ref := backend.MetaEndpointRef(meta)
	return taskauthority.EndpointBinding{Backend: ref.Backend, Handle: ref.Handle, SessionOwner: ref.SessionOwner, WorkspaceID: ref.WorkspaceID, TabID: ref.TabID}
}

// bindingRef is the backend identity of a canonical endpoint binding.
func bindingRef(ep taskauthority.EndpointBinding) backend.EndpointRef {
	return backend.EndpointRef{Backend: ep.Backend, Handle: ep.Handle, SessionOwner: ep.SessionOwner, WorkspaceID: ep.WorkspaceID, TabID: ep.TabID}
}
