package fleet

// CreateRequest is the typed create request for one Soldier launch endpoint.
// ReservationID and FenceToken are the one-time endpoint reservation identity
// the committed launch intent owns (CanonicalBeginSpawnRequest). The
// reservation-aware create contract is MANDATORY for canonical launch-intent
// runs from the FIRST attempt: the capability finds-or-creates the endpoint
// under the exact reservation so recovery of a crash between create and
// durable attach returns the SAME endpoint, never a replacement.
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

// EndpointCapabilities is the single mandatory endpoint lifecycle contract of
// the canonical launch path. CreateReserved is reservation-aware
// find-or-create: every call consumes the exact ReservationID/FenceToken of
// the launch intent and returns the SAME endpoint for the same reservation
// (created on first use, re-adopted on recovery). There is no unreserved
// create path — a capability that cannot express the owner-clean contract is
// not a valid EndpointCapabilities for a canonical launch.
type EndpointCapabilities interface {
	CreateReserved(CreateRequest) (CreatedEndpoint, error)
	Submit(CreatedEndpoint, string) error
	Probe(CreatedEndpoint) (SpawnEndpointObservation, error)
	Capture(CreatedEndpoint, int) (string, error)
	Dispose(CreatedEndpoint) error
}
