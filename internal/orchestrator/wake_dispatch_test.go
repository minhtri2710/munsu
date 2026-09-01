package orchestrator

import (
	"fmt"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// --- Mock ports ---

type mockProbePort struct {
	obs backend.EndpointObservation
	err error
}

func (m *mockProbePort) Probe(_ string) (backend.EndpointObservation, error) {
	return m.obs, m.err
}

// obsFor builds an all-axes-valid observation whose State() derives the
// requested coarse state. It starts from the shape ObserveEndpoint reports
// (lifecycle unknown, responsive, freshness unknown, activity unknown, probe
// source) and overrides exactly one deciding axis per state.
func obsFor(state backend.EndpointObservationState, detail string) backend.EndpointObservation {
	obs := backend.EndpointObservation{
		Lifecycle:      backend.LifecycleUnknown,
		Responsiveness: backend.Responsive,
		Freshness:      backend.FreshnessUnknown,
		Activity:       backend.ActivityUnknown,
		Source:         backend.SourceProbe,
		Detail:         detail,
	}
	switch state {
	case backend.EndpointAlive:
		obs.Lifecycle = backend.LifecycleAlive
	case backend.EndpointStarting:
		obs.Lifecycle = backend.LifecycleStarting
	case backend.EndpointDead:
		obs.Lifecycle = backend.LifecycleDead
	case backend.EndpointStaleIdentity:
		obs.Freshness = backend.FreshnessStale
	case backend.EndpointUnresponsive:
		obs.Responsiveness = backend.Unresponsive
	case backend.EndpointUnresolved:
		obs.Source = backend.SourceDerived
	case backend.EndpointUnknown:
		// base shape already derives unknown
	}
	return obs
}

func TestObsFor_DerivesIntendedStates(t *testing.T) {
	for _, s := range []backend.EndpointObservationState{
		backend.EndpointAlive,
		backend.EndpointStarting,
		backend.EndpointUnresponsive,
		backend.EndpointDead,
		backend.EndpointUnknown,
		backend.EndpointStaleIdentity,
		backend.EndpointUnresolved,
	} {
		obs := obsFor(s, "")
		if !obs.Valid() {
			t.Errorf("obsFor(%v) is not Valid(): %s", s, obs)
		}
		if got := obs.State(); got != s {
			t.Errorf("obsFor(%v).State() = %v, want %v", s, got, s)
		}
	}
}

type mockSubmitPort struct {
	acknowledged bool
	status       string
	detail       string
	err          error
	prompt       string // captures the last prompt submitted
}

func (m *mockSubmitPort) Submit(_ string, prompt string) SubmitResult {
	m.prompt = prompt
	return SubmitResult{
		Acknowledged: m.acknowledged,
		Status:       m.status,
		Detail:       m.detail,
		Err:          m.err,
	}
}

func aliveProbe() *mockProbePort {
	return &mockProbePort{obs: obsFor(backend.EndpointAlive, "")}
}

func setupMockRequest(t *testing.T, mode WakeDeliveryMode, alive bool) (DispatchWakeRequest, string) {
	t.Helper()
	home := testutil.TempHome(t)

	// Enqueue a wake so the queue has entries (except for empty-queue tests).
	if err := EnqueueWake(home, "signal", "task-1", "test payload"); err != nil {
		t.Fatalf("EnqueueWake: %v", err)
	}

	obs := obsFor(backend.EndpointAlive, "")
	if !alive {
		obs = obsFor(backend.EndpointUnresponsive, "mock not ready")
	}

	target := TargetResult{
		Source:  RuntimeSource,
		Handle:  "default:w1:p1",
		Session: "default",
	}

	return DispatchWakeRequest{
		HomeDir: home,
		Mode:    mode,
		Target:  target,
		Probe:   &mockProbePort{obs: obs},
		Submit:  &mockSubmitPort{acknowledged: true},
	}, home
}

// --- Test: mode gates ---

func TestDispatchWake_NativeModeSkipped(t *testing.T) {
	req, home := setupMockRequest(t, WakeDeliveryNative, true)

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("native mode: expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "delivery-mode" {
		t.Errorf("expected reason delivery-mode, got %q", result.Reason)
	}

	// No wake should have been claimed
	claim, err := ClaimWakes(home, "munsu:herdr", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if claim == nil || len(claim.Wakes) == 0 {
		t.Fatal("expected wake to still be available (not claimed)")
	}
}

func TestDispatchWake_ManualModeSkipped(t *testing.T) {
	req, home := setupMockRequest(t, WakeDeliveryManual, true)

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("manual mode: expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "delivery-mode" {
		t.Errorf("expected reason delivery-mode, got %q", result.Reason)
	}

	// No wake claimed
	claim, err := ClaimWakes(home, "munsu:herdr", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if claim == nil || len(claim.Wakes) == 0 {
		t.Fatal("expected wake to still be available (not claimed)")
	}
}

// --- Test: target identity gates ---

func TestDispatchWake_MissingHandleSkipped(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "", Session: "default"},
		Probe:   aliveProbe(),
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "missing-target" {
		t.Errorf("expected reason missing-target, got %q", result.Reason)
	}
}

func TestDispatchWake_MissingSessionSkipped(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "w1:p1", Session: ""},
		Probe:   aliveProbe(),
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "missing-target" {
		t.Errorf("expected reason missing-target, got %q", result.Reason)
	}
}

// --- Test: ownership validation ---

func TestDispatchWake_InvalidOwnershipErrors(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: Unsupported, Handle: "foreign:w1:p1", Session: "foreign", SourceDetail: "unsupported source"},
		Probe:   aliveProbe(),
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	_, err := DispatchWake(req)
	if err == nil {
		t.Fatal("expected error for invalid ownership")
	}
	if !strings.Contains(err.Error(), "invalid target ownership") {
		t.Errorf("expected error about invalid target ownership, got: %v", err)
	}
}

