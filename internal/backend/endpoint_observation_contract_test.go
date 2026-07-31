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
func (b *contractBackend) Alive(string) bool                        { return b.alive }
func (b *contractBackend) Teardown(string) error                    { return nil }
func (b *contractBackend) CheckAlive(string) (bool, error)          { return b.alive, b.checkErr }

type contractAgentBackend struct {
	alive      bool
	agentAlive bool
	checkErr  error
}

func (b contractAgentBackend) NewWindow(string, string) (string, error) { return "", nil }
func (b contractAgentBackend) SendKeys(string, string) error            { return nil }
func (b contractAgentBackend) Capture(string, int) (string, error)      { return "", nil }
func (b contractAgentBackend) Alive(string) bool                        { return b.alive }
func (b contractAgentBackend) Teardown(string) error                    { return nil }
func (b contractAgentBackend) CheckAgentAlive(string) (bool, bool, error) {
	return b.alive, b.agentAlive, b.checkErr
}

func TestEndpointObservationContract(t *testing.T) {
	tests := []struct {
		name string
		bk   Backend
		want EndpointObservationState
	}{
		{"plain alive", &contractBackend{alive: true}, EndpointAlive},
		{"plain authoritative absent", &contractBackend{checkErr: ErrPaneNotFound}, EndpointDead},
		{"plain probe failure", &contractBackend{checkErr: errors.New("timeout")}, EndpointUnresponsive},
		{"plain false without authority", &contractBackend{}, EndpointUnknown},
		{"agent alive", contractAgentBackend{alive: true, agentAlive: true}, EndpointAlive},
		{"agent starting", contractAgentBackend{alive: true, agentAlive: false}, EndpointStarting},
		{"agent authoritative absent", contractAgentBackend{checkErr: ErrPaneNotFound}, EndpointDead},
		{"agent probe failure", contractAgentBackend{checkErr: errors.New("permission denied")}, EndpointUnresponsive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ObserveBackendEndpoint(tt.bk, "pane-1")
			if got.State != tt.want {
				t.Fatalf("ObserveBackendEndpoint() = %+v, want state %v", got, tt.want)
			}
			if (tt.name == "plain probe failure" || tt.name == "agent probe failure") && got.State == EndpointDead {
				t.Fatal("operational probe failure must never be dead")
			}
		})
	}
}
