package fleet

import (
	"errors"
	"strings"
	"testing"
)

// TestCheckAliveWithProbe_StrictStates proves the ONE strict decision rule:
// only authoritative pane absence (Absent) is CaptainDead; pane-present/
// no-agent, generic probe errors, and missing launch evidence never authorize
// relaunch.
func TestCheckAliveWithProbe_StrictStates(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "strict-states")
	writeCaptainMeta(t, parent, "strict-states", home, "dead-window")

	state, err := checkAliveWithProbe(parent, Info{ID: "strict-states", Home: home}, &testProbeEndpoint{result: CaptainProbeResult{Absent: true}})
	if err != nil || state != CaptainDead {
		t.Fatalf("Absent evidence: state=%v err=%v, want CaptainDead", state, err)
	}

	state, err = checkAliveWithProbe(parent, Info{ID: "strict-states", Home: home}, &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: false}})
	if state != CaptainUnproven || err == nil || !strings.Contains(err.Error(), "not authoritatively absent") {
		t.Fatalf("no-agent evidence: state=%v err=%v, want CaptainUnproven fail-closed", state, err)
	}

	state, err = checkAliveWithProbe(parent, Info{ID: "strict-states", Home: home}, &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}})
	if err != nil || state != CaptainAlive {
		t.Fatalf("alive evidence: state=%v err=%v, want CaptainAlive", state, err)
	}

	state, err = checkAliveWithProbe(parent, Info{ID: "strict-states", Home: home}, &testProbeEndpoint{err: errors.New("backend down")})
	if state != CaptainUnproven || err == nil {
		t.Fatalf("probe error: state=%v err=%v, want CaptainUnproven fail-closed", state, err)
	}

	// No launch evidence: never launched.
	state, err = checkAliveWithProbe(parent, Info{ID: "never-launched", Home: home}, &testProbeEndpoint{result: CaptainProbeResult{Absent: true}})
	if err != nil || state != CaptainSeeded {
		t.Fatalf("no-meta evidence: state=%v err=%v, want CaptainSeeded", state, err)
	}
}

// TestProbeLiveness_StrictDeadOnly proves ProbeLiveness reports "dead" only
// for authoritative pane absence; pane-present/no-agent and generic errors
// fail closed as "unknown".
func TestProbeLiveness_StrictDeadOnly(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "strict-probe")
	writeCaptainMeta(t, parent, "strict-probe", home, "dead-window")
	registered := []Info{{ID: "strict-probe", Home: home}}

	probes := ProbeLiveness(parent, registered, &testProbeEndpoint{result: CaptainProbeResult{Absent: true}})
	if len(probes) != 1 || probes[0].Status != "dead" {
		t.Fatalf("Absent probes = %+v, want one dead", probes)
	}

	probes = ProbeLiveness(parent, registered, &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: false}})
	if len(probes) != 1 || probes[0].Status != "unknown" {
		t.Fatalf("no-agent probes = %+v, want unknown (never dead)", probes)
	}

	probes = ProbeLiveness(parent, registered, &testProbeEndpoint{err: errors.New("boom")})
	if len(probes) != 1 || probes[0].Status != "unknown" {
		t.Fatalf("error probes = %+v, want unknown", probes)
	}

	probes = ProbeLiveness(parent, registered, &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}})
	if len(probes) != 1 || probes[0].Status != "alive" {
		t.Fatalf("alive probes = %+v, want alive", probes)
	}
}

// TestConverge_StrictDeadOnlyNoAgentFailsClosed proves Converge's auto-recover
// path never launches on pane-present/no-agent evidence: it fails closed.
func TestConverge_StrictDeadOnlyNoAgentFailsClosed(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "noagent-converge")
	writeCaptainMeta(t, parent, "noagent-converge", captainHome, "dead-window")
	launch := &countingLaunchEndpoint{}

	result, err := Converge(parent, []Info{{ID: "noagent-converge", Home: captainHome}}, ConvergeCapabilities{
		Continuity:   noopCaptainContinuity{},
		Messaging:    noopCaptainMessaging{},
		Watcher:      noopCaptainWatcher{},
		Notification: &captainNotificationTransport{acknowledged: true},
		Mailbox:      &captainTestMailboxSender{},
		Integration:  staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:       launch,
		Probe:        &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: false}},
	})
	if err == nil {
		t.Fatal("expected fail-closed converge error for pane-present/no-agent")
	}
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0 (pane-present/no-agent fails closed)", launch.calls)
	}
	step := findConvergeStep(result.Steps, "noagent-converge: liveness check")
	if step == nil || step.Status != ConvergeFailed || !strings.Contains(step.Detail, "not authoritatively absent") {
		t.Fatalf("liveness step = %+v, want fail-closed step naming strict-dead-only", step)
	}
}

