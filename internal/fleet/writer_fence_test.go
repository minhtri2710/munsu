package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeArtifactScanner struct{ snapshots [][]WriterArtifact }

func (f *fakeArtifactScanner) Scan(string) ([]WriterArtifact, error) {
	s := f.snapshots[0]
	f.snapshots = f.snapshots[1:]
	return s, nil
}

type fakeProcessInventory struct{ snapshots [][]WriterProcess }

func (f *fakeProcessInventory) List(string) ([]WriterProcess, error) {
	s := f.snapshots[0]
	f.snapshots = f.snapshots[1:]
	return s, nil
}

type fakeEndpointDrainer struct {
	drained []string
	err     error
}

func (f *fakeEndpointDrainer) Drain(p WriterProcess) error {
	f.drained = append(f.drained, p.Endpoint)
	return f.err
}

type fakeExactController struct {
	terminated []int
	retired    []string
	err        error
	retireErr  error
}
type emptyEndpointFence struct{}

func (emptyEndpointFence) ScanEndpoints(string) ([]BoundEndpoint, error) { return nil, nil }
func (emptyEndpointFence) ProbeBoundEndpoint(BoundEndpoint) (EndpointStatus, error) {
	return EndpointStatus{}, nil
}
func (emptyEndpointFence) DisposeBoundEndpoint(BoundEndpoint) error { return nil }

type fakeProcessVerifier struct {
	dead bool
	err  error
}

func (f *fakeProcessVerifier) VerifyDead(WriterArtifact) (bool, error) { return f.dead, f.err }

func (f *fakeExactController) TerminateExact(p WriterProcess) error {
	f.terminated = append(f.terminated, p.PID)
	return f.err
}
func (f *fakeExactController) RetireArtifact(a WriterArtifact) error {
	f.retired = append(f.retired, a.Path)
	return f.retireErr
}

func process(home string) WriterProcess {
	canonical, err := canonicalHome(home)
	if err != nil {
		panic(err)
	}
	return WriterProcess{PID: 42, StartToken: "123", ExecutablePath: "/bin/munsu", CanonicalHome: canonical, Kind: "watcher", Endpoint: "pane-1", SessionOwner: "session-1"}
}
func artifact(homeDir string) WriterArtifact {
	canonical, err := canonicalHome(homeDir)
	if err != nil {
		panic(err)
	}
	return WriterArtifact{Path: "state/.watcher-identity", Kind: "watcher", PID: 42, StartToken: "123", ExecutablePath: "/bin/munsu", CanonicalHome: canonical, Endpoint: "pane-1", SessionOwner: "session-1"}
}
func fence(a [][]WriterArtifact, p [][]WriterProcess) (CompositeWriterFence, *fakeEndpointDrainer, *fakeExactController) {
	d := &fakeEndpointDrainer{}
	c := &fakeExactController{}
	return CompositeWriterFence{Artifacts: &fakeArtifactScanner{a}, Processes: &fakeProcessInventory{p}, Endpoints: d, Controller: c, Verifier: &fakeProcessVerifier{dead: true}}, d, c
}

