package cli

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/session"
)

type probeBackend struct{ aliveHandle string }

func (p probeBackend) NewWindow(string, string) (string, error) { return "", nil }
func (p probeBackend) SendKeys(string, string) error            { return nil }
func (p probeBackend) Capture(string, int) (string, error)      { return "", nil }
func (p probeBackend) Alive(handle string) bool                 { return handle == p.aliveHandle }
func (p probeBackend) Teardown(string) error                    { return nil }

func TestCLIEndpointProbePreservesBoundMetadata(t *testing.T) {
	var gotHome string
	var got map[string]string
	probe := cliEndpointProbe{resolve: func(home string, meta map[string]string) (session.Backend, string, error) {
		gotHome = home
		got = meta
		return probeBackend{aliveHandle: "pane-1"}, "herdr", nil
	}}
	status, err := probe.ProbeEndpoint(fleet.EndpointRef{Backend: "herdr", Handle: "pane-1", SessionOwner: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", Home: "/home"})
	if err != nil || !status.Alive {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if gotHome != "/home" || got["herdr_session"] != "session-1" || got["herdr_workspace_id"] != "workspace-1" || got["herdr_tab_id"] != "tab-1" || got["window"] != "pane-1" {
		t.Fatalf("home=%q meta=%v", gotHome, got)
	}
}
