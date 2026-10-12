package orchestrator

import "testing"

type recordingHooks struct {
	activations int
}

func (h *recordingHooks) Activate(string) { h.activations++ }

func TestWatcherHooksActivateAfterTheStartupCycle(t *testing.T) {
	home := t.TempDir()
	recoveryDone.Delete(home)
	hooks := &recordingHooks{}
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, &testMailboxSender{}, hooks, NoopRetirementPort{}, nil, acceptingCheckValidationPort{}, testTaskStatePort{}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, &testMailboxSender{}, hooks, NoopRetirementPort{}, nil, acceptingCheckValidationPort{}, testTaskStatePort{}); err != nil {
		t.Fatal(err)
	}
	if hooks.activations != 1 {
		t.Fatalf("hooks = %+v", hooks)
	}
}

func TestWatcherHooksAreIsolatedPerHomeAndInstance(t *testing.T) {
	homeA, homeB := t.TempDir(), t.TempDir()
	recoveryDone.Delete(homeA)
	recoveryDone.Delete(homeB)
	hooksA, hooksB := &recordingHooks{}, &recordingHooks{}
	if _, err := RunCycleWithProbeAndSender(homeA, testEndpointProbe{}, &testMailboxSender{}, hooksA, NoopRetirementPort{}, nil, acceptingCheckValidationPort{}, testTaskStatePort{}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunCycleWithProbeAndSender(homeB, testEndpointProbe{}, &testMailboxSender{}, hooksB, NoopRetirementPort{}, nil, acceptingCheckValidationPort{}, testTaskStatePort{}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunCycleWithProbeAndSender(homeA, testEndpointProbe{}, &testMailboxSender{}, hooksA, NoopRetirementPort{}, nil, acceptingCheckValidationPort{}, testTaskStatePort{}); err != nil {
		t.Fatal(err)
	}
	if hooksA.activations != 1 {
		t.Fatalf("hooks A = %+v", hooksA)
	}
	if hooksB.activations != 0 {
		t.Fatalf("hooks B = %+v", hooksB)
	}
}
