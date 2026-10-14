// CLI-side native observation event port.
//
// The composition root adapts backend adapters to the orchestrator's native
// event seam: it lists the home's live, bound soldier endpoints from the
// canonical store (orchestrator.LiveBoundEndpoints) and exposes an
// ObservationEventSource for every endpoint whose backend declares a native
// event surface. Endpoints whose backend has no event surface are omitted, so
// the orchestrator keeps pure polling for them. Wire/protocol detail stays in
// the backend adapter; this port only resolves which endpoint has a source.
package cli

import (
	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// observationEventPort resolves native event sources for the bound endpoints
// of a home. resolve is injectable for tests (defaults to BackendForTask).
type observationEventPort struct {
	resolve func(string, map[string]string) (backend.Backend, string, error)
}

// runtimeObservationEventPort builds the production event port over the
// explicit backend resolution path.
func runtimeObservationEventPort() orchestrator.ObservationEventPort {
	return observationEventPort{resolve: backend.BackendForTask}
}

// Sources lists the home's bound endpoints and returns one EndpointSource per
// endpoint whose backend exposes a native event source. Endpoints without a
// source are omitted (pure polling for them). An endpoint is only included
// when the exact bound identity resolves and the resolved adapter implements
// ObservationEventSource.
func (p observationEventPort) Sources(homeDir string) ([]orchestrator.EndpointSource, error) {
	endpoints, err := orchestrator.LiveBoundEndpoints(homeDir)
	if err != nil {
		return nil, err
	}
	var out []orchestrator.EndpointSource
	for _, ep := range endpoints {
		meta := ep.Meta
		meta["home"] = homeDir
		bk, resolved, err := p.resolveFor(homeDir, meta)
		if err != nil || resolved != meta["backend"] {
			continue // unresolvable or mismatched binding → poll
		}
		src, ok := bk.(backend.ObservationEventSource)
		if !ok {
			continue // backend declares no native event surface → poll
		}
		out = append(out, orchestrator.EndpointSource{
			Endpoint: backend.EndpointRef{
				Backend: meta["backend"],
				Handle:  meta["window"],
			},
			Source: src,
		})
	}
	return out, nil
}

// resolveFor routes through the injectable resolver, defaulting to the
// production BackendForTask path when nil.
func (p observationEventPort) resolveFor(homeDir string, meta map[string]string) (backend.Backend, string, error) {
	resolve := p.resolve
	if resolve == nil {
		resolve = backend.BackendForTask
	}
	return resolve(homeDir, meta)
}
