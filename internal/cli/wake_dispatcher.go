package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// probeAdapter wraps a backend.Backend into an orchestrator.ProbePort
// using the typed backend.ObserveBackendEndpoint function.
type probeAdapter struct {
	bk backend.Backend
}

func (a *probeAdapter) Probe(window string) (backend.EndpointObservation, error) {
	return backend.ObserveEndpoint(a.bk, window), nil
}

// submitAdapter wraps a backend.Backend into an orchestrator.SubmitPort.
type submitAdapter struct {
	bk backend.Backend
}

func (a *submitAdapter) Submit(window, prompt string) orchestrator.SubmitResult {
	result := backend.SubmitPrompt(a.bk, window, prompt)
	return orchestrator.SubmitResult{
		Acknowledged: result.Acknowledged(),
		Status:       string(result.Status),
		Detail:       result.Detail,
		Err:          result.Err,
	}
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

// dispatchHerdrWake composes typed adapters, invokes the Orchestrator
// transaction, and renders typed outcomes. It does NOT re-select a bound
// backend or duplicate target, readiness, claim, prompt, submission, or
// retry policy — those are owned by the Orchestrator.
func dispatchHerdrWake(homeDir string) error {
	mode, err := orchestrator.ResolveWakeDeliveryMode(homeDir)
	if err != nil {
		return err
	}
	target, err := orchestrator.ResolveTargetWithSource(homeDir)
	if err != nil {
		return err
	}
	meta := map[string]string{"backend": "herdr", "window": target.Handle, "herdr_session": target.Session}
	bk, name, err := backend.BackendForTask(homeDir, meta)
	if err != nil || name != "herdr" {
		return err
	}

	req := orchestrator.DispatchWakeRequest{
		HomeDir: homeDir,
		Mode:    mode,
		Target:  target,
		Probe:   &probeAdapter{bk: bk},
		Submit:  &submitAdapter{bk: bk},
	}

	result, err := orchestrator.DispatchWake(req)
	if err != nil {
		return err
	}
	switch result.Outcome {
	case orchestrator.WakeSubmitted:
		return nil
	case orchestrator.WakeDeferred:
		fmt.Fprintf(os.Stderr, "wake prompt deferred: reason=%s detail=%s\n", result.Reason, result.Detail)
		return nil
	default:
		// Skipped — not an error; the orchestrator handles the outcome type.
		return nil
	}
}
