package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

var (
	resolveWakeMode    = orchestrator.ResolveWakeDeliveryMode
	resolveWakeTarget  = orchestrator.ResolveTargetWithSource
	validateWakeTarget = orchestrator.ValidateTargetOwnership
	resolveWakeBackend = backend.BackendForTask
	probeWakeEndpoint  = probeCaptainBackend
	claimWake          = orchestrator.ClaimWakes
	submitWakePrompt   = backend.SubmitPrompt
)

// cliProbeAdapter wraps a backend.Backend into an orchestrator.ProbePort.
type cliProbeAdapter struct {
	bk backend.Backend
}

func (a *cliProbeAdapter) Probe(window string) (orchestrator.ProbeResult, error) {
	status, err := probeWakeEndpoint(a.bk, window)
	if err != nil {
		return orchestrator.ProbeResult{}, err
	}
	return orchestrator.ProbeResult{
		PaneAlive:      status.PaneAlive,
		AgentAlive:     status.AgentAlive,
		ReadyForPrompt: status.ReadyForPrompt,
	}, nil
}

// cliSubmitAdapter wraps a backend.Backend into an orchestrator.SubmitPort.
type cliSubmitAdapter struct {
	bk backend.Backend
}

func (a *cliSubmitAdapter) Submit(window, prompt string) (bool, string, error) {
	result := submitWakePrompt(a.bk, window, prompt)
	return result.Acknowledged(), result.Detail, result.Err
}

type wakeDispatchHooks struct {
	orchestrator.WatcherHooks
}

func (h wakeDispatchHooks) Activate(homeDir string) {
	h.WatcherHooks.Activate(homeDir)
	if err := dispatchHerdrWake(homeDir); err != nil {
		fmt.Fprintf(os.Stderr, "wake dispatch: %v\n", err)
	}
}

func watcherHooks() orchestrator.WatcherHooks {
	return wakeDispatchHooks{WatcherHooks: orchestrator.NewCaptainWatcherHooks(newSessionUplinkTransport(), newSessionActivationTransport())}
}

// dispatchHerdrWake is a thin wrapper around orchestrator.DispatchWake.
// It resolves the mode, target, and backend from the environment and
// delegates the complete workflow to the orchestrator via adapter ports.
func dispatchHerdrWake(homeDir string) error {
	mode, err := resolveWakeMode(homeDir)
	if err != nil {
		return err
	}
	target, err := resolveWakeTarget(homeDir)
	if err != nil {
		return err
	}
	meta := map[string]string{"backend": "herdr", "window": target.Handle, "herdr_session": target.Session}
	bk, name, err := resolveWakeBackend(homeDir, meta)
	if err != nil || name != "herdr" {
		return err
	}

	req := orchestrator.DispatchWakeRequest{
		HomeDir: homeDir,
		Mode:    mode,
		Target:  target,
		Probe:   &cliProbeAdapter{bk: bk},
		Submit:  &cliSubmitAdapter{bk: bk},
	}

	result, err := orchestrator.DispatchWake(req)
	if err != nil {
		return err
	}
	switch result.Outcome {
	case orchestrator.WakeSubmitted:
		return nil
	case orchestrator.WakeDeferred:
		return fmt.Errorf("wake prompt deferred: %s", result.Detail)
	default:
		// Skipped — not an error; the orchestrator handles the outcome type.
		return nil
	}
}
