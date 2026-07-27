package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

type endpointFake struct {
	endpoints [][]BoundEndpoint
	disposed  []string
	alive     bool
}

func (f *endpointFake) ScanEndpoints(string) ([]BoundEndpoint, error) {
	s := f.endpoints[0]
	f.endpoints = f.endpoints[1:]
	return s, nil
}
func (f *endpointFake) ProbeBoundEndpoint(BoundEndpoint) (EndpointStatus, error) {
	return EndpointStatus{Alive: f.alive}, nil
}
func (f *endpointFake) DisposeBoundEndpoint(e BoundEndpoint) error {
	f.disposed = append(f.disposed, e.TaskID)
	return nil
}
func TestTaskEndpointScannerReadsBoundMetadata(t *testing.T) {
	h := t.TempDir()
	os.MkdirAll(filepath.Join(h, "state"), 0700)
	os.WriteFile(filepath.Join(h, "state", "task.meta"), []byte("backend=tmux\nwindow=@1\nherdr_session=s1\n"), 0600)
	got, err := (TaskEndpointScanner{}).ScanEndpoints(h)
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if got[0].TaskID != "task" || got[0].Handle != "@1" || got[0].SessionOwner != "s1" {
		t.Fatalf("got=%+v", got[0])
	}
}

type endpointService struct {
	probe                EndpointStatus
	probeErr, disposeErr error
	disposed             string
}

func (s *endpointService) ProbeEndpoint(string, string) (EndpointStatus, error) {
	return s.probe, s.probeErr
}
func (s *endpointService) DisposeEndpoint(_, handle string) error {
	s.disposed = handle
	return s.disposeErr
}

func TestServiceEndpointControllerUsesBoundBackend(t *testing.T) {
	service := &endpointService{probe: EndpointStatus{Alive: true}}
	controller := ServiceEndpointController{Service: service}
	status, err := controller.ProbeBoundEndpoint(BoundEndpoint{Backend: "tmux", Handle: "@1"})
	if err != nil || !status.Alive {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if err := controller.DisposeBoundEndpoint(BoundEndpoint{Backend: "tmux", Handle: "@1"}); err != nil {
		t.Fatal(err)
	}
}
func TestServiceEndpointControllerFailsClosedOnUnknownBackend(t *testing.T) {
	controller := ServiceEndpointController{}
	if _, err := controller.ProbeBoundEndpoint(BoundEndpoint{Backend: "missing", Handle: "@1"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestFenceEndpointsDisposesAliveAndRescans(t *testing.T) {
	h := t.TempDir()
	e := BoundEndpoint{TaskID: "task", Backend: "tmux", Handle: "@1", CanonicalHome: h}
	f := &endpointFake{endpoints: [][]BoundEndpoint{{e}, {}}, alive: true}
	got, err := fenceEndpoints(h, f, f)
	if err != nil || len(got) != 1 || len(f.disposed) != 1 {
		t.Fatalf("got=%v disposed=%v err=%v", got, f.disposed, err)
	}
}
func TestFenceEndpointsFailsWhenEndpointRemains(t *testing.T) {
	h := t.TempDir()
	e := BoundEndpoint{TaskID: "task", Backend: "tmux", Handle: "@1", CanonicalHome: h}
	f := &endpointFake{endpoints: [][]BoundEndpoint{{e}, {e}}, alive: true}
	if _, err := fenceEndpoints(h, f, f); err == nil {
		t.Fatal("expected remain error")
	}
}
