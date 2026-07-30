package cli

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

func saveWakeDispatcherSeams(t *testing.T) {
	t.Helper()
	mode, target, validate := resolveWakeMode, resolveWakeTarget, validateWakeTarget
	resolve, probe, claim, submit := resolveWakeBackend, probeWakeEndpoint, claimWake, submitWakePrompt
	t.Cleanup(func() {
		resolveWakeMode, resolveWakeTarget, validateWakeTarget = mode, target, validate
		resolveWakeBackend, probeWakeEndpoint, claimWake, submitWakePrompt = resolve, probe, claim, submit
	})
}

func setupWakeDispatcher(t *testing.T, mode orchestrator.WakeDeliveryMode, ready bool) (*int, *int, *[]string) {
	t.Helper()
	saveWakeDispatcherSeams(t)
	claims, submits := 0, 0
	prompts := []string{}
	resolveWakeMode = func(string) (orchestrator.WakeDeliveryMode, error) { return mode, nil }
	resolveWakeTarget = func(string) (orchestrator.TargetResult, error) {
		return orchestrator.TargetResult{Handle: "default:w1:p1", Session: "default"}, nil
	}
	validateWakeTarget = func(*orchestrator.TargetResult) error { return nil }
	resolveWakeBackend = func(string, map[string]string) (backend.Backend, string, error) {
		return &captainProbeBackend{}, "herdr", nil
	}
	probeWakeEndpoint = func(backend.Backend, string) (fleet.CaptainProbeResult, error) {
		return fleet.CaptainProbeResult{PaneAlive: true, AgentAlive: true, ReadyForPrompt: ready, AgentStatus: "done"}, nil
	}
	claimWake = func(string, string, int, int) (*orchestrator.ClaimResult, error) {
		claims++
		return &orchestrator.ClaimResult{LeaseID: "lease-1", Wakes: []orchestrator.ClaimedWakeRecord{{Epoch: "100", Seq: "1", Payload: "payload"}}}, nil
	}
	submitWakePrompt = func(_ backend.Backend, _ string, prompt string) backend.PromptResult {
		submits++
		prompts = append(prompts, prompt)
		return backend.PromptResult{Status: backend.PromptSubmitted}
	}
	return &claims, &submits, &prompts
}

func TestDispatchHerdrWakeModeAndReadinessGates(t *testing.T) {
	for _, mode := range []orchestrator.WakeDeliveryMode{orchestrator.WakeDeliveryNative, orchestrator.WakeDeliveryManual} {
		claims, submits, _ := setupWakeDispatcher(t, mode, true)
		if err := dispatchHerdrWake(t.TempDir()); err != nil || *claims != 0 || *submits != 0 {
			t.Fatalf("mode=%s claims=%d submits=%d err=%v", mode, *claims, *submits, err)
		}
	}
	claims, submits, _ := setupWakeDispatcher(t, orchestrator.WakeDeliveryHerdr, false)
	if err := dispatchHerdrWake(t.TempDir()); err != nil || *claims != 0 || *submits != 0 {
		t.Fatalf("not-ready claims=%d submits=%d err=%v", *claims, *submits, err)
	}
}

func TestDispatchHerdrWakeClaimsAndSubmitsExactPrompt(t *testing.T) {
	home := t.TempDir()
	if err := orchestrator.EnqueueWake(home, "signal", "task-1", "test payload"); err != nil {
		t.Fatal(err)
	}
	_, submits, prompts := setupWakeDispatcher(t, orchestrator.WakeDeliveryHerdr, true)
	if err := dispatchHerdrWake(home); err != nil {
		t.Fatal(err)
	}
	if *submits != 1 || len(*prompts) != 1 {
		t.Fatalf("submits=%d prompts=%d", *submits, len(*prompts))
	}
	for _, want := range []string{"[mu-system:wake]", "claim_id:", "event_id:", "munsu wake resolve"} {
		if !strings.Contains((*prompts)[0], want) {
			t.Fatalf("prompt missing %q: %s", want, (*prompts)[0])
		}
	}
}

func TestDispatchHerdrWakeSubmitFailurePreservesLease(t *testing.T) {
	home := t.TempDir()
	if err := orchestrator.EnqueueWake(home, "signal", "task", "payload"); err != nil {
		t.Fatal(err)
	}
	_, _, _ = setupWakeDispatcher(t, orchestrator.WakeDeliveryHerdr, true)
	claimWake = orchestrator.ClaimWakes
	submitWakePrompt = func(backend.Backend, string, string) backend.PromptResult {
		return backend.PromptResult{Status: backend.PromptBackendFailed, Detail: "backend failed", Err: errors.New("backend failed")}
	}
	if err := dispatchHerdrWake(home); err == nil {
		t.Fatal("submit failure unexpectedly succeeded")
	}
	entries, err := os.ReadDir(orchestrator.LeaseDir(home))
	if err != nil || len(entries) != 1 {
		t.Fatalf("lease was not preserved: entries=%v err=%v", entries, err)
	}
}

func TestDispatchHerdrWakeFailsClosedBeforeClaim(t *testing.T) {
	claims, _, _ := setupWakeDispatcher(t, orchestrator.WakeDeliveryHerdr, true)
	resolveWakeTarget = func(string) (orchestrator.TargetResult, error) { return orchestrator.TargetResult{}, nil }
	if err := dispatchHerdrWake(t.TempDir()); err != nil || *claims != 0 {
		t.Fatalf("missing target claims=%d err=%v", *claims, err)
	}
	// Invalid ownership: orchestrator.DispatchWake calls ValidateTargetOwnership
	// (not the package-level validateWakeTarget mock). This case is tested
	// in the Orchestrator-level tests; the CLI wrapper returns Skipped (nil error)
	// for non-herdr targets since resolveWakeBackend won't return "herdr".
}
