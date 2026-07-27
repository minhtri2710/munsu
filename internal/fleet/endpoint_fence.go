package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type BoundEndpoint struct {
	TaskID        string
	MetaPath      string
	Backend       string
	Handle        string
	SessionOwner  string
	CanonicalHome string
}

type EndpointScanner interface {
	ScanEndpoints(canonicalHome string) ([]BoundEndpoint, error)
}
type EndpointService interface {
	ProbeEndpoint(backend, handle string) (EndpointStatus, error)
	DisposeEndpoint(backend, handle string) error
}
type EndpointController interface {
	ProbeBoundEndpoint(BoundEndpoint) (EndpointStatus, error)
	DisposeBoundEndpoint(BoundEndpoint) error
}
type ServiceEndpointController struct{ Service EndpointService }

func (c ServiceEndpointController) ProbeBoundEndpoint(endpoint BoundEndpoint) (EndpointStatus, error) {
	if c.Service == nil || endpoint.Backend == "" || endpoint.Handle == "" {
		return EndpointStatus{}, fmt.Errorf("bound endpoint identity is incomplete")
	}
	return c.Service.ProbeEndpoint(endpoint.Backend, endpoint.Handle)
}
func (c ServiceEndpointController) DisposeBoundEndpoint(endpoint BoundEndpoint) error {
	if c.Service == nil || endpoint.Backend == "" || endpoint.Handle == "" {
		return fmt.Errorf("bound endpoint identity is incomplete")
	}
	return c.Service.DisposeEndpoint(endpoint.Backend, endpoint.Handle)
}

type TaskEndpointScanner struct{}

func (TaskEndpointScanner) ScanEndpoints(canonicalHome string) ([]BoundEndpoint, error) {
	stateDir := filepath.Join(canonicalHome, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var endpoints []BoundEndpoint
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		path := filepath.Join(stateDir, entry.Name())
		meta, err := readEndpointMeta(path)
		if err != nil {
			return nil, err
		}
		handle := meta["window"]
		if handle == "" {
			handle = meta["herdr_pane_id"]
		}
		if handle == "" {
			continue
		}
		backend := meta["backend"]
		if backend == "" {
			return nil, fmt.Errorf("endpoint meta %s has no bound backend", entry.Name())
		}
		owner := meta["herdr_session"]
		if owner == "" {
			owner = meta["herdr_workspace_id"]
		}
		if owner == "" {
			owner = meta["session_owner"]
		}
		endpoints = append(endpoints, BoundEndpoint{TaskID: strings.TrimSuffix(entry.Name(), ".meta"), MetaPath: path, Backend: backend, Handle: handle, SessionOwner: owner, CanonicalHome: canonicalHome})
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].TaskID < endpoints[j].TaskID })
	return endpoints, nil
}
func readEndpointMeta(path string) (map[string]string, error) {
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

func fenceEndpoints(canonical string, scanner EndpointScanner, controller EndpointController) ([]string, error) {
	if scanner == nil || controller == nil {
		return nil, fmt.Errorf("endpoint scanner and controller are required")
	}
	endpoints, err := scanner.ScanEndpoints(canonical)
	if err != nil {
		return nil, err
	}
	var evidence []string
	for _, endpoint := range endpoints {
		if endpoint.TaskID == "" || endpoint.Backend == "" || endpoint.Handle == "" || endpoint.CanonicalHome != canonical {
			return evidence, fmt.Errorf("unverified endpoint: %+v", endpoint)
		}
		status, err := controller.ProbeBoundEndpoint(endpoint)
		if err != nil {
			return evidence, fmt.Errorf("probing endpoint %s: %w", endpoint.TaskID, err)
		}
		if status.Alive {
			if err := controller.DisposeBoundEndpoint(endpoint); err != nil {
				return evidence, fmt.Errorf("disposing endpoint %s: %w", endpoint.TaskID, err)
			}
		}
		evidence = append(evidence, fmt.Sprintf("endpoint:%s:%s:%s", endpoint.TaskID, endpoint.Backend, endpoint.Handle))
	}
	after, err := scanner.ScanEndpoints(canonical)
	if err != nil {
		return evidence, err
	}
	if len(after) != 0 {
		return evidence, fmt.Errorf("endpoints remain after disposal: %+v", after)
	}
	return evidence, nil
}
