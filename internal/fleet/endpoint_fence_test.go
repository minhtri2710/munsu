package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type endpointFake struct {
	endpoints [][]BoundEndpoint
	probes    []EndpointStatus
	probeErrs []error
	disposed  []BoundEndpoint
}

func (f *endpointFake) ScanEndpoints(string) ([]BoundEndpoint, error) {
	s := f.endpoints[0]
	f.endpoints = f.endpoints[1:]
	return s, nil
}
func (f *endpointFake) ProbeBoundEndpoint(BoundEndpoint) (EndpointStatus, error) {
	s := f.probes[0]
	f.probes = f.probes[1:]
	var err error
	if len(f.probeErrs) > 0 {
		err = f.probeErrs[0]
		f.probeErrs = f.probeErrs[1:]
	}
	return s, err
}
func (f *endpointFake) DisposeBoundEndpoint(e BoundEndpoint) error {
	f.disposed = append(f.disposed, e)
	return nil
}
func endpoint(home string) BoundEndpoint {
	return BoundEndpoint{TaskID: "task", Backend: "herdr", Handle: "pane-1", SessionOwner: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", CanonicalHome: home}
}

func TestTaskEndpointScannerReadsFullBoundMetadata(t *testing.T) {
	h := t.TempDir()
	os.MkdirAll(filepath.Join(h, "state"), 0700)
	os.WriteFile(filepath.Join(h, "state", "task.meta"), []byte("backend=herdr\nwindow=pane-1\nherdr_session=session-1\nherdr_workspace_id=workspace-1\nherdr_tab_id=tab-1\n"), 0600)
	got, err := (TaskEndpointScanner{}).ScanEndpoints(h)
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	want := endpoint(h)
	want.MetaPath = filepath.Join(h, "state", "task.meta")
	if !sameBoundEndpoint(got[0], want) {
		t.Fatalf("got=%+v want=%+v", got[0], want)
	}
}

type endpointService struct {
	gotProbe, gotDispose BoundEndpoint
	status               EndpointStatus
	err                  error
}

func (s *endpointService) ProbeEndpoint(e BoundEndpoint) (EndpointStatus, error) {
	s.gotProbe = e
	return s.status, s.err
}
func (s *endpointService) DisposeEndpoint(e BoundEndpoint) error { s.gotDispose = e; return s.err }
func TestServiceEndpointControllerPreservesFullIdentity(t *testing.T) {
	h := t.TempDir()
	e := endpoint(h)
	service := &endpointService{status: endpointStatusFromState(EndpointAlive)}
	controller := ServiceEndpointController{Service: service}
	if _, err := controller.ProbeBoundEndpoint(e); err != nil {
		t.Fatal(err)
	}
	if err := controller.DisposeBoundEndpoint(e); err != nil {
		t.Fatal(err)
	}
	if !sameBoundEndpoint(service.gotProbe, e) || !sameBoundEndpoint(service.gotDispose, e) {
		t.Fatalf("probe=%+v dispose=%+v", service.gotProbe, service.gotDispose)
	}
}
func TestServiceEndpointControllerFailsClosedOnIncompleteIdentity(t *testing.T) {
	controller := ServiceEndpointController{Service: &endpointService{}}
	if _, err := controller.ProbeBoundEndpoint(BoundEndpoint{Backend: "herdr", Handle: "pane"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestFenceEndpointsRetainsMetadataWhenEndpointIsDead(t *testing.T) {
	h := t.TempDir()
	e := endpoint(h)
	f := &endpointFake{endpoints: [][]BoundEndpoint{{e}, {e}}, probes: []EndpointStatus{endpointStatusFromState(EndpointAlive), endpointStatusFromState(EndpointDead)}}
	got, err := fenceEndpoints(h, f, f)
	if err != nil || len(got) != 1 || len(f.disposed) != 1 {
		t.Fatalf("got=%v disposed=%v err=%v", got, f.disposed, err)
	}
}
func TestFenceEndpointsFailsWhenStillAlive(t *testing.T) {
	h := t.TempDir()
	e := endpoint(h)
	f := &endpointFake{endpoints: [][]BoundEndpoint{{e}, {e}}, probes: []EndpointStatus{endpointStatusFromState(EndpointAlive), endpointStatusFromState(EndpointAlive)}}
	if _, err := fenceEndpoints(h, f, f); err == nil {
		t.Fatal("expected error")
	}
}
func TestFenceEndpointsFailsWhenEndpointResurfaces(t *testing.T) {
	h := t.TempDir()
	e := endpoint(h)
	f := &endpointFake{endpoints: [][]BoundEndpoint{{}, {e}}, probes: nil}
	if _, err := fenceEndpoints(h, f, f); err == nil {
		t.Fatal("expected error")
	}
}
func TestFenceEndpointsFailsOnIdentityReplacement(t *testing.T) {
	h := t.TempDir()
	before := endpoint(h)
	after := before
	after.Handle = "pane-2"
	f := &endpointFake{endpoints: [][]BoundEndpoint{{before}, {after}}, probes: []EndpointStatus{endpointStatusFromState(EndpointAlive)}}
	if _, err := fenceEndpoints(h, f, f); err == nil {
		t.Fatal("expected error")
	}
}
func TestFenceEndpointsFailsOnPostDisposalProbeError(t *testing.T) {
	h := t.TempDir()
	e := endpoint(h)
	f := &endpointFake{endpoints: [][]BoundEndpoint{{e}, {e}}, probes: []EndpointStatus{endpointStatusFromState(EndpointAlive), {}}, probeErrs: []error{nil, errors.New("probe failed")}}
	if _, err := fenceEndpoints(h, f, f); err == nil {
		t.Fatal("expected error")
	}
}

func TestFenceEndpointsFailsClosedOnUncertainInitialObservation(t *testing.T) {
	h := t.TempDir()
	e := endpoint(h)
	for _, state := range []EndpointObservationState{EndpointUnresponsive, EndpointUnknown, EndpointUnresolved, EndpointStaleIdentity} {
		t.Run(state.String(), func(t *testing.T) {
			f := &endpointFake{endpoints: [][]BoundEndpoint{{e}}, probes: []EndpointStatus{endpointStatusFromState(state)}}
			if _, err := fenceEndpoints(h, f, f); err == nil {
				t.Fatalf("expected %s to fail closed", state)
			}
		})
	}
}

func TestFenceEndpointsRequiresAuthoritativeDeadAfterDisposal(t *testing.T) {
	h := t.TempDir()
	e := endpoint(h)
	f := &endpointFake{endpoints: [][]BoundEndpoint{{e}, {e}}, probes: []EndpointStatus{endpointStatusFromState(EndpointStarting), endpointStatusFromState(EndpointUnknown)}}
	if _, err := fenceEndpoints(h, f, f); err == nil {
		t.Fatal("expected non-dead post-disposal observation to fail")
	}
}