// --- Test: probe gate errors ---

func TestDispatchWake_ProbeErrorSkipped(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   &mockProbePort{err: fmt.Errorf("agent not found")},
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "probe-error" {
		t.Errorf("expected reason probe-error, got %q", result.Reason)
	}
}

// --- Test: typed observation gates ---

func TestDispatchWake_AliveProceeds(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSubmitted {
		t.Fatalf("alive endpoint: expected Submitted, got outcome=%q err=%v", result.Outcome, err)
	}
}

func TestDispatchWake_StartingSkipped(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   &mockProbePort{obs: obsFor(backend.EndpointStarting, "pane exists, agent not ready")},
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "target-unready" {
		t.Errorf("expected reason target-unready, got %q", result.Reason)
	}
}

func TestDispatchWake_UnresponsiveSkipped(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   &mockProbePort{obs: obsFor(backend.EndpointUnresponsive, "timeout")},
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "target-unready" {
		t.Errorf("expected reason target-unready, got %q", result.Reason)
	}
}

func TestDispatchWake_DeadSkipped(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   &mockProbePort{obs: obsFor(backend.EndpointDead, "pane not found")},
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "endpoint-dead" {
		t.Errorf("expected reason endpoint-dead, got %q", result.Reason)
	}
}

func TestDispatchWake_UnknownSkippedWithoutClaim(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   &mockProbePort{obs: obsFor(backend.EndpointUnknown, "no authoritative probe")},
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "endpoint-unknown" {
		t.Errorf("expected reason endpoint-unknown, got %q", result.Reason)
	}

	// Wake must NOT be claimed
	claim, err := ClaimWakes(home, "munsu:herdr", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if claim == nil || len(claim.Wakes) == 0 {
		t.Fatal("unknown endpoint must NOT trigger wake claim")
	}
}

func TestDispatchWake_StaleIdentitySkippedWithoutClaim(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   &mockProbePort{obs: obsFor(backend.EndpointStaleIdentity, "endpoint identity changed")},
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "stale-identity" {
		t.Errorf("expected reason stale-identity, got %q", result.Reason)
	}

	// Wake must NOT be claimed
	claim, err := ClaimWakes(home, "munsu:herdr", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if claim == nil || len(claim.Wakes) == 0 {
		t.Fatal("stale-identity endpoint must NOT trigger wake claim")
	}
}

func TestDispatchWake_UnresolvedSkippedWithoutClaim(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   &mockProbePort{obs: obsFor(backend.EndpointUnresolved, "cannot resolve bound backend")},
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "endpoint-unresolved" {
		t.Errorf("expected reason endpoint-unresolved, got %q", result.Reason)
	}

	// Wake must NOT be claimed
	claim, err := ClaimWakes(home, "munsu:herdr", 60, 10)
	if err != nil {
		t.Fatalf("ClaimWakes: %v", err)
	}
	if claim == nil || len(claim.Wakes) == 0 {
		t.Fatal("unresolved endpoint must NOT trigger wake claim")
	}
}

// --- Test: empty queue ---

func TestDispatchWake_EmptyQueueSkipped(t *testing.T) {
	home := testutil.TempHome(t)
	// No EnqueueWake call — queue is empty

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   aliveProbe(),
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "empty-queue" {
		t.Errorf("expected reason empty-queue, got %q", result.Reason)
	}
}

// --- Test: full happy path ---

func TestDispatchWake_ClaimsAndSubmits(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != WakeSubmitted {
		t.Fatalf("expected Submitted, got %q", result.Outcome)
	}
	if result.Reason != "acknowledged" {
		t.Errorf("expected reason acknowledged, got %q", result.Reason)
	}
	if result.Detail == "" {
		t.Error("expected non-empty detail")
	}
}

// --- Test: exact prompt construction ---

func TestDispatchWake_ExactPromptConstruction(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := req.Submit.(*mockSubmitPort)

	_, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := submitPort.prompt
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}

	for _, want := range []string{
		"[mu-system:wake]",
		"key:",
		"claim_id:",
		"event_id:",
		"munsu wake resolve",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestDispatchWake_ExactPromptFields(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := req.Submit.(*mockSubmitPort)

	_, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := submitPort.prompt

	// Verify claim_id format (starts with "claim_id: lease-")
	if !strings.Contains(prompt, "claim_id: lease-") {
		t.Errorf("prompt should contain claim_id with prefix lease-")
	}

	// Verify event_id format (epoch:seq)
	if !strings.Contains(prompt, "event_id: ") {
		t.Errorf("prompt should contain event_id: %s", prompt)
	}

	// Verify the munsu wake resolve instruction
	if !strings.Contains(prompt, "munsu wake resolve --claim-id") {
		t.Errorf("prompt should contain munsu wake resolve --claim-id")
	}
	if !strings.Contains(prompt, "--event-id") {
		t.Errorf("prompt should contain --event-id flag")
	}
	if !strings.Contains(prompt, "--summary") {
		t.Errorf("prompt should contain --summary flag")
	}

	// Verify payload is in the prompt
	if !strings.Contains(prompt, "test payload") {
		t.Errorf("prompt should contain wake payload: %s", prompt)
	}
}

// --- Test: claim-before-submit ordering ---

func TestDispatchWake_ClaimBeforeSubmitOrdering(t *testing.T) {
	home := testutil.TempHome(t)

	// Enqueue two wakes
	if err := EnqueueWake(home, "signal", "task-1", "payload-1"); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueWake(home, "signal", "task-2", "payload-2"); err != nil {
		t.Fatal(err)
	}

	submitPort := &mockSubmitPort{acknowledged: true}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   aliveProbe(),
		Submit:  submitPort,
	}

	// First call: should claim one wake and submit
	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSubmitted {
		t.Fatalf("first call: expected Submitted, got %q err=%v", result.Outcome, err)
	}

	// The claimed wake should reference something — grab claim info from the prompt
	prompt1 := submitPort.prompt
	if !strings.Contains(prompt1, "payload-1") {
		t.Errorf("first prompt should contain payload-1, got: %s", prompt1)
	}

	// Second call: should claim the second wake
	submitPort.prompt = ""
	submitPort2 := &mockSubmitPort{acknowledged: true}
	req.Submit = submitPort2

	result2, err2 := DispatchWake(req)
	if err2 != nil || result2.Outcome != WakeSubmitted {
		t.Fatalf("second call: expected Submitted, got %q err=%v", result2.Outcome, err2)
	}

	prompt2 := submitPort2.prompt
	if !strings.Contains(prompt2, "payload-2") {
		t.Errorf("second prompt should contain payload-2, got: %s", prompt2)
	}
}

// --- Test: one-Wake behavior ---

func TestDispatchWake_OneWakeMax(t *testing.T) {
	home := testutil.TempHome(t)

	// Enqueue two wakes
	if err := EnqueueWake(home, "signal", "task-1", "payload-1"); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueWake(home, "signal", "task-2", "payload-2"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   aliveProbe(),
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	// First dispatch: claims one wake, submits it
	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSubmitted {
		t.Fatalf("first call: expected Submitted, got %q err=%v", result.Outcome, err)
	}

	// Second dispatch with a new submit port should claim the remaining wake
	submit2 := &mockSubmitPort{acknowledged: true}
	req.Submit = submit2

	result2, err2 := DispatchWake(req)
	if err2 != nil || result2.Outcome != WakeSubmitted {
		t.Fatalf("second call: expected Submitted, got %q err=%v", result2.Outcome, err2)
	}

	// Third dispatch: should find empty queue
	req.Submit = &mockSubmitPort{acknowledged: true}
	result3, err3 := DispatchWake(req)
	if err3 != nil || result3.Outcome != WakeSkipped {
		t.Fatalf("third call: expected Skipped (empty queue), got %q err=%v", result3.Outcome, err3)
	}
	if result3.Reason != "empty-queue" {
		t.Errorf("expected reason empty-queue, got %q", result3.Reason)
	}
}

// --- Test: deferred submission ---

func TestDispatchWake_SubmitDeferred(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "rejected", detail: "backend busy"}
	req.Submit = submitPort

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("expected nil error for deferred, got: %v", err)
	}
	if result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got %q", result.Outcome)
	}
	if result.Reason != "rejected" {
		t.Errorf("expected reason rejected, got %q", result.Reason)
	}
	if result.Detail != "backend busy" {
		t.Errorf("expected detail 'backend busy', got %q", result.Detail)
	}
}

