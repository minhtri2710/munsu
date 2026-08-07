package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

// --- RecoverTransaction tests ---

func TestRecoverTransaction_ProvenanceFailureSkipsAll(t *testing.T) {
	parent := t.TempDir()
	// Home dir exists but has no provenance marker.
	smHome := filepath.Join(parent, "captains", "sm-bad")
	os.MkdirAll(smHome, 0755)

	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Launch: testLaunchEndpoint{}, Nudge: &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}, Probe: &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}}}
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
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Launch: testLaunchEndpoint{}, Nudge: &testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}}, Probe: &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}}}}
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

func TestRecoverIntegrationStatus_MissingCanonicalIntegrationFailsClosed(t *testing.T) {
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "absent", Message: "no integration manifest found — not installed"}}}}
	step := tx.stepIntegrationStatus(Info{Home: captainHomeWithHarness(t, "pi")})
	if step.State != StepFailed {
		t.Fatalf("state = %s, want %s", step.State, StepFailed)
	}
	if !strings.Contains(step.Detail, "integration absent") || !strings.Contains(step.Detail, "munsu integrate repair --harness pi --scope project") {
		t.Fatalf("detail = %q, want typed actionable repair", step.Detail)
	}
}

func TestRecoverIntegrationStatus_HealthyCanonicalIntegrationPermitsRelaunch(t *testing.T) {
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })

	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "healthy-integration")
	writeCanonicalPiIntegration(t, home)
	writeCaptainMeta(t, parent, "healthy-integration", home, "dead-window")
	launch := &countingLaunchEndpoint{}
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{result: CaptainProbeResult{}},
	}}

	result := tx.Recover(parent, Info{ID: "healthy-integration", Home: home})
	if launch.calls != 1 {
		t.Fatalf("launch calls = %d, want 1", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepOk || !strings.Contains(step.Detail, "relaunched") {
		t.Fatalf("relaunch step = %+v, want successful relaunch", step)
	}
}

func TestRecoverIntegrationStatus_MissingCanonicalIntegrationDoesNotRelaunch(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "missing-integration")
	writeCaptainMeta(t, parent, "missing-integration", home, "dead-window")
	launch := &countingLaunchEndpoint{}
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "absent"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{result: CaptainProbeResult{}},
	}}

	result := tx.Recover(parent, Info{ID: "missing-integration", Home: home})
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepSkipped || !strings.Contains(step.Detail, "integration") {
		t.Fatalf("relaunch step = %+v, want integration-blocked skip", step)
	}
}

func TestRecoverIntegrationStatus_DigestInvalidCanonicalIntegrationFailsClosed(t *testing.T) {
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "drifted", Message: "one or more integration artifacts are missing or modified"}}}}
	step := tx.stepIntegrationStatus(Info{Home: captainHomeWithHarness(t, "pi")})
	if step.State != StepFailed {
		t.Fatalf("state = %s, want %s", step.State, StepFailed)
	}
	if !strings.Contains(step.Detail, "integration drifted") || !strings.Contains(step.Detail, "munsu integrate repair --harness pi --scope project") {
		t.Fatalf("detail = %q, want typed actionable repair", step.Detail)
	}
}

func TestRecoverIntegrationStatus_DriftedCanonicalIntegrationDoesNotRelaunch(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "drifted-integration")
	writeCaptainMeta(t, parent, "drifted-integration", home, "dead-window")
	launch := &countingLaunchEndpoint{}
	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "drifted", Message: "digest mismatch"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{result: CaptainProbeResult{}},
	}}

	result := tx.Recover(parent, Info{ID: "drifted-integration", Home: home})
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepSkipped || !strings.Contains(step.Detail, "integration") {
		t.Fatalf("relaunch step = %+v, want integration-blocked skip", step)
	}
}

// captainHomeWithHarness creates a captain home whose published snapshot
// carries the given CaptainProfile harness. The snapshot is the ONLY captain
// harness identity source for recovery operations; no flat pins are consulted.
func captainHomeWithHarness(t *testing.T, name string) string {
	t.Helper()
	return captainHomeWithSnapshot(t, config.CaptainProfile{Harness: name})
}

type staticIntegrationPort struct{ status IntegrationStatus }

func (p staticIntegrationPort) EnsureCaptain(string) error { return nil }
func (p staticIntegrationPort) Status(string, string) (IntegrationStatus, error) {
	return p.status, nil
}

type countingStatusIntegrationPort struct{ calls int }

func (p *countingStatusIntegrationPort) EnsureCaptain(string) error { return nil }
func (p *countingStatusIntegrationPort) Status(string, string) (IntegrationStatus, error) {
	p.calls++
	return IntegrationStatus{State: "installed"}, nil
}

type countingLaunchEndpoint struct{ calls int }

func (e *countingLaunchEndpoint) Launch(string, LaunchRequest) (LaunchResult, error) {
	e.calls++
	return LaunchResult{Backend: "test", Window: "window"}, nil
}
func (e *countingLaunchEndpoint) Cleanup(string, LaunchResult) error { return nil }

// findStep locates a step by name in the result slice.
func findStep(steps []StepResult, name string) *StepResult {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}
