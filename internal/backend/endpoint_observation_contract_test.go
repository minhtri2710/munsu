package backend

import (
	"errors"
	"testing"
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

func matchPi(process string) bool { return process == "pi" }

func matchNothing(string) bool { return false }

func TestEndpointObservationContract(t *testing.T) {
	tests := []struct {
		name    string
		bk      Backend
		matches ProcessMatcher
		want    EndpointObservationState
	}{
		{"plain pane without a process reporter is starting", &contractBackend{alive: true}, matchPi, EndpointStarting},
		{"plain authoritative absent", &contractBackend{checkErr: ErrPaneNotFound}, matchPi, EndpointDead},
		{"plain probe failure", &contractBackend{checkErr: errors.New("timeout")}, matchPi, EndpointUnresponsive},
		{"plain false without authority", &contractBackend{}, matchPi, EndpointUnknown},
		{"process matching the harness is alive", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "pi"}, matchPi, EndpointAlive},
		{"process of another harness is starting", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "claude"}, matchPi, EndpointStarting},
		{"shell process is starting", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "zsh"}, matchPi, EndpointStarting},
		{"unreadable process evidence is starting", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "pi", processErr: errors.New("display-message failed")}, matchPi, EndpointStarting},
		{"matcher rejecting the process is starting", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "pi"}, matchNothing, EndpointStarting},
		{"no matcher never matches", &contractProcessBackend{contractBackend: contractBackend{alive: true}, process: "pi"}, nil, EndpointStarting},
		{"process evidence does not override authoritative absence", &contractProcessBackend{contractBackend: contractBackend{checkErr: ErrPaneNotFound}, process: "pi"}, matchPi, EndpointDead},
		{"agent alive", contractAgentBackend{alive: true, agentAlive: true}, nil, EndpointAlive},
		{"agent starting", contractAgentBackend{alive: true, agentAlive: false}, nil, EndpointStarting},
		{"agent authoritative absent", contractAgentBackend{checkErr: ErrPaneNotFound}, nil, EndpointDead},
		{"agent probe failure", contractAgentBackend{checkErr: errors.New("permission denied")}, nil, EndpointUnresponsive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ObserveEndpoint(tt.bk, "pane-1", tt.matches)
			if got.State() != tt.want {
				t.Fatalf("ObserveEndpoint() = %+v, want state %v", got, tt.want)
			}
			if (tt.name == "plain probe failure" || tt.name == "agent probe failure") && got.State() == EndpointDead {
				t.Fatal("operational probe failure must never be dead")
			}
		})
	}
}
