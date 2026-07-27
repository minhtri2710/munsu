package supervision

import "testing"

type recordingHooks struct {
	startup, cycles, activations int
}

func (h *recordingHooks) Reconcile(_ string, startup bool) error {
	if startup {
		h.startup++
	} else {
		h.cycles++
	}
	return nil
}
func (h *recordingHooks) Activate(string) { h.activations++ }

func TestWatcherHooksRunStartupOnceThenCycleAndActivation(t *testing.T) {
	home := t.TempDir()
	recoveryDone.Delete(home)
	hooks := &recordingHooks{}
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, &testMailboxSender{}, hooks); err != nil {
		t.Fatal(err)
	}
	if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, &testMailboxSender{}, hooks); err != nil {
		t.Fatal(err)
	}
	if hooks.startup != 1 || hooks.cycles != 1 || hooks.activations != 1 {
		t.Fatalf("hooks = %+v", hooks)
	}
}

func TestWatcherHooksAreIsolatedPerHomeAndInstance(t *testing.T) {
	homeA, homeB := t.TempDir(), t.TempDir()
	recoveryDone.Delete(homeA)
	recoveryDone.Delete(homeB)
	hooksA, hooksB := &recordingHooks{}, &recordingHooks{}
	if _, err := RunCycleWithProbeAndSender(homeA, testEndpointProbe{}, &testMailboxSender{}, hooksA); err != nil {
		t.Fatal(err)
	}
	if _, err := RunCycleWithProbeAndSender(homeB, testEndpointProbe{}, &testMailboxSender{}, hooksB); err != nil {
		t.Fatal(err)
	}
	if _, err := RunCycleWithProbeAndSender(homeA, testEndpointProbe{}, &testMailboxSender{}, hooksA); err != nil {
		t.Fatal(err)
	}
	if hooksA.startup != 1 || hooksA.cycles != 1 || hooksA.activations != 1 {
		t.Fatalf("hooks A = %+v", hooksA)
	}
	if hooksB.startup != 1 || hooksB.cycles != 0 || hooksB.activations != 0 {
		t.Fatalf("hooks B = %+v", hooksB)
	}
}