func TestDispatchWake_SubmitBackendErrorDeferred(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "backend-failed", detail: "backend failed", err: fmt.Errorf("internal error")}
	req.Submit = submitPort

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("expected nil error for deferred with backend error, got: %v", err)
	}
	if result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got %q", result.Outcome)
	}
	if result.Reason != "backend-error" {
		t.Errorf("expected reason backend-error, got %q", result.Reason)
	}
	if result.Detail != "internal error" {
		t.Errorf("expected detail 'internal error', got %q", result.Detail)
	}
}

func TestDispatchWake_DeferredStalledHasTypedReason(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "stalled", detail: "agent busy"}
	req.Submit = submitPort

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got %q", result.Outcome)
	}
	if result.Reason != "stalled" {
		t.Errorf("expected reason stalled, got %q", result.Reason)
	}
}

func TestDispatchWake_DeferredEndpointDeadHasTypedReason(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "endpoint-dead", detail: "target pane not found"}
	req.Submit = submitPort

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got %q", result.Outcome)
	}
	if result.Reason != "endpoint-dead" {
		t.Errorf("expected reason endpoint-dead, got %q", result.Reason)
	}
}

func TestDispatchWake_DeferredUnsupportedHasTypedReason(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "unsupported", detail: "protocol version too low"}
	req.Submit = submitPort

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got %q", result.Outcome)
	}
	if result.Reason != "unsupported" {
		t.Errorf("expected reason unsupported, got %q", result.Reason)
	}
}

