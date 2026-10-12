package cli

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
)

type probeBackend struct {
	aliveHandle string
	checkErr    error
	process     string
}

func (p probeBackend) ForegroundProcess(string) (string, error) { return p.process, nil }

func (p probeBackend) NewWindow(string, string) (string, error) { return "", nil }
func (p probeBackend) SendKeys(string, string) error            { return nil }
func (p probeBackend) Capture(string, int) (string, error)      { return "", nil }
func (p probeBackend) Alive(handle string) bool                 { return handle == p.aliveHandle }
func (p probeBackend) CheckAlive(handle string) (bool, error) {
	if p.checkErr != nil {
		return false, p.checkErr
	}
	return handle == p.aliveHandle, nil
}
func (p probeBackend) Teardown(string) error { return nil }

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

	alive, err := probe.Probe("/home", map[string]string{"backend": "herdr", "window": "session-1:pane-1", "session_owner": "session-1"})
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
	if status.Lifecycle == fleet.LifecycleAlive || status.State() != fleet.EndpointStarting {
		t.Fatalf("endpoint must be starting when the pane exists but the agent is not alive: %+v", status)
	}
}

func TestCLIEndpointProbePreservesBoundMetadata(t *testing.T) {
	var gotHome string
	var got map[string]string
	probe := cliEndpointProbe{resolve: func(home string, meta map[string]string) (backend.Backend, string, error) {
		gotHome = home
		got = meta
		return probeBackend{aliveHandle: "session-1:pane-1", process: "claude"}, "herdr", nil
	}}
	status, err := probe.ProbeEndpoint(fleet.EndpointRef{Backend: "herdr", Handle: "session-1:pane-1", SessionOwner: "session-1", WorkspaceID: "workspace-1", TabID: "tab-1", Home: "/home", Harness: "claude"})
	if err != nil || status.Lifecycle != fleet.LifecycleAlive {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if gotHome != "/home" || got["session_owner"] != "session-1" || got["workspace_id"] != "workspace-1" || got["tab_id"] != "tab-1" || got["window"] != "session-1:pane-1" {
		t.Fatalf("home=%q meta=%v", gotHome, got)
	}
}

func TestCLIEndpointProbeRejectsIncompleteAndUnknownIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend string
		handle  string
		want    string // "" allows
	}{
		{"incomplete identity", "", "", "bound endpoint identity is incomplete"},
		{"unknown blocks", "unknown", "p", `unsupported bound backend "unknown"`},
		{"zellij blocks", "zellij", "p", `unsupported bound backend "zellij"`},
		{"cmux blocks", "cmux", "p", `unsupported bound backend "cmux"`},
		{"orca blocks", "orca", "p", `unsupported bound backend "orca"`},
		{"herdr allows", "herdr", "p", ""},
		{"tmux allows", "tmux", "p", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := cliEndpointProbe{resolve: func(string, map[string]string) (backend.Backend, string, error) {
				return &captainProbeBackend{paneAlive: true, agentAlive: true}, tc.backend, nil
			}}
			_, err := probe.ProbeEndpoint(fleet.EndpointRef{Backend: tc.backend, Handle: tc.handle, Home: "/home"})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("ProbeEndpoint(%s) = %v, want acceptance", tc.backend, err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("ProbeEndpoint(%s) = %v, want %q", tc.backend, err, tc.want)
			}
		})
	}
}

func TestCLIEndpointProbeRejectsHerdrSessionMismatch(t *testing.T) {
	probe := cliEndpointProbe{resolve: func(string, map[string]string) (backend.Backend, string, error) {
		return &captainProbeBackend{paneAlive: true, agentAlive: true}, "herdr", nil
	}}
	for _, tc := range []struct {
		handle    string
		wantStale bool
	}{
		{"session-a:w1:p1", true},
		{"w1:p1", false},
	} {
		status, err := probe.ProbeEndpoint(fleet.EndpointRef{Backend: "herdr", Handle: tc.handle, SessionOwner: "session-b", Home: "/home"})
		if err != nil {
			t.Fatal(err)
		}
		if stale := status.State() == fleet.EndpointStaleIdentity; stale != tc.wantStale {
			t.Fatalf("handle %q: status=%+v, want stale-identity=%v", tc.handle, status, tc.wantStale)
		}
	}
}

func TestCLIEndpointProbeTaskProbeSessionOwnership(t *testing.T) {
	tests := []struct {
		name      string
		window    string
		session   string
		wantAlive bool
		wantErr   string
	}{
		{name: "pane in another herdr session than the task records is refused", window: "session-a:w1:p1", session: "session-b", wantErr: "herdr session ownership mismatch"},
		{name: "pane in the recorded herdr session is probed", window: "session-a:w1:p1", session: "session-a", wantAlive: true},
		{name: "bare pane id names no session and is probed", window: "w1:p1", session: "session-b", wantAlive: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := cliEndpointProbe{resolve: func(string, map[string]string) (backend.Backend, string, error) {
				return &captainProbeBackend{paneAlive: true, agentAlive: true}, "herdr", nil
			}}
			alive, err := probe.Probe("/home", map[string]string{"backend": "herdr", "window": tt.window, "session_owner": tt.session})
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr || alive {
					t.Fatalf("alive=%v err=%v, want refusal %q", alive, err, tt.wantErr)
				}
				return
			}
			if err != nil || alive != tt.wantAlive {
				t.Fatalf("alive=%v err=%v, want alive=%v", alive, err, tt.wantAlive)
			}
		})
	}
}
