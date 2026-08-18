package backend

import "testing"

func TestAgentStatusAlive(t *testing.T) {
	for _, status := range []string{"working", "idle", "busy", "blocked", "unknown", "done"} {
		if !isAgentStatusAlive(status) {
			t.Errorf("status %q should be alive", status)
		}
	}
	for _, status := range []string{"", "none", "failed", "exited", "stopped"} {
		if isAgentStatusAlive(status) {
			t.Errorf("status %q should be terminal", status)
		}
	}
}

func TestAgentStatusReady(t *testing.T) {
	for _, status := range []string{"idle", "done", "Idle", "IDLE", " idle ", " Done "} {
		if !AgentStatusReady(status) {
			t.Errorf("status %q should be ready", status)
		}
	}
	for _, status := range []string{"", "working", "busy", "blocked", "unknown", "failed", "exited", "stopped", "Working", " blocked "} {
		if AgentStatusReady(status) {
			t.Errorf("status %q should not be ready", status)
		}
	}
}