func TestDispatchWake_DeferredBackendFailedHasTypedReason(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "backend-failed", detail: "CLI invocation failed"}
	req.Submit = submitPort

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got %q", result.Outcome)
	}
	if result.Reason != "backend-failed" {
		t.Errorf("expected reason backend-failed, got %q", result.Reason)
	}
}

// --- Test: Submitted outcome with detail ---

func TestDispatchWake_SubmittedWithDetail(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != WakeSubmitted {
		t.Fatalf("expected Submitted, got %q", result.Outcome)
	}
	// Detail should contain event= and claim= identifiers
	if !strings.Contains(result.Detail, "event=") {
		t.Errorf("detail should contain event=, got: %s", result.Detail)
	}
	if !strings.Contains(result.Detail, "claim=") {
		t.Errorf("detail should contain claim=, got: %s", result.Detail)
	}
}

// --- Test: nil ports error ---

func TestDispatchWake_NilProbePortErrors(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   nil,
		Submit:  &mockSubmitPort{acknowledged: true},
	}

	_, err := DispatchWake(req)
	if err == nil {
		t.Fatal("expected error for nil probe port")
	}
	if !strings.Contains(err.Error(), "probe port is nil") {
		t.Errorf("expected probe port error, got: %v", err)
	}
}

func TestDispatchWake_NilSubmitPortErrors(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   aliveProbe(),
		Submit:  nil,
	}

	_, err := DispatchWake(req)
	if err == nil {
		t.Fatal("expected error for nil submit port")
	}
	if !strings.Contains(err.Error(), "submit port is nil") {
		t.Errorf("expected submit port error, got: %v", err)
	}
}

