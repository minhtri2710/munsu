package captain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- RecoverTransaction tests ---

func TestRecoverTransaction_ProvenanceFailureSkipsAll(t *testing.T) {
	parent := t.TempDir()
	// Home dir exists but has no provenance marker.
	smHome := filepath.Join(parent, "captains", "sm-bad")
	os.MkdirAll(smHome, 0755)

	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Launch: testLaunchEndpoint{}, Nudge: &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}, Probe: &testProbeEndpoint{result: ProbeResult{PaneAlive: true, AgentAlive: true}}}}
	res := tx.Recover(parent, Info{ID: "sm-bad", Home: smHome})

	if len(res.Steps) != 11 {
		t.Fatalf("expected 11 steps, got %d", len(res.Steps))
	}

	// Provenance must fail.
	if res.Steps[0].Name != "provenance" || res.Steps[0].State != StepFailed {
		t.Errorf("provenance step expected failed, got %s/%s", res.Steps[0].Name, res.Steps[0].State)
	}

	// All subsequent steps must be skipped.
	for i := 1; i < len(res.Steps); i++ {
		if res.Steps[i].State != StepSkipped {
			t.Errorf("step %d (%s) expected skipped, got %s", i, res.Steps[i].Name, res.Steps[i].State)
		}
	}
}

func TestRecoverTransaction_EmptyHomeFailed(t *testing.T) {
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Launch: testLaunchEndpoint{}, Nudge: &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}, Probe: &testProbeEndpoint{result: ProbeResult{PaneAlive: true, AgentAlive: true}}}}
	res := tx.Recover(t.TempDir(), Info{ID: "empty", Home: ""})

	if len(res.Steps) != 11 {
		t.Fatalf("expected 10 steps, got %d", len(res.Steps))
	}

	if res.Steps[0].Name != "provenance" || res.Steps[0].State != StepFailed {
		t.Errorf("provenance should fail for empty home: %s/%s", res.Steps[0].Name, res.Steps[0].State)
	}
	if !strings.Contains(res.Steps[0].Detail, "missing home path") {
		t.Errorf("detail should mention missing home: %s", res.Steps[0].Detail)
	}
}

func TestRecoverTransaction_StepsString(t *testing.T) {
	res := &RecoverResult{Steps: []StepResult{
		{Name: "provenance", State: StepOk, Detail: "valid"},
		{Name: "config-validation", State: StepFailed, Detail: "missing config"},
		{Name: "relaunch-pane", State: StepSkipped, Detail: "not launched"},
	}}
	s := res.StepsString()
	if !strings.Contains(s, "provenance: ok (valid)") {
		t.Errorf("StepsString missing ok step: %s", s)
	}
	if !strings.Contains(s, "config-validation: FAILED (missing config)") {
		t.Errorf("StepsString missing failed step: %s", s)
	}
	if !strings.Contains(s, "relaunch-pane: skipped (not launched)") {
		t.Errorf("StepsString missing skipped step: %s", s)
	}
}

func TestRecoverTransaction_EmptyStepsString(t *testing.T) {
	empty := (&RecoverResult{}).StepsString()
	if empty != "no recovery steps" {
		t.Errorf("empty StepsString() = %q", empty)
	}
}

// findStep locates a step by name in the result slice.
func findStep(steps []StepResult, name string) *StepResult {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}