func TestCompositeWriterFenceOSPhaseDoesNotRequireEndpointPhase(t *testing.T) {
	h := t.TempDir()
	f, _, _ := fence([][]WriterArtifact{{}, {}}, [][]WriterProcess{{}, {}})
	if result, err := f.FenceOSWriters(h); err != nil || !result.VerifiedQuiescent {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCompositeWriterFenceVerifiesAfterDrainTerminateCleanupAndRescan(t *testing.T) {
	h := t.TempDir()
	f, d, c := fence([][]WriterArtifact{{artifact(h)}, {}}, [][]WriterProcess{{process(h)}, {}})
	result, err := f.FenceWriters(h)
	if err != nil {
		t.Fatal(err)
	}
	if !result.VerifiedQuiescent || len(d.drained) != 1 || len(c.terminated) != 1 || len(c.retired) != 1 {
		t.Fatalf("result=%+v drain=%v terminate=%v retire=%v", result, d.drained, c.terminated, c.retired)
	}
}
func TestCompositeWriterFenceAcceptsSymlinkEquivalentHome(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f, _, _ := fence([][]WriterArtifact{{artifact(link)}, {}}, [][]WriterProcess{{process(real)}, {}})
	if _, err := f.FenceWriters(link); err != nil {
		t.Fatal(err)
	}
}
func TestCompositeWriterFenceRejectsPIDReuseStartMismatch(t *testing.T) {
	h := t.TempDir()
	a := artifact(h)
	p := process(h)
	p.StartToken = "124"
	f, _, c := fence([][]WriterArtifact{{a}}, [][]WriterProcess{{p}})
	if _, err := f.FenceWriters(h); err == nil || len(c.terminated) != 0 {
		t.Fatalf("err=%v terminated=%v", err, c.terminated)
	}
}
func TestCompositeWriterFenceRejectsExecutableMissing(t *testing.T) {
	h := t.TempDir()
	p := process(h)
	p.ExecutablePath = ""
	f, _, c := fence([][]WriterArtifact{{artifact(h)}}, [][]WriterProcess{{p}})
	if _, err := f.FenceWriters(h); err == nil || len(c.terminated) != 0 {
		t.Fatalf("err=%v terminated=%v", err, c.terminated)
	}
}
func TestCompositeWriterFenceRejectsEndpointOwnershipMismatch(t *testing.T) {
	h := t.TempDir()
	p := process(h)
	p.SessionOwner = "other"
	f, _, c := fence([][]WriterArtifact{{artifact(h)}}, [][]WriterProcess{{p}})
	if _, err := f.FenceWriters(h); err == nil || len(c.terminated) != 0 {
		t.Fatalf("err=%v terminated=%v", err, c.terminated)
	}
}
func TestCompositeWriterFenceRejectsProcessWithoutArtifact(t *testing.T) {
	h := t.TempDir()
	f, _, _ := fence([][]WriterArtifact{{}}, [][]WriterProcess{{process(h)}})
	if _, err := f.FenceWriters(h); err == nil {
		t.Fatal("expected error")
	}
}
func TestCompositeWriterFenceCleansVerifiedDeadStaleArtifact(t *testing.T) {
	h := t.TempDir()
	f, _, controller := fence([][]WriterArtifact{{artifact(h)}, {}}, [][]WriterProcess{{}, {}})
	result, err := f.FenceWriters(h)
	if err != nil {
		t.Fatal(err)
	}
	if !result.VerifiedQuiescent || len(controller.retired) != 1 {
		t.Fatalf("result=%+v retired=%v", result, controller.retired)
	}
}

func TestCompositeWriterFenceRejectsUnverifiedStaleArtifact(t *testing.T) {
	h := t.TempDir()
	f, _, controller := fence([][]WriterArtifact{{artifact(h)}}, [][]WriterProcess{{}})
	f.Verifier = &fakeProcessVerifier{dead: false}
	if _, err := f.FenceWriters(h); err == nil || len(controller.retired) != 0 {
		t.Fatalf("err=%v retired=%v", err, controller.retired)
	}
}
func TestCompositeWriterFenceFailsWhenProcessAppearsAfterRescan(t *testing.T) {
	h := t.TempDir()
	f, _, _ := fence([][]WriterArtifact{{}, {}}, [][]WriterProcess{{}, {process(h)}})
	if _, err := f.FenceWriters(h); err == nil {
		t.Fatal("expected error")
	}
}
func TestCompositeWriterFenceFailsWhenExitIsNotConfirmed(t *testing.T) {
	h := t.TempDir()
	f, _, controller := fence([][]WriterArtifact{{artifact(h)}}, [][]WriterProcess{{process(h)}})
	f.Verifier = &fakeProcessVerifier{dead: false}
	if _, err := f.FenceWriters(h); err == nil || len(controller.retired) != 0 {
		t.Fatalf("err=%v retired=%v", err, controller.retired)
	}
}

func TestCompositeWriterFenceFailsOnTerminationTimeoutWithoutCleanup(t *testing.T) {
	h := t.TempDir()
	f, _, c := fence([][]WriterArtifact{{artifact(h)}}, [][]WriterProcess{{process(h)}})
	c.err = errors.New("timeout")
	if _, err := f.FenceWriters(h); err == nil || len(c.retired) != 0 {
		t.Fatalf("err=%v retired=%v", err, c.retired)
	}
}
func TestCompositeWriterFenceFailsWhenArtifactRetirementFails(t *testing.T) {
	h := t.TempDir()
	f, _, controller := fence([][]WriterArtifact{{artifact(h)}}, [][]WriterProcess{{process(h)}})
	controller.retireErr = errors.New("retirement denied")
	if result, err := f.FenceWriters(h); err == nil || result.VerifiedQuiescent {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCompositeWriterFenceFailsOnPartialEndpointDrain(t *testing.T) {
	h := t.TempDir()
	f, d, c := fence([][]WriterArtifact{{artifact(h)}}, [][]WriterProcess{{process(h)}})
	d.err = errors.New("partial drain")
	if _, err := f.FenceWriters(h); err == nil || len(c.terminated) != 0 || len(c.retired) != 0 {
		t.Fatalf("err=%v terminated=%v retired=%v", err, c.terminated, c.retired)
	}
}
