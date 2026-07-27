package spawn

type CreateRequest struct {
	Home, PreferredBackend, WorkspaceName, TabName, Cwd string
}

type CreatedEndpoint struct {
	Backend, Handle, SessionOwner, WorkspaceID, TabID string
	Metadata                                          map[string]string
}

type EndpointStatus struct{ Alive bool }

type EndpointCapabilities interface {
	Create(CreateRequest) (CreatedEndpoint, error)
	Submit(CreatedEndpoint, string) error
	Probe(CreatedEndpoint) (EndpointStatus, error)
	Capture(CreatedEndpoint, int) (string, error)
	Dispose(CreatedEndpoint) error
}
