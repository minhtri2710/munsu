package fleet

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestRecover_PiIntegrationStatusControlsRelaunch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      string
		wantLaunch int
		wantFailed int
	}{
		{name: "installed", state: "installed", wantLaunch: 1},
		{name: "absent", state: "absent", wantFailed: 1},
		{name: "drifted", state: "drifted", wantFailed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			home := seedCaptainForTest(t, parent, "pi-captain")
			writeCanonicalPiIntegration(t, home)
			writeCaptainMeta(t, parent, "pi-captain", home, "dead-window")
			launch := &countingLaunchEndpoint{}
			var probe ProbeEndpoint = &testProbeEndpoint{result: CaptainProbeResult{}}
			if tc.state == "installed" {
				// The relaunch must prove post-launch liveness: dead before
				// launch, alive on the first post-launch probe.
				probe = &testProbeEndpoint{results: []CaptainProbeResult{{}, {PaneAlive: true, AgentAlive: true}}}
			}
			result, err := Recover(parent, []Info{{ID: "pi-captain", Home: home}}, RecoverCapabilities{
				Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: tc.state, Message: "test status"}},
				Launch:      launch,
				Probe:       probe,
			})
			if err != nil {
				t.Fatal(err)
			}
			if launch.calls != tc.wantLaunch || result.Failed != tc.wantFailed {
				t.Fatalf("launch calls=%d failed=%d, want %d/%d; result=%+v", launch.calls, result.Failed, tc.wantLaunch, tc.wantFailed, result)
			}
		})
	}
}

