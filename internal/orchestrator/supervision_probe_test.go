package orchestrator

import (
	"errors"
	"testing"

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
