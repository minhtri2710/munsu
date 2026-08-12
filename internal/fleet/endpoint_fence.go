package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type BoundEndpoint struct {
	TaskID       string
	MetaPath     string
	Backend      string
	Handle       string
	SessionOwner string
	WorkspaceID  string
	TabID        string
	// Canonical endpoint proof (when available from Task Authority); required
	// to authorize freshness before any disposal. .meta alone cannot authorize.
	LeaseID       string
	FenceToken    string
	Incarnation   string
	CanonicalHome string
}

type EndpointScanner interface {
	ScanEndpoints(canonicalHome string) ([]BoundEndpoint, error)
}
type EndpointService interface {
	ProbeEndpoint(BoundEndpoint) (EndpointStatus, error)
	DisposeEndpoint(BoundEndpoint) error
}
type EndpointController interface {
	ProbeBoundEndpoint(BoundEndpoint) (EndpointStatus, error)
	DisposeBoundEndpoint(BoundEndpoint) error
}
type ServiceEndpointController struct{ Service EndpointService }

func (c ServiceEndpointController) ProbeBoundEndpoint(e BoundEndpoint) (EndpointStatus, error) {
	if c.Service == nil || !validBoundEndpoint(e) {
		return EndpointStatus{}, fmt.Errorf("bound endpoint identity is incomplete")
	}
	return c.Service.ProbeEndpoint(e)
}
func (c ServiceEndpointController) DisposeBoundEndpoint(e BoundEndpoint) error {
	if c.Service == nil || !validBoundEndpoint(e) {
		return fmt.Errorf("bound endpoint identity is incomplete")
	}
	return c.Service.DisposeEndpoint(e)
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
		endpoints = append(endpoints, BoundEndpoint{TaskID: strings.TrimSuffix(entry.Name(), ".meta"), MetaPath: path, Backend: backend, Handle: handle, SessionOwner: meta["herdr_session"], WorkspaceID: meta["herdr_workspace_id"], TabID: meta["herdr_tab_id"], LeaseID: meta["endpoint_lease_id"], FenceToken: meta["endpoint_fence_token"], Incarnation: meta["endpoint_incarnation"], CanonicalHome: canonicalHome})
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
func validBoundEndpoint(e BoundEndpoint) bool {
	return e.TaskID != "" && e.Backend != "" && e.Handle != "" && e.CanonicalHome != ""
}
func sameBoundEndpoint(a, b BoundEndpoint) bool {
	return a.TaskID == b.TaskID && a.Backend == b.Backend && a.Handle == b.Handle && a.SessionOwner == b.SessionOwner && a.WorkspaceID == b.WorkspaceID && a.TabID == b.TabID && a.LeaseID == b.LeaseID && a.FenceToken == b.FenceToken && a.Incarnation == b.Incarnation && a.CanonicalHome == b.CanonicalHome
}

func fenceEndpoints(canonical string, scanner EndpointScanner, controller EndpointController) ([]string, error) {
	if scanner == nil || controller == nil {
		return nil, fmt.Errorf("endpoint scanner and controller are required")
	}
	before, err := scanner.ScanEndpoints(canonical)
	if err != nil {
		return nil, err
	}
	var evidence []string
	for _, endpoint := range before {
		if !validBoundEndpoint(endpoint) || endpoint.CanonicalHome != canonical {
			return evidence, fmt.Errorf("unverified endpoint: %+v", endpoint)
		}
		status, err := controller.ProbeBoundEndpoint(endpoint)
		if err != nil {
			return evidence, fmt.Errorf("probing endpoint %s: %w", endpoint.TaskID, err)
		}
		// Authorize freshness against the exact canonical proof of this bound
		// endpoint. .meta alone cannot authorize: an incomplete proof (missing
		// lease/fence/incarnation) or an ambiguous starting/unknown/
		// stale/unresponsive reading fails closed — metadata is retained and
		// nothing is disposed (BEO-16).
		auth := authorizeObservation(status, exactEndpointProof{
			backend: endpoint.Backend, handle: endpoint.Handle,
			incarnation: endpoint.Incarnation, leaseID: endpoint.LeaseID, fenceToken: endpoint.FenceToken,
		})
		switch {
		case auth.Absent():
			// Fleet-authorized, already gone: keep.
		case auth.Live():
			if err := controller.DisposeBoundEndpoint(endpoint); err != nil {
				return evidence, fmt.Errorf("disposing endpoint %s: %w", endpoint.TaskID, err)
			}
		default:
			// Ambiguous or unauthorized: keep metadata, fail closed.
			return evidence, fmt.Errorf("endpoint %s observation %s is not safe to fence (ambiguous cannot be disposed)", endpoint.TaskID, auth.State())
		}
		evidence = append(evidence, fmt.Sprintf("endpoint:%s:%s:%s", endpoint.TaskID, endpoint.Backend, endpoint.Handle))
	}
	after, err := scanner.ScanEndpoints(canonical)
	if err != nil {
		return evidence, err
	}
	if len(after) != len(before) {
		return evidence, fmt.Errorf("endpoint metadata set changed during fencing")
	}
	byTask := map[string]BoundEndpoint{}
	for _, e := range before {
		byTask[e.TaskID] = e
	}
	for _, e := range after {
		original, ok := byTask[e.TaskID]
		if !ok || !sameBoundEndpoint(original, e) {
			return evidence, fmt.Errorf("endpoint identity changed during fencing: %+v", e)
		}
		status, err := controller.ProbeBoundEndpoint(e)
		if err != nil {
			return evidence, fmt.Errorf("re-probing endpoint %s: %w", e.TaskID, err)
		}
		auth := authorizeObservation(status, exactEndpointProof{
			backend: e.Backend, handle: e.Handle,
			incarnation: e.Incarnation, leaseID: e.LeaseID, fenceToken: e.FenceToken,
		})
		if !auth.Absent() {
			return evidence, fmt.Errorf("endpoint %s observation %s after disposal, want dead/current authorized", e.TaskID, auth.State())
		}
	}
	return evidence, nil
}