func TestRecover_ArmedRelaunchGuardRefusesDuplicateLaunch(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "guarded-recover")
	writeCanonicalPiIntegration(t, captainHome)
	writeCaptainMeta(t, parent, "guarded-recover", captainHome, "dead-window")
	writeRelaunchGuard(t, parent, "guarded-recover", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	launch := &countingLaunchEndpoint{}

	res, err := Recover(parent, []Info{{ID: "guarded-recover", Home: captainHome}}, RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{result: CaptainProbeResult{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0 while relaunch guard armed", launch.calls)
	}
	if res.Failed != 1 || !strings.Contains(res.Entries[0].Error, "duplicate launch refused") {
		t.Fatalf("entry = %+v, want duplicate-launch refusal", res.Entries)
	}
}

func TestRecover_ExpiredRelaunchGuardAllowsFreshRelaunchAndProvesLiveness(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "expired-recover")
	writeCanonicalPiIntegration(t, captainHome)
	writeCaptainMeta(t, parent, "expired-recover", captainHome, "dead-window")
	writeRelaunchGuard(t, parent, "expired-recover", strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10))
	launch := &countingLaunchEndpoint{}

	res, err := Recover(parent, []Info{{ID: "expired-recover", Home: captainHome}}, RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{results: []CaptainProbeResult{{}, {PaneAlive: true, AgentAlive: true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if launch.calls != 1 || res.Relaunched != 1 || res.Failed != 0 {
		t.Fatalf("launch calls=%d relaunched=%d failed=%d, want 1/1/0 after guard expiry", launch.calls, res.Relaunched, res.Failed)
	}
	meta, err := home.ReadMeta(parent, taskIDForCaptain("expired-recover"))
	if err != nil {
		t.Fatalf("read relaunched meta: %v", err)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("relaunch_liveness = %q, want cleared after proven liveness", meta["relaunch_liveness"])
	}
	if _, ok := meta[relaunchGuardUntilField]; ok {
		t.Fatalf("%s = %q, want cleared after proven liveness", relaunchGuardUntilField, meta[relaunchGuardUntilField])
	}
}

func TestRecover_RelaunchProofProbeErrorFailsWithoutArming(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "probe-error-recover")
	writeCanonicalPiIntegration(t, captainHome)
	writeCaptainMeta(t, parent, "probe-error-recover", captainHome, "dead-window")
	launch := &countingLaunchEndpoint{}
	probe := &errorAfterLaunchProbe{}

	res, err := Recover(parent, []Info{{ID: "probe-error-recover", Home: captainHome}}, RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:      launch,
		Probe:       probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if launch.calls != 1 {
		t.Fatalf("launch calls = %d, want 1", launch.calls)
	}
	if res.Failed != 1 || !strings.Contains(res.Entries[0].Error, "post-launch liveness check failed") {
		t.Fatalf("entry = %+v, want post-launch probe failure", res.Entries)
	}
	meta, err := home.ReadMeta(parent, taskIDForCaptain("probe-error-recover"))
	if err != nil {
		t.Fatalf("read relaunched meta: %v", err)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("relaunch_liveness = %q, want unarmed on probe error", meta["relaunch_liveness"])
	}
}

func TestRecover_AliveCaptainClearsArmedRelaunchGuard(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "alive-recover")
	writeCanonicalPiIntegration(t, captainHome)
	writeCaptainMeta(t, parent, "alive-recover", captainHome, "dead-window")
	writeRelaunchGuard(t, parent, "alive-recover", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	launch := &countingLaunchEndpoint{}

	res, err := Recover(parent, []Info{{ID: "alive-recover", Home: captainHome}}, RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if launch.calls != 0 || res.Alive != 1 {
		t.Fatalf("launch calls=%d alive=%d, want 0/1", launch.calls, res.Alive)
	}
	meta, err := home.ReadMeta(parent, taskIDForCaptain("alive-recover"))
	if err != nil {
		t.Fatalf("read cleared guard meta: %v", err)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("relaunch_liveness = %q, want cleared", meta["relaunch_liveness"])
	}
	if _, ok := meta[relaunchGuardUntilField]; ok {
		t.Fatalf("%s = %q, want cleared", relaunchGuardUntilField, meta[relaunchGuardUntilField])
	}
}

func TestConverge_AutoRecoverRefusesArmedRelaunchGuard(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "guarded-converge")
	writeCanonicalPiIntegration(t, captainHome)
	writeCaptainMeta(t, parent, "guarded-converge", captainHome, "dead-window")
	writeRelaunchGuard(t, parent, "guarded-converge", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	launch := &countingLaunchEndpoint{}

	result, err := Converge(parent, []Info{{ID: "guarded-converge", Home: captainHome}}, ConvergeCapabilities{
		Continuity:   noopCaptainContinuity{},
		Messaging:    noopCaptainMessaging{},
		Watcher:      noopCaptainWatcher{},
		Notification: &captainNotificationTransport{acknowledged: true},
		Mailbox:      &captainTestMailboxSender{},
		Integration:  staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:       launch,
		Probe:        &testProbeEndpoint{result: CaptainProbeResult{}},
	})
	if err == nil {
		t.Fatal("expected converge failure for guarded auto-recover")
	}
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0 while relaunch guard armed", launch.calls)
	}
	step := findConvergeStep(result.Steps, "guarded-converge: liveness check")
	if step == nil || step.Status != ConvergeFailed || !strings.Contains(step.Detail, "duplicate launch refused") {
		t.Fatalf("liveness step = %+v, want duplicate-launch refusal", step)
	}
}

func TestConverge_AutoRecoverProvesLivenessAfterExpiredRelaunchGuard(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "expired-converge")
	writeCanonicalPiIntegration(t, captainHome)
	writeCaptainMeta(t, parent, "expired-converge", captainHome, "dead-window")
	writeRelaunchGuard(t, parent, "expired-converge", strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10))
	launch := &countingLaunchEndpoint{}

	result, err := Converge(parent, []Info{{ID: "expired-converge", Home: captainHome}}, ConvergeCapabilities{
		Continuity:   noopCaptainContinuity{},
		Messaging:    noopCaptainMessaging{},
		Watcher:      noopCaptainWatcher{},
		Notification: &captainNotificationTransport{acknowledged: true},
		Mailbox:      &captainTestMailboxSender{},
		Integration:  staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:       launch,
		Probe:        &testProbeEndpoint{results: []CaptainProbeResult{{}, {PaneAlive: true, AgentAlive: true}}},
	})
	if err != nil {
		t.Fatalf("converge error: %v", err)
	}
	if launch.calls != 1 {
		t.Fatalf("launch calls = %d, want 1 after guard expiry", launch.calls)
	}
	step := findConvergeStep(result.Steps, "expired-converge: liveness check")
	if step == nil || step.Status != ConvergeOK || !strings.Contains(step.Detail, "auto-recovered") {
		t.Fatalf("liveness step = %+v, want proven auto-recovery", step)
	}
	meta, err := home.ReadMeta(parent, taskIDForCaptain("expired-converge"))
	if err != nil {
		t.Fatalf("read relaunched meta: %v", err)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("relaunch_liveness = %q, want cleared after proven liveness", meta["relaunch_liveness"])
	}
	if _, ok := meta[relaunchGuardUntilField]; ok {
		t.Fatalf("%s = %q, want cleared after proven liveness", relaunchGuardUntilField, meta[relaunchGuardUntilField])
	}
}

func TestConverge_AliveCaptainClearsArmedRelaunchGuard(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "alive-converge")
	writeCanonicalPiIntegration(t, captainHome)
	writeCaptainMeta(t, parent, "alive-converge", captainHome, "dead-window")
	writeRelaunchGuard(t, parent, "alive-converge", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	launch := &countingLaunchEndpoint{}

	_, err := Converge(parent, []Info{{ID: "alive-converge", Home: captainHome}}, ConvergeCapabilities{
		Continuity:   noopCaptainContinuity{},
		Messaging:    noopCaptainMessaging{},
		Watcher:      noopCaptainWatcher{},
		Notification: &captainNotificationTransport{acknowledged: true},
		Mailbox:      &captainTestMailboxSender{},
		Integration:  staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:       launch,
		Probe:        &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}},
	})
	if err != nil {
		t.Fatalf("converge error: %v", err)
	}
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0 for alive captain", launch.calls)
	}
	meta, err := home.ReadMeta(parent, taskIDForCaptain("alive-converge"))
	if err != nil {
		t.Fatalf("read cleared guard meta: %v", err)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("relaunch_liveness = %q, want cleared", meta["relaunch_liveness"])
	}
	if _, ok := meta[relaunchGuardUntilField]; ok {
		t.Fatalf("%s = %q, want cleared", relaunchGuardUntilField, meta[relaunchGuardUntilField])
	}
}

func findConvergeStep(steps []ConvergeStepResult, name string) *ConvergeStepResult {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}

type errorAfterLaunchProbe struct{ calls int }

func (p *errorAfterLaunchProbe) Probe(string, map[string]string) (CaptainProbeResult, error) {
	p.calls++
	if p.calls == 1 {
		return CaptainProbeResult{}, nil
	}
	return CaptainProbeResult{}, errors.New("backend down")
}
