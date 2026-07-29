package cli

import (
	"fmt"
	"time"

	"github.com/minhtri2710/munsu/internal/fleet"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

type captainRecoverFunc func(string, fleet.Info) error

func ensureCaptainReady(parentHome string, sm fleet.Info, probe fleet.ProbeEndpoint, recoverCaptain captainRecoverFunc) error {
	return ensureCaptainReadyWithWait(parentHome, sm, probe, recoverCaptain, 5, func() { time.Sleep(200 * time.Millisecond) })
}

func ensureCaptainReadyWithWait(parentHome string, sm fleet.Info, probe fleet.ProbeEndpoint, recoverCaptain captainRecoverFunc, attempts int, wait func()) error {
	meta, err := mhome.ReadMeta(parentHome, "captain:"+sm.ID)
	if err != nil {
		return err
	}
	status, err := probe.Probe(sm.Home, meta)
	if err == nil && status.PaneAlive && status.AgentAlive {
		if status.ReadyForPrompt {
			return nil
		}
		return fmt.Errorf("captain endpoint alive but not ready (status=%s)", status.AgentStatus)
	}
	if recoverCaptain == nil {
		return fmt.Errorf("captain endpoint unavailable")
	}
	if err := recoverCaptain(parentHome, sm); err != nil {
		return err
	}
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		meta, err = mhome.ReadMeta(parentHome, "captain:"+sm.ID)
		if err != nil {
			return err
		}
		status, err = probe.Probe(sm.Home, meta)
		if err == nil && status.PaneAlive && status.AgentAlive && status.ReadyForPrompt {
			return nil
		}
		if attempt+1 < attempts && wait != nil {
			wait()
		}
	}
	return fmt.Errorf("captain endpoint unavailable after recovery")
}

func newCaptainRecoverTransaction() *fleet.RecoverTransaction {
	return &fleet.RecoverTransaction{Capabilities: fleet.RecoverCapabilities{
		Continuity: captainContinuityAdapter{notification: newSessionUplinkTransport()},
		Watcher:    captainWatcherAdapter{},
		Launch:     newSessionLaunchEndpoint(),
		Probe:      newSessionProbeEndpoint(),
		Nudge:      newSessionNudgeEndpoint(),
	}}
}

func recoverCaptainEndpoint(parentHome string, sm fleet.Info) error {
	result := newCaptainRecoverTransaction().Recover(parentHome, sm)
	for _, step := range result.Steps {
		if step.State == fleet.StepFailed {
			return fmt.Errorf("captain recovery failed: %s", result.StepsString())
		}
	}
	return nil
}
