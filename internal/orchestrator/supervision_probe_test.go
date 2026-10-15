package orchestrator

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type watcherProbe struct {
	alive bool
	err   error
	meta  map[string]string
}

func (p *watcherProbe) Probe(_ string, meta map[string]string) (bool, error) {
	p.meta = meta
	return p.alive, p.err
}
func TestScanFleetProbesTheCanonicalHerdrIdentity(t *testing.T) {
	h := t.TempDir()
	seedBoundSoldier(t, h, "task", taskauthority.EndpointBinding{Backend: "herdr", Handle: "session-1:pane-1", SessionOwner: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1"})
	p := &watcherProbe{alive: true}
	if reasons := scanFleetWithProbe(h, false, p, testSeatLiveness, nil); len(reasons) != 0 {
		t.Fatalf("reasons=%+v", reasons)
	}
	if p.meta["window"] != "session-1:pane-1" || p.meta["session_owner"] != "session-1" || p.meta["workspace_id"] != "workspace-1" || p.meta["tab_id"] != "tab-1" {
		t.Fatalf("meta=%v", p.meta)
	}
}
func TestScanTaskWithProbeSurfacesProbeError(t *testing.T) {
	h := t.TempDir()
	reason := scanTaskWithProbe(h, "task", map[string]string{"window": "pane", "backend": "herdr"}, &watcherProbe{err: errors.New("backend unavailable")}, testSeatLiveness)
	if reason == nil || reason.Kind != "check" {
		t.Fatalf("reason=%+v", reason)
	}
}
