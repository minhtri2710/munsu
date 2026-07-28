package fleet

type CreateRequest struct {
	Home, PreferredBackend, TabName, Cwd string
}

type CreatedEndpoint struct {
	Backend, Handle, SessionOwner, WorkspaceID, TabID string
	Metadata                                          map[string]string
}

type SpawnEndpointStatus struct{ Alive bool }

type EndpointCapabilities interface {
	Create(CreateRequest) (CreatedEndpoint, error)
	Submit(CreatedEndpoint, string) error
	Probe(CreatedEndpoint) (SpawnEndpointStatus, error)
	Capture(CreatedEndpoint, int) (string, error)
	Dispose(CreatedEndpoint) error
}
