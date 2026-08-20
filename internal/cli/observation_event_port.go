// CLI-side native observation event port (BEO-17/P1b).
//
// The composition root adapts backend adapters to the orchestrator's native
// event seam: it scans the home's exact bound endpoints (meta projection) and
// exposes an ObservationEventSource for every endpoint whose backend declares
// a native event surface. Endpoints whose backend has no event surface are
// omitted, so the orchestrator keeps pure polling for them. Wire/protocol
// detail stays in the backend adapter; this port only resolves which endpoint
// has a source and which opaque incarnation the orchestrator must match.
package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/home"
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

// Sources scans the home's bound endpoints and returns one EndpointSource per
// endpoint whose backend exposes a native event source. Endpoints without a
// source are omitted (pure polling for them). An endpoint is only included
// when the exact bound identity resolves and the resolved adapter implements
// ObservationEventSource.
func (p observationEventPort) Sources(homeDir string) ([]orchestrator.EndpointSource, error) {
	endpoints, err := scanBoundEndpoints(homeDir)
	if err != nil {
		return nil, err
	}
	var out []orchestrator.EndpointSource
	for _, ep := range endpoints {
		meta := ep.meta
		meta["home"] = ep.home
		bk, resolved, err := p.resolveFor(ep.home, meta)
		if err != nil || resolved != ep.backend {
			continue // unresolvable or mismatched binding → poll
		}
		src, ok := bk.(backend.ObservationEventSource)
		if !ok {
			continue // backend declares no native event surface → poll
		}
		out = append(out, orchestrator.EndpointSource{
			Endpoint: backend.EndpointRef{
				Backend: ep.backend,
				Handle:  ep.handle,
			},
			Incarnation: ep.meta["endpoint_incarnation"],
			Source:      src,
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

// boundEndpoint is one exact bound endpoint discovered from the home's state
// meta projection. The projection is diagnostic for event wiring: the
// orchestrator still re-probes the canonical binding before any policy
// decision.
type boundEndpoint struct {
	backend string
	handle  string
	meta    map[string]string
	home    string
}

// scanBoundEndpoints reads state/*.meta and returns the bound endpoints that
// carry a window handle and an explicit backend identity.
func scanBoundEndpoints(homeDir string) ([]boundEndpoint, error) {
	stateDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []boundEndpoint
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		if _, err := home.ReverseDurableKey(strings.TrimSuffix(entry.Name(), ".meta")); err != nil {
			continue
		}
		meta, err := readMetaFile(filepath.Join(stateDir, entry.Name()))
		if err != nil {
			continue
		}
		handle := meta["window"]
		if handle == "" {
			handle = meta["herdr_pane_id"]
		}
		if handle == "" {
			continue
		}
		bk := meta["backend"]
		if bk == "" {
			continue
		}
		// Captains are idle-by-default and supervised via status signals;
		// their panes are not event-wait targets.
		if meta["kind"] == "captain" {
			continue
		}
		out = append(out, boundEndpoint{backend: bk, handle: handle, meta: meta, home: homeDir})
	}
	return out, nil
}

// readMetaFile parses a flat key=value meta file.
func readMetaFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			out[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return out, nil
}