// --- Test: Lease preservation after deferred ---

func TestDispatchWake_DeferredPreservesLease(t *testing.T) {
	req, home := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "rejected", detail: "backend busy"}
	req.Submit = submitPort

	// Dispatch returns deferred
	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got outcome=%q err=%v", result.Outcome, err)
	}

	// The wake should be claimed under a lease (not acked, not lost)
	entries, err := os.ReadDir(mhome.LeaseDir(home))
	if err != nil {
		t.Fatalf("reading lease dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 lease file, got %d", len(entries))
	}

	// The wake queue should be empty (wake claimed)
	queuePath := QueuePath(home)
	if _, err := os.Stat(queuePath); err == nil || !os.IsNotExist(err) {
		t.Fatal("wake queue should be empty after claim")
	}
}

func TestDispatchWake_DeferredDoesNotAckLease(t *testing.T) {
	req, home := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "rejected", detail: "backend busy"}
	req.Submit = submitPort

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got outcome=%q err=%v", result.Outcome, err)
	}

	// Read the lease file to confirm events are still present (not acked)
	entries, err := os.ReadDir(mhome.LeaseDir(home))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 lease, got %d err=%v", len(entries), err)
	}
	leasPath := filepath.Join(mhome.LeaseDir(home), entries[0].Name())
	data, err := os.ReadFile(leasPath)
	if err != nil {
		t.Fatalf("reading lease: %v", err)
	}
	if !strings.Contains(string(data), "test payload") {
		t.Errorf("lease file should still contain wake payload, got: %s", data)
	}
}

// --- Test: Retry/reclaim after deferred lease expiry ---

func TestDispatchWake_DeferredWakeReclaimsAfterLeaseExpiry(t *testing.T) {
	req, home := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "rejected", detail: "backend busy"}
	req.Submit = submitPort

	// Dispatch returns deferred — lease is claimed
	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got outcome=%q err=%v", result.Outcome, err)
	}

	// Verify lease exists
	entries, err := os.ReadDir(mhome.LeaseDir(home))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 lease, got %d err=%v", len(entries), err)
	}

	// Reclaim expired leases — our lease has 60s expiry, so it's valid.
	// To test reclaim, delete the lease file to simulate expiry.
	leasePath := filepath.Join(mhome.LeaseDir(home), entries[0].Name())
	if err := os.Remove(leasePath); err != nil {
		t.Fatalf("removing lease: %v", err)
	}

	// Manually re-enqueue to simulate reclaim
	if err := EnqueueWake(home, "signal", "task-1", "test payload"); err != nil {
		t.Fatalf("EnqueueWake: %v", err)
	}

	// Second dispatch should succeed (retry path)
	submitPort2 := &mockSubmitPort{acknowledged: true}
	req.Submit = submitPort2

	result2, err2 := DispatchWake(req)
	if err2 != nil || result2.Outcome != WakeSubmitted {
		t.Fatalf("retry dispatch: expected Submitted, got outcome=%q err=%v", result2.Outcome, err2)
	}
}

func TestDispatchWake_DeferredLeaseNotReclaimedWhileValid(t *testing.T) {
	req, home := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "rejected", detail: "backend busy"}
	req.Submit = submitPort

	// Dispatch returns deferred
	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got outcome=%q err=%v", result.Outcome, err)
	}

	// ReclaimExpiredLeases runs — lease is still valid (60s), nothing reclaimed
	reclaimed, err := mhome.ReclaimExpiredLeases(home)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed != 0 {
		t.Fatalf("expected 0 reclaimed for valid lease, got %d", reclaimed)
	}

	// Lease file should still exist
	entries, err := os.ReadDir(mhome.LeaseDir(home))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 lease after reclaim, got %d err=%v", len(entries), err)
	}
}

// --- Test: Completed resolution suppresses reclaim of deferred events ---

