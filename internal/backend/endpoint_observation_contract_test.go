package backend

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

type contractBackend struct {
	alive    bool
	checkErr error
}

func (b *contractBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b *contractBackend) SendKeys(string, string) error            { return nil }
func (b *contractBackend) Capture(string, int) (string, error)      { return "", nil }
func (b *contractBackend) Teardown(string) error                    { return nil }
func (b *contractBackend) CheckAlive(string) (bool, error)          { return b.alive, b.checkErr }

type contractAgentBackend struct {
	alive      bool
	agentAlive bool
	checkErr   error
}

func (b contractAgentBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b contractAgentBackend) SendKeys(string, string) error            { return nil }
func (b contractAgentBackend) Capture(string, int) (string, error)      { return "", nil }
func (b contractAgentBackend) Teardown(string) error                    { return nil }
func (b contractAgentBackend) CheckAgentAlive(string) (bool, bool, error) {
	return b.alive, b.agentAlive, b.checkErr
}

// contractProcessBackend is a non-agent-aware backend that also reports the
// pane's foreground process (ForegroundProcessReporter).
type contractProcessBackend struct {
	contractBackend
	process    string
	processErr error
}

func (b *contractProcessBackend) ForegroundProcess(string) (string, error) {
	return b.process, b.processErr
}

func TestEndpointObservationContract(t *testing.T) {
	tests := []struct {
		name    string
		bk      Backend
		harness string
		want    EndpointObservationState
	}{
		{"plain pane without a process reporter is starting", &contractBackend{alive: true}, harness.Pi, EndpointStarting},
		{"plain authoritative absent", &contractBackend{checkErr: ErrPaneNotFound}, harness.Pi, EndpointDead},
		{"plain probe failure", &contractBackend{checkErr: errors.New("timeout")}, harness.Pi, EndpointUnresponsive},
		{"plain false without authority", &contractBackend{}, harness.Pi, EndpointUnknown},
		{"process matching the harness is alive", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "pi"}, harness.Pi, EndpointAlive},
		{"process of another harness is starting", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "claude"}, harness.Pi, EndpointStarting},
		{"shell process is starting", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "zsh"}, harness.Pi, EndpointStarting},
		{"unreadable process evidence is starting", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "pi", processErr: errors.New("display-message failed")}, harness.Pi, EndpointStarting},
		{"unknown harness never matches", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "pi"}, "no-such-harness", EndpointStarting},
		{"empty harness never matches", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "pi"}, "", EndpointStarting},
		{"process evidence does not override authoritative absence", &contractProcessBackend{contractBackend: contractBackend{checkErr: ErrPaneNotFound}, process: "pi"}, harness.Pi, EndpointDead},
		{"agent alive", contractAgentBackend{alive: true, agentAlive: true}, "", EndpointAlive},
		{"agent starting", contractAgentBackend{alive: true, agentAlive: false}, "", EndpointStarting},
		{"agent authoritative absent", contractAgentBackend{checkErr: ErrPaneNotFound}, "", EndpointDead},
		{"agent probe failure", contractAgentBackend{checkErr: errors.New("permission denied")}, "", EndpointUnresponsive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ObserveEndpoint(tt.bk, "pane-1", tt.harness)
			if got.State() != tt.want {
				t.Fatalf("ObserveEndpoint() = %+v, want state %v", got, tt.want)
			}
			if (tt.name == "plain probe failure" || tt.name == "agent probe failure") && got.State() == EndpointDead {
				t.Fatal("operational probe failure must never be dead")
			}
		})
	}
}
