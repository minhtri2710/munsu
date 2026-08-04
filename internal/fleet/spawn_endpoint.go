package fleet

// CreateRequest is the typed create request for one Soldier launch endpoint.
// ReservationID and FenceToken are the one-time endpoint reservation identity
// the committed launch intent owns (CanonicalBeginSpawnRequest). A capability
// that supports reservation-aware find-or-create (ReentrantEndpointCapabilities)
// uses them to reuse the endpoint created by an earlier attempt of the same
// launch; capabilities without reservation support ignore them and create
// fresh.
type CreateRequest struct {
	Home, PreferredBackend, TabName, Cwd string
	ReservationID                        string
	FenceToken                           string
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

// ReentrantEndpointCapabilities is the optional reservation-aware endpoint
// capability. A capability that implements it proves find-or-create under the
// exact launch reservation: repeated CreateReserved calls with the same
// reservation identity return the SAME endpoint (the one created by an earlier
// attempt of the same launch), never a replacement. Capabilities without this
// contract can still create endpoints on the first attempt, but recovery after
// a crash between create and durable attach must fail closed (DEPENDENCY_REQUEST)
// instead of silently creating a replacement.
type ReentrantEndpointCapabilities interface {
	CreateReserved(CreateRequest) (CreatedEndpoint, error)
}
