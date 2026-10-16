package orchestrator

import (
	"errors"
	"os"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type watcherProbe struct {
	alive bool
	err   error
	ep    taskauthority.EndpointBinding
}

func (p *watcherProbe) Probe(_ string, ep taskauthority.EndpointBinding, _ string) (bool, error) {
	p.ep = ep
	return p.alive, p.err
}
func TestScanFleetProbesTheCanonicalHerdrIdentity(t *testing.T) {
	h := t.TempDir()
	seedBoundSoldier(t, h, "task", taskauthority.EndpointBinding{Backend: "herdr", Handle: "session-1:pane-1", SessionOwner: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1"})
	p := &watcherProbe{alive: true}
	if reasons := scanFleetWithProbe(h, false, p, testSeatLiveness, nil); len(reasons) != 0 {
		t.Fatalf("reasons=%+v", reasons)
	}
	if p.ep.Handle != "session-1:pane-1" || p.ep.SessionOwner != "session-1" || p.ep.WorkspaceID != "workspace-1" || p.ep.TabID != "tab-1" {
		t.Fatalf("probed endpoint=%+v", p.ep)
	}
}
func TestScanTaskWithProbeSurfacesProbeError(t *testing.T) {
	h := t.TempDir()
	reason := scanTaskWithProbe(h, BoundEndpoint{TaskID: "task", Endpoint: taskauthority.EndpointBinding{Handle: "pane", Backend: "herdr"}}, &watcherProbe{err: errors.New("backend unavailable")}, testSeatLiveness)
	if reason == nil || reason.Kind != "check" {
		t.Fatalf("reason=%+v", reason)
	}
}

// A scan that could not list the bound tasks probed none of them, so it keeps
// every wake marker; a scan that listed them clears a resolved task's marker.
func TestScanFleetKeepsMarkersWhenBoundListingFails(t *testing.T) {
	for _, tc := range []struct {
		name       string
		corrupt    bool
		wantMarker bool
	}{
		{name: "listing fails", corrupt: true, wantMarker: true},
		{name: "listing succeeds", corrupt: false, wantMarker: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := t.TempDir()
			seedTask(t, h, "queued")
			if tc.corrupt {
				hm, err := mhome.Open(h)
				if err != nil {
					t.Fatal(err)
				}
				doc, err := hm.Path(mhome.RootState, "task-authority/tasks/queued/current.json")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(doc, []byte("not json"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := mhome.AppendStatus(h, "quiet", "working: building"); err != nil {
				t.Fatal(err)
			}
			marker := mhome.WatcherSeenMarkerPath(h, "quiet")
			if err := os.WriteFile(marker, []byte("fingerprint"), 0o600); err != nil {
				t.Fatal(err)
			}
			scanFleetWithProbe(h, true, testEndpointProbe{}, testSeatLiveness, nil)
			if _, err := os.Stat(marker); (err == nil) != tc.wantMarker {
				t.Fatalf("marker present = %v, want %v", err == nil, tc.wantMarker)
			}
		})
	}
}
