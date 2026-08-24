package cli

import (
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

func TestRecoverCaptainEndpointReportsFailedRecoveryStep(t *testing.T) {
	if err := recoverCaptainEndpoint(t.TempDir(), fleet.Info{}); err == nil {
		t.Fatal("expected failed captain recovery to be reported")
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
