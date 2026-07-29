package cli

import (
	"fmt"
	"os"
	"strings"

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

func dispatchHerdrWake(homeDir string) error {
	mode, err := resolveWakeMode(homeDir)
	if err != nil || mode != orchestrator.WakeDeliveryHerdr {
		return err
	}
	target, err := resolveWakeTarget(homeDir)
	if err != nil || target.Handle == "" || target.Session == "" {
		return err
	}
	if err := validateWakeTarget(&target); err != nil {
		return err
	}
	meta := map[string]string{"backend": "herdr", "window": target.Handle, "herdr_session": target.Session}
	bk, name, err := resolveWakeBackend(homeDir, meta)
	if err != nil || name != "herdr" {
		return err
	}
	status, err := probeWakeEndpoint(bk, target.Handle)
	if err != nil || !status.PaneAlive || !status.AgentAlive || !status.ReadyForPrompt {
		return err
	}
	claim, err := claimWake(homeDir, "munsu:herdr", 60, 1)
	if err != nil || len(claim.Wakes) == 0 {
		return err
	}
	wake := claim.Wakes[0]
	eventID := wake.Epoch + ":" + wake.Seq
	prompt := fmt.Sprintf("[mu-system:wake]\nkey: %s\nclaim_id: %s\nevent_id: %s\n\n%s\n\nReview this durable wake, then run:\nmunsu wake resolve --claim-id %q --event-id %q --summary %q", eventID, claim.LeaseID, eventID, wake.Payload, claim.LeaseID, eventID, "<non-empty summary>")
	result := submitWakePrompt(bk, target.Handle, prompt)
	if !result.Acknowledged() {
		return fmt.Errorf("wake prompt deferred: %s", strings.TrimSpace(result.Detail))
	}
	return nil
}