// TestConverge_StrictDeadOnlyAuthoritativeAbsenceRelaunches proves Converge's
// auto-recover path launches only when the pane is authoritatively absent.
func TestConverge_StrictDeadOnlyAuthoritativeAbsenceRelaunches(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "absent-converge")
	writeCaptainMeta(t, parent, "absent-converge", captainHome, "dead-window")
	writeCanonicalPiIntegration(t, captainHome)
	launch := &countingLaunchEndpoint{}

	result, err := Converge(parent, []Info{{ID: "absent-converge", Home: captainHome}}, ConvergeCapabilities{
		Continuity:   noopCaptainContinuity{},
		Messaging:    noopCaptainMessaging{},
		Watcher:      noopCaptainWatcher{},
		Notification: &captainNotificationTransport{acknowledged: true},
		Mailbox:      &captainTestMailboxSender{},
		Integration:  staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:       launch,
		Probe:        &testProbeEndpoint{results: []CaptainProbeResult{{Absent: true}, {PaneAlive: true, AgentAlive: true}}},
	})
	if err != nil {
		t.Fatalf("converge error: %v", err)
	}
	if launch.calls != 1 {
		t.Fatalf("launch calls = %d, want 1 (authoritative absence authorizes relaunch)", launch.calls)
	}
	step := findConvergeStep(result.Steps, "absent-converge: liveness check")
	if step == nil || step.Status != ConvergeOK || !strings.Contains(step.Detail, "auto-recovered") {
		t.Fatalf("liveness step = %+v, want proven auto-recovery", step)
	}
}

// TestRecoverTransaction_NoAgentFailsClosed proves RecoverTransaction.
// stepRelaunch never relaunches on pane-present/no-agent evidence.
func TestRecoverTransaction_NoAgentFailsClosed(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "tx-noagent")
	writeCaptainMeta(t, parent, "tx-noagent", home, "dead-window")
	launch := &countingLaunchEndpoint{}

	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: false}},
	}}
	result := tx.Recover(parent, Info{ID: "tx-noagent", Home: home})
	if launch.calls != 0 {
		t.Fatalf("launch calls = %d, want 0 (pane-present/no-agent fails closed)", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepFailed || !strings.Contains(step.Detail, "not authoritatively absent") {
		t.Fatalf("relaunch step = %+v, want fail-closed step naming strict-dead-only", step)
	}
}

// TestRecoverTransaction_AuthoritativeAbsenceRelaunches preserves the
// ErrPaneNotFound relaunch contract through RecoverTransaction.stepRelaunch.
func TestRecoverTransaction_AuthoritativeAbsenceRelaunches(t *testing.T) {
	parent := t.TempDir()
	home := seedCaptainForTest(t, parent, "tx-absent")
	writeCaptainMeta(t, parent, "tx-absent", home, "dead-window")
	writeCanonicalPiIntegration(t, home)
	launch := &countingLaunchEndpoint{}

	tx := &RecoverTransaction{Capabilities: RecoverCapabilities{
		Integration: staticIntegrationPort{status: IntegrationStatus{Harness: "pi", Scope: "project", State: "installed"}},
		Launch:      launch,
		Probe:       &testProbeEndpoint{results: []CaptainProbeResult{{Absent: true}, {PaneAlive: true, AgentAlive: true}}},
	}}
	result := tx.Recover(parent, Info{ID: "tx-absent", Home: home})
	if launch.calls != 1 {
		t.Fatalf("launch calls = %d, want 1 (authoritative absence authorizes relaunch)", launch.calls)
	}
	step := findStep(result.Steps, "relaunch-pane")
	if step == nil || step.State != StepOk || !strings.Contains(step.Detail, "relaunched") {
		t.Fatalf("relaunch step = %+v, want successful relaunch", step)
	}
}
