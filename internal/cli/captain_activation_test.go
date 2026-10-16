package cli

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

type sequenceCaptainProbe struct {
	results []fleet.CaptainProbeResult
	calls   int
}

func (p *sequenceCaptainProbe) Probe(string, map[string]string) (fleet.CaptainProbeResult, error) {
	result := p.results[p.calls]
	p.calls++
	return result, nil
}

func TestCaptainRecoveryErrFailsOnAnyFailedStep(t *testing.T) {
	for _, tc := range []struct {
		name    string
		steps   []fleet.StepResult
		wantErr bool
	}{
		{name: "a failed step fails recovery", steps: []fleet.StepResult{{Name: "provenance", State: fleet.StepOk}, {Name: "relaunch-pane", State: fleet.StepFailed}}, wantErr: true},
		{name: "ok and skipped steps pass", steps: []fleet.StepResult{{Name: "provenance", State: fleet.StepOk}, {Name: "relaunch-pane", State: fleet.StepSkipped}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := captainRecoveryErr(&fleet.RecoverResult{Steps: tc.steps})
			if (err != nil) != tc.wantErr || err != nil && !strings.Contains(err.Error(), "captain recovery failed") {
				t.Fatalf("captainRecoveryErr = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}

func TestCaptainRecoverCommandFailsOnFailedStep(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if err := fleet.Register(homeDir, "broken", t.TempDir(), "", "broken"); err != nil {
		t.Fatal(err)
	}
	_, err := runRoot(t, "captain", "recover", "broken", "--home", homeDir)
	if err == nil || !strings.Contains(err.Error(), "captain recovery failed") || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("captain recover = %v, want captain recovery failed on provenance", err)
	}
}

func TestRecoverCaptainEndpointReportsFailedRecoveryStep(t *testing.T) {
	err := recoverCaptainEndpoint(t.TempDir(), fleet.Info{})
	if err == nil || !strings.Contains(err.Error(), "captain recovery failed") {
		t.Fatalf("recoverCaptainEndpoint = %v, want captain recovery failed", err)
	}
}

func TestEnsureCaptainReadyDefersLiveAgentThatIsNotReady(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	if err := mhome.WriteMeta(parent, "captain:test", map[string]string{"kind": "captain", "home": captainHome}); err != nil {
		t.Fatal(err)
	}
	probe := &sequenceCaptainProbe{results: []fleet.CaptainProbeResult{{PaneAlive: true, AgentAlive: true, ReadyForPrompt: false, AgentStatus: "working"}}}
	recoverCalls := 0
	err := ensureCaptainReady(parent, fleet.Info{ID: "test", Home: captainHome}, probe, func(string, fleet.Info) error {
		recoverCalls++
		return nil
	})
	if err == nil || recoverCalls != 0 || probe.calls != 1 {
		t.Fatalf("err=%v recover=%d probes=%d", err, recoverCalls, probe.calls)
	}
}

func TestEnsureCaptainReadyWaitsForPostRecoveryRegistration(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	if err := mhome.WriteMeta(parent, "captain:test", map[string]string{"kind": "captain", "home": captainHome}); err != nil {
		t.Fatal(err)
	}
	probe := &sequenceCaptainProbe{results: []fleet.CaptainProbeResult{
		{PaneAlive: false, AgentAlive: false},
		{PaneAlive: false, AgentAlive: false},
		{PaneAlive: true, AgentAlive: true, ReadyForPrompt: true, AgentStatus: "idle"},
	}}
	recoverCalls := 0
	waitCalls := 0
	err := ensureCaptainReadyWithWait(parent, fleet.Info{ID: "test", Home: captainHome}, probe, func(string, fleet.Info) error {
		recoverCalls++
		return nil
	}, 3, func() { waitCalls++ })
	if err != nil {
		t.Fatal(err)
	}
	if recoverCalls != 1 || waitCalls != 1 || probe.calls != 3 {
		t.Fatalf("recover=%d waits=%d probes=%d", recoverCalls, waitCalls, probe.calls)
	}
}

func TestEnsureCaptainReadyFailsWhenRecoveryDoesNotRestoreAgent(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	if err := mhome.WriteMeta(parent, "captain:test", map[string]string{"kind": "captain", "home": captainHome}); err != nil {
		t.Fatal(err)
	}
	probe := &sequenceCaptainProbe{results: []fleet.CaptainProbeResult{
		{PaneAlive: false, AgentAlive: false},
		{PaneAlive: false, AgentAlive: false},
	}}
	if err := ensureCaptainReadyWithWait(parent, fleet.Info{ID: "test", Home: captainHome}, probe, func(string, fleet.Info) error { return nil }, 1, nil); err == nil {
		t.Fatal("expected unavailable after recovery")
	}
}