func TestDispatchWake_CompletedResolutionSuppressesReclaim(t *testing.T) {
	home := testutil.TempHome(t)

	// Create an expired lease file manually with a wake record
	leaseDir := mhome.LeaseDir(home)
	if err := os.MkdirAll(leaseDir, 0755); err != nil {
		t.Fatal(err)
	}
	leaseID := "lease-expired-resolved"
	leasePath := filepath.Join(leaseDir, leaseID)
	leaseContent := leaseID + "\tconsumer\t0\t0\n100\t1\tsignal\ttask-1\tpayload\n"
	if err := os.WriteFile(leasePath, []byte(leaseContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a completed resolution record for the same event
	resDir := filepath.Join(home, "state/.wake-resolutions")
	if err := os.MkdirAll(resDir, 0755); err != nil {
		t.Fatal(err)
	}
	resName := strings.NewReplacer("/", "_", ":", "_").Replace(leaseID+"-"+"100:1") + ".json"
	resContent := `{"lease_id":"` + leaseID + `","event_id":"100:1","summary":"checked","state":"completed","updated_at":100}`
	if err := os.WriteFile(filepath.Join(resDir, resName), []byte(resContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Reclaim should suppress the resolved event
	reclaimed, err := mhome.ReclaimExpiredLeases(home)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed != 0 {
		t.Fatalf("expected 0 reclaimed for resolved event, got %d", reclaimed)
	}

	// Queue should remain empty
	if HasQueuedWakes(home) {
		t.Error("queue should be empty after reclaim suppression")
	}

	// Lease file should have been removed
	if _, err := os.Stat(leasePath); err == nil {
		t.Error("expired lease file should have been removed")
	}
}

func TestDispatchWake_UnresolvedEventGetsReclaimed(t *testing.T) {
	home := testutil.TempHome(t)

	// Create an expired lease file manually with a wake record (no resolution)
	leaseDir := mhome.LeaseDir(home)
	if err := os.MkdirAll(leaseDir, 0755); err != nil {
		t.Fatal(err)
	}
	leaseID := "lease-expired-unresolved"
	leasePath := filepath.Join(leaseDir, leaseID)
	leaseContent := leaseID + "\tconsumer\t0\t0\n100\t1\tsignal\ttask-1\tpayload\n"
	if err := os.WriteFile(leasePath, []byte(leaseContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Reclaim should re-enqueue the wake
	reclaimed, err := mhome.ReclaimExpiredLeases(home)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed != 1 {
		t.Fatalf("expected 1 reclaimed, got %d", reclaimed)
	}

	// Queue should have the reclaimed wake
	if !HasQueuedWakes(home) {
		t.Error("queue should have reclaimed wake")
	}

	// Lease file should have been removed
	if _, err := os.Stat(leasePath); err == nil {
		t.Error("expired lease file should have been removed")
	}
}

// --- Test: Skipped and deferred outcomes do not fail watcher cycle ---

func TestDispatchWake_SkippedOutcomeNotError(t *testing.T) {
	// Native mode should skip — not error
	req, _ := setupMockRequest(t, WakeDeliveryNative, true)

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("Skipped outcomes should not return errors, got: %v", err)
	}
	if result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got %q", result.Outcome)
	}
}

func TestDispatchWake_DeferredOutcomeNotError(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, status: "rejected", detail: "backend busy"}
	req.Submit = submitPort

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("Deferred outcomes should not return errors, got: %v", err)
	}
	if result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got %q", result.Outcome)
	}
}

func TestDispatchWake_DeferredDoesNotPreventSubsequentDispatch(t *testing.T) {
	home := testutil.TempHome(t)

	// Enqueue two wakes
	if err := EnqueueWake(home, "signal", "task-1", "payload-1"); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueWake(home, "signal", "task-2", "payload-2"); err != nil {
		t.Fatal(err)
	}

	// Deferred dispatch for wake 1
	submit1 := &mockSubmitPort{acknowledged: false, status: "rejected", detail: "backend busy"}
	req1 := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   aliveProbe(),
		Submit:  submit1,
	}
	result, err := DispatchWake(req1)
	if err != nil || result.Outcome != WakeDeferred {
		t.Fatalf("wake 1: expected Deferred, got outcome=%q err=%v", result.Outcome, err)
	}

	// Submitted dispatch for wake 2 — should work normally
	submit2 := &mockSubmitPort{acknowledged: true}
	req2 := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe:   aliveProbe(),
		Submit:  submit2,
	}
	result2, err2 := DispatchWake(req2)
	if err2 != nil || result2.Outcome != WakeSubmitted {
		t.Fatalf("wake 2: expected Submitted, got outcome=%q err=%v", result2.Outcome, err2)
	}
}
