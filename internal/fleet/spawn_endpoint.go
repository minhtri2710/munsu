package fleet

type CreateRequest struct {
	Home, PreferredBackend, TabName, Cwd string
}

type CreatedEndpoint struct {
	Backend, Handle, SessionOwner, WorkspaceID, TabID string
	Metadata                                          map[string]string
}

type SpawnEndpointObservation = EndpointStatus

type EndpointCapabilities interface {
	Create(CreateRequest) (CreatedEndpoint, error)
	Submit(CreatedEndpoint, string) error
	Probe(CreatedEndpoint) (SpawnEndpointObservation, error)
	Capture(CreatedEndpoint, int) (string, error)
	Dispose(CreatedEndpoint) error
}
