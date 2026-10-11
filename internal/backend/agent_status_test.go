package backend

import "testing"

// TestAgentStatusClassification is the one table of herdr agent-status words:
// every reader (activity hint, liveness, prompt readiness) classifies through
// classifyAgentStatus, after case and space normalization.
func TestAgentStatusClassification(t *testing.T) {
	for _, tc := range []struct {
		status   string
		activity Activity
		alive    bool
		ready    bool
	}{
		{"idle", ActivityIdle, true, true},
		{"done", ActivityIdle, true, true},
		{"working", ActivityBusy, true, false},
		{"blocked", ActivityBlocked, true, false},
		{"unknown", ActivityUnknown, true, false},
		{"Working", ActivityBusy, true, false},
		{" idle ", ActivityIdle, true, true},
		{" Done ", ActivityIdle, true, true},
		{"BLOCKED", ActivityBlocked, true, false},
		{"", ActivityUnknown, false, false},
		{"none", ActivityUnknown, false, false},
		{"exited", ActivityUnknown, false, false},
	} {
		if got := NormalizeActivityHint(tc.status); got != tc.activity {
			t.Errorf("NormalizeActivityHint(%q) = %v, want %v", tc.status, got, tc.activity)
		}
		if got := isAgentStatusAlive(tc.status); got != tc.alive {
			t.Errorf("isAgentStatusAlive(%q) = %v, want %v", tc.status, got, tc.alive)
		}
		if got := AgentStatusReady(tc.status); got != tc.ready {
			t.Errorf("AgentStatusReady(%q) = %v, want %v", tc.status, got, tc.ready)
		}
	}
}
