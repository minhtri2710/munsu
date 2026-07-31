package fleet

import "github.com/minhtri2710/munsu/internal/backend"

type EndpointObservationState = backend.EndpointObservationState
type EndpointStatus = backend.EndpointObservation

const (
	EndpointObservationInvalid = backend.EndpointObservationInvalid
	EndpointAlive              = backend.EndpointAlive
	EndpointStarting           = backend.EndpointStarting
	EndpointUnresponsive       = backend.EndpointUnresponsive
	EndpointDead               = backend.EndpointDead
	EndpointUnknown            = backend.EndpointUnknown
	EndpointStaleIdentity      = backend.EndpointStaleIdentity
	EndpointUnresolved         = backend.EndpointUnresolved
)
