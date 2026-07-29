package cli

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
)

type probeBackend struct{ aliveHandle string }

func (p probeBackend) NewWindow(string, string) (string, error) { return "", nil }
func (p probeBackend) SendKeys(string, string) error            { return nil }
func (p probeBackend) Capture(string, int) (string, error)      { return "", nil }
func (p probeBackend) Alive(handle string) bool                 { return handle == p.aliveHandle }
func (p probeBackend) Teardown(string) error                    { return nil }

func TestCLIEndpointProbeTaskProbeUsesCaptainHome(t *testing.T) {
	var resolvedHome string
	probe := cliEndpointProbe{resolve: func(home string, _ map[string]string) (backend.Backend, string, error) {
		resolvedHome = home
		return &captainProbeBackend{paneAlive: true, agentAlive: true}, "herdr", nil
	}}

	alive, err := probe.Probe("/parent", map[string]string{"home": "/captain", "backend": "herdr", "window": "session-1:pane-1"})
	if err != nil || !alive {
		t.Fatalf("alive=%v err=%v", alive, err)
	}
	if resolvedHome != "/captain" {
		t.Fatalf("resolved home=%q want captain home", resolvedHome)
	}
}

func TestCLIEndpointProbeTaskProbeRequiresLiveAgent(t *testing.T) {
	probe := cliEndpointProbe{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &captainProbeBackend{alive: true, paneAlive: true, agentAlive: false}, "herdr", nil
	}}

	alive, err := probe.Probe("/home", map[string]string{"backend": "herdr", "window": "session-1:pane-1", "herdr_session": "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if alive {
		t.Fatal("task probe must be dead when the pane exists but the agent is not alive")
	}
}

func TestCLIEndpointProbeRequiresLiveAgent(t *testing.T) {
	probe := cliEndpointProbe{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &captainProbeBackend{alive: true, paneAlive: true, agentAlive: false}, "herdr", nil
	}}

	status, err := probe.ProbeEndpoint(fleet.EndpointRef{Backend: "herdr", Handle: "session-1:pane-1", SessionOwner: "session-1", Home: "/home"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Alive {
		t.Fatal("endpoint must be dead when the pane exists but the agent is not alive")
	}
}

func TestCLIEndpointProbePreservesBoundMetadata(t *testing.T) {
	var gotHome string
	var got map[string]string
	probe := cliEndpointProbe{resolve: func(home string, meta map[string]string) (backend.Backend, string, error) {
		gotHome = home
		got = meta
		return probeBackend{aliveHandle: "session-1:pane-1"}, "herdr", nil
	}}
	status, err := probe.ProbeEndpoint(fleet.EndpointRef{Backend: "herdr", Handle: "session-1:pane-1", SessionOwner: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", Home: "/home"})
	if err != nil || !status.Alive {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if gotHome != "/home" || got["herdr_session"] != "session-1" || got["herdr_workspace_id"] != "workspace-1" || got["herdr_tab_id"] != "tab-1" || got["window"] != "session-1:pane-1" {
		t.Fatalf("home=%q meta=%v", gotHome, got)
	}
}

func TestCLIEndpointProbeRejectsIncompleteAndUnknownIdentity(t *testing.T) {
	probe := cliEndpointProbe{}
	if _, err := probe.ProbeEndpoint(fleet.EndpointRef{}); err == nil {
		t.Fatal("expected incomplete error")
	}
	if _, err := probe.ProbeEndpoint(fleet.EndpointRef{Backend: "unknown", Handle: "p", Home: "/home"}); err == nil {
		t.Fatal("expected backend error")
	}
}

func TestCLIEndpointProbeRejectsHerdrSessionMismatch(t *testing.T) {
	probe := cliEndpointProbe{}
	_, err := probe.ProbeEndpoint(fleet.EndpointRef{Backend: "herdr", Handle: "session-a:pane-1", SessionOwner: "session-b", Home: "/home"})
	if err == nil {
		t.Fatal("expected session mismatch")
	}
}
