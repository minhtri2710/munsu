package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

// --- Mock ports ---

type mockProbePort struct {
	result ProbeResult
	err    error
}

func (m *mockProbePort) Probe(_ string) (ProbeResult, error) {
	return m.result, m.err
}

type mockSubmitPort struct {
	acknowledged bool
	detail       string
	err          error
	prompt       string // captures the last prompt submitted
}

func (m *mockSubmitPort) Submit(_ string, prompt string) (bool, string, error) {
	m.prompt = prompt
	return m.acknowledged, m.detail, m.err
}

func setupMockRequest(t *testing.T, mode WakeDeliveryMode, ready bool) (DispatchWakeRequest, string) {
	t.Helper()
	home := testutil.TempHome(t)

	// Enqueue a wake so the queue has entries (except for empty-queue tests).
	if err := EnqueueWake(home, "signal", "task-1", "test payload"); err != nil {
		t.Fatalf("EnqueueWake: %v", err)
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
		Probe: &mockProbePort{
			result: ProbeResult{
				PaneAlive:      ready,
				AgentAlive:     ready,
				ReadyForPrompt: ready,
			},
		},
		Submit: &mockSubmitPort{
			acknowledged: true,
		},
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
		Probe:   &mockProbePort{result: ProbeResult{PaneAlive: true, AgentAlive: true, ReadyForPrompt: true}},
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
		Probe:   &mockProbePort{result: ProbeResult{PaneAlive: true, AgentAlive: true, ReadyForPrompt: true}},
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
		Probe:   &mockProbePort{result: ProbeResult{PaneAlive: true, AgentAlive: true, ReadyForPrompt: true}},
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

// --- Test: probe gates ---

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

func TestDispatchWake_UnreadyTargetSkipped(t *testing.T) {
	home := testutil.TempHome(t)
	if err := EnqueueWake(home, "signal", "task-1", "payload"); err != nil {
		t.Fatal(err)
	}

	req := DispatchWakeRequest{
		HomeDir: home,
		Mode:    WakeDeliveryHerdr,
		Target:  TargetResult{Source: RuntimeSource, Handle: "default:w1:p1", Session: "default"},
		Probe: &mockProbePort{
			result: ProbeResult{PaneAlive: true, AgentAlive: false, ReadyForPrompt: false},
		},
		Submit: &mockSubmitPort{acknowledged: true},
	}

	result, err := DispatchWake(req)
	if err != nil || result.Outcome != WakeSkipped {
		t.Fatalf("expected Skipped, got outcome=%q err=%v", result.Outcome, err)
	}
	if result.Reason != "target-unready" {
		t.Errorf("expected reason target-unready, got %q", result.Reason)
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
		Probe: &mockProbePort{
			result: ProbeResult{PaneAlive: true, AgentAlive: true, ReadyForPrompt: true},
		},
		Submit: &mockSubmitPort{acknowledged: true},
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
		Probe: &mockProbePort{
			result: ProbeResult{PaneAlive: true, AgentAlive: true, ReadyForPrompt: true},
		},
		Submit: submitPort,
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
		Probe: &mockProbePort{
			result: ProbeResult{PaneAlive: true, AgentAlive: true, ReadyForPrompt: true},
		},
		Submit: &mockSubmitPort{acknowledged: true},
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
	submitPort := &mockSubmitPort{acknowledged: false, detail: "backend busy"}
	req.Submit = submitPort

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("expected nil error for deferred, got: %v", err)
	}
	if result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got %q", result.Outcome)
	}
	if result.Detail == "" {
		t.Error("expected non-empty detail for deferred")
	}
}

func TestDispatchWake_SubmitBackendErrorDeferred(t *testing.T) {
	req, _ := setupMockRequest(t, WakeDeliveryHerdr, true)
	submitPort := &mockSubmitPort{acknowledged: false, detail: "backend failed", err: fmt.Errorf("internal error")}
	req.Submit = submitPort

	result, err := DispatchWake(req)
	if err != nil {
		t.Fatalf("expected nil error for deferred with backend error, got: %v", err)
	}
	if result.Outcome != WakeDeferred {
		t.Fatalf("expected Deferred, got %q", result.Outcome)
	}
	if !strings.Contains(result.Detail, "backend error") {
		t.Errorf("expected detail to mention backend error, got: %s", result.Detail)
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
		Probe: &mockProbePort{
			result: ProbeResult{PaneAlive: true, AgentAlive: true, ReadyForPrompt: true},
		},
		Submit: nil,
	}

	_, err := DispatchWake(req)
	if err == nil {
		t.Fatal("expected error for nil submit port")
	}
	if !strings.Contains(err.Error(), "submit port is nil") {
		t.Errorf("expected submit port error, got: %v", err)
	}
}
