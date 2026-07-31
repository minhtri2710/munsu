package backend

import (
	"errors"
	"fmt"
)

type EndpointObservationState uint8

const (
	EndpointObservationInvalid EndpointObservationState = iota
	EndpointAlive
	EndpointStarting
	EndpointUnresponsive
	EndpointDead
	EndpointUnknown
	EndpointStaleIdentity
	EndpointUnresolved
)

func (s EndpointObservationState) String() string {
	switch s {
	case EndpointAlive:
		return "alive"
	case EndpointStarting:
		return "starting"
	case EndpointUnresponsive:
		return "unresponsive"
	case EndpointDead:
		return "dead"
	case EndpointUnknown:
		return "unknown"
	case EndpointStaleIdentity:
		return "stale-identity"
	case EndpointUnresolved:
		return "unresolved"
	default:
		return "invalid"
	}
}

func (s EndpointObservationState) Valid() bool {
	return s >= EndpointAlive && s <= EndpointUnresolved
}

type EndpointObservation struct {
	State           EndpointObservationState
	RecognizedAgent bool
	Busy            bool
	Detail          string
}

func (o EndpointObservation) Alive() bool { return o.State == EndpointAlive }

func ObservationFromProbeError(endpoint EndpointRef, err error) EndpointObservation {
	if err == nil {
		return EndpointObservation{State: EndpointUnknown}
	}
	if errors.Is(err, ErrPaneNotFound) {
		return EndpointObservation{State: EndpointDead, Detail: err.Error()}
	}
	return EndpointObservation{State: EndpointUnresponsive, Detail: fmt.Sprintf("probing %s/%s: %v", endpoint.Backend, endpoint.Handle, err)}
}

type endpointAliveChecker interface {
	CheckAlive(string) (bool, error)
}

func ObserveBackendEndpoint(bk Backend, handle string) EndpointObservation {
	if bk == nil || handle == "" {
		return EndpointObservation{State: EndpointUnknown, Detail: "endpoint identity is incomplete"}
	}
	if aware, ok := bk.(AgentAwareBackend); ok {
		paneAlive, agentAlive, err := aware.CheckAgentAlive(handle)
		if err != nil {
			return ObservationFromProbeError(EndpointRef{Handle: handle}, err)
		}
		if paneAlive && agentAlive {
			return EndpointObservation{State: EndpointAlive, RecognizedAgent: true}
		}
		if paneAlive {
			return EndpointObservation{State: EndpointStarting, Detail: "pane exists but no recognized live agent"}
		}
		return EndpointObservation{State: EndpointUnknown, Detail: "agent-aware backend returned no authoritative error"}
	}
	if checker, ok := bk.(endpointAliveChecker); ok {
		alive, err := checker.CheckAlive(handle)
		if err != nil {
			return ObservationFromProbeError(EndpointRef{Handle: handle}, err)
		}
		if alive {
			return EndpointObservation{State: EndpointAlive}
		}
		return EndpointObservation{State: EndpointUnknown, Detail: "backend returned not alive without authoritative absence"}
	}
	if bk.Alive(handle) {
		return EndpointObservation{State: EndpointAlive}
	}
	return EndpointObservation{State: EndpointUnknown, Detail: "backend has no authoritative probe"}
}
