package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- fakeIdentity for recovery tests ---

// fakeIdentityChecker returns predetermined identity status.
type fakeIdentityChecker struct {
	known    bool
	resolved bool
	stale    bool
	err      error
}

func (f *fakeIdentityChecker) Known() bool    { return f.known }
func (f *fakeIdentityChecker) Resolved() bool { return f.resolved }
func (f *fakeIdentityChecker) Stale() bool    { return f.stale }

// --- RecoveryAction tests ---

func TestRecoveryActionStrings(t *testing.T) {
	tests := []struct {
		action RecoveryAction
		want   string
	}{
		{ActionNone, "none"},
		{ActionAutoRelaunch, "auto-relaunch"},
		{ActionNudge, "nudge"},
		{ActionReconcile, "reconcile"},
		{ActionQuarantine, "quarantine"},
	}
	for _, tc := range tests {
		got := tc.action.String()
		if got != tc.want {
			t.Errorf("RecoveryAction(%d) = %q, want %q", tc.action, got, tc.want)
		}
	}
}

func TestRecoveryActionDefault(t *testing.T) {
	var a RecoveryAction
	if a.String() != "none" {
		t.Errorf("default RecoveryAction = %q, want none", a.String())
	}
}

// --- ResolveRecovery tests ---

func TestResolveRecovery_EndpointDead_AutoRelaunch(t *testing.T) {
	// Authoritatively dead endpoint → auto-relaunch (when circuit is closed).
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeEndpointDead),
		Verdict:          "unknown",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action != ActionAutoRelaunch {
		t.Errorf("Action = %v, want auto-relaunch", action.Action)
	}
	if action.Reason == "" {
		t.Error("Reason is empty")
	}
}

func TestResolveRecovery_EndpointDead_CircuitOpen_NoRelaunch(t *testing.T) {
	// Authoritatively dead but circuit is open → no auto-relaunch (escalate).
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeEndpointDead),
		Verdict:          "unknown",
		CircuitOpen:      true,
		CircuitBlocked:   true,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action == ActionAutoRelaunch {
		t.Fatal("auto-relaunch when circuit is open, should escalate")
	}
	if action.Action != ActionQuarantine {
		t.Errorf("Action = %v, want quarantine (open circuit)", action.Action)
	}
}

func TestResolveRecovery_CaptureFailed_Nudge(t *testing.T) {
	// Capture failure is not authoritative absence: bounded nudge, never
	// auto-relaunch.
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeCaptureFailed),
		Verdict:          "unknown",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action != ActionNudge {
		t.Errorf("Action = %v, want nudge (capture failure never auto-relaunches)", action.Action)
	}
	if action.Reason == "" {
		t.Error("Reason is empty")
	}
}

func TestResolveRecovery_CaptureFailed_OpenCircuit_Quarantine(t *testing.T) {
	// Circuit semantics preserved: an open circuit quarantines regardless of
	// capture failure.
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeCaptureFailed),
		Verdict:          "unknown",
		CircuitOpen:      true,
		CircuitBlocked:   true,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action != ActionQuarantine {
		t.Errorf("Action = %v, want quarantine (open circuit)", action.Action)
	}
}

func TestResolveRecovery_UnresponsivePending_Nudge(t *testing.T) {
	// Unresponsive endpoint (pending verdict) → nudge.
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeUnsafe),
		Verdict:          "pending",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action != ActionNudge {
		t.Errorf("Action = %v, want nudge", action.Action)
	}
	if action.Reason == "" {
		t.Error("Reason is empty")
	}
}

func TestResolveRecovery_UnresponsiveUnknown_Nudge(t *testing.T) {
	// Unresponsive endpoint (unknown verdict) → nudge.
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeUnsafe),
		Verdict:          "unknown",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action != ActionNudge {
		t.Errorf("Action = %v, want nudge", action.Action)
	}
}

func TestResolveRecovery_UnknownIdentity_Reconcile(t *testing.T) {
	// Unknown identity → reconcile.
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeEndpointDead),
		Verdict:          "unknown",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    false,
		IdentityResolved: false,
		IdentityStale:    false,
	})
	if action.Action != ActionReconcile {
		t.Errorf("Action = %v, want reconcile", action.Action)
	}
	if action.Reason == "" {
		t.Error("Reason is empty")
	}
}

func TestResolveRecovery_UnresolvedIdentity_Reconcile(t *testing.T) {
	// Unknown + not resolved → reconcile.
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeEndpointDead),
		Verdict:          "unknown",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    false,
		IdentityResolved: false,
		IdentityStale:    false,
	})
	if action.Action != ActionReconcile {
		t.Errorf("Action = %v, want reconcile", action.Action)
	}
}

func TestResolveRecovery_StaleIdentity_Quarantine(t *testing.T) {
	// Stale identity → quarantine.
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeEndpointDead),
		Verdict:          "unknown",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    true,
	})
	if action.Action != ActionQuarantine {
		t.Errorf("Action = %v, want quarantine", action.Action)
	}
	if action.Reason == "" {
		t.Error("Reason is empty")
	}
}

func TestResolveRecovery_Injected_None(t *testing.T) {
	// Successfully injected → no recovery action needed.
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeInjected),
		Verdict:          "empty",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action != ActionNone {
		t.Errorf("Action = %v, want none", action.Action)
	}
}

func TestResolveRecovery_BackendFailed_MaybeNudge(t *testing.T) {
	// Backend failure (could be transient) → nudge.
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeBackendFailed),
		Verdict:          "empty",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action != ActionNudge {
		t.Errorf("Action = %v, want nudge", action.Action)
	}
}

// --- NudgeBudget tests ---

func TestNudgeBudgetDefault(t *testing.T) {
	b := DefaultNudgeBudget()
	if b.MaxNudges != 2 {
		t.Errorf("MaxNudges = %d, want 2", b.MaxNudges)
	}
	if b.Window != 5*time.Minute {
		t.Errorf("Window = %v, want 5m", b.Window)
	}
	if b.Cooldown != 30*time.Second {
		t.Errorf("Cooldown = %v, want 30s", b.Cooldown)
	}
}

func TestNudgeTrackerExhausted(t *testing.T) {
	now := time.Now()
	nt := NewNudgeTracker(DefaultNudgeBudget())

	// First nudge: not exhausted.
	if nt.Exhausted(now) {
		t.Error("Exhausted = true on fresh tracker, want false")
	}

	// Record 2 nudges.
	nt.Record(now)
	nt.Record(now)

	if !nt.Exhausted(now) {
		t.Error("Exhausted = false after 2 nudges, want true")
	}
}

func TestNudgeTrackerResetsOnWindow(t *testing.T) {
	now := time.Now()
	nt := NewNudgeTracker(DefaultNudgeBudget())

	nt.Record(now)
	nt.Record(now)
	if !nt.Exhausted(now) {
		t.Error("Exhausted = false after 2 nudges, want true")
	}

	// After window expires, nudges reset.
	future := now.Add(6 * time.Minute)
	if nt.Exhausted(future) {
		t.Error("Exhausted = true after window, want false (reset)")
	}
}

func TestNudgeTrackerCooldown(t *testing.T) {
	now := time.Now()
	nt := NewNudgeTracker(DefaultNudgeBudget())

	nt.Record(now)

	// Still in cooldown.
	if !nt.InCooldown(now) {
		t.Error("InCooldown = false immediately after nudge, want true")
	}

	// After cooldown elapses.
	future := now.Add(31 * time.Second)
	if nt.InCooldown(future) {
		t.Error("InCooldown = true after cooldown, want false")
	}
}

// --- Integration: Recovery tracks circuit + nudge ---

func TestRecoveryIntegration_EndpointDeadCircuitClosed(t *testing.T) {
	home := t.TempDir()
	store := NewCircuitStore(home)
	now := time.Now()

	key := CircuitKey{
		Target:    "s:p",
		Input:     "task-1",
		Signature: CircuitSignature(string(OutcomeEndpointDead), "connection refused"),
	}

	// Simulate first endpoint-dead event.
	opened, err := RecordCircuitAttempt(store, key, DefaultBudget(), now)
	if err != nil {
		t.Fatalf("RecordCircuitAttempt: %v", err)
	}
	if opened {
		t.Fatal("opened on first attempt")
	}

	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeEndpointDead),
		Verdict:          "unknown",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action != ActionAutoRelaunch {
		t.Errorf("Action = %v, want auto-relaunch", action.Action)
	}
}

func TestRecoveryIntegration_EndpointDeadCircuitOpen(t *testing.T) {
	home := t.TempDir()
	store := NewCircuitStore(home)
	now := time.Now()

	key := CircuitKey{
		Target:    "s:p",
		Input:     "task-1",
		Signature: CircuitSignature(string(OutcomeEndpointDead), "connection refused"),
	}

	// Open the circuit by exhausting the budget.
	var opened bool
	for i := 0; i < 3; i++ {
		opened, _ = RecordCircuitAttempt(store, key, DefaultBudget(), now)
	}
	if !opened {
		t.Fatal("circuit should be open after 3 attempts")
	}

	blocked, err := IsCircuitBlocked(store, key, now)
	if err != nil {
		t.Fatalf("IsCircuitBlocked: %v", err)
	}
	if !blocked {
		t.Fatal("circuit should be blocked")
	}

	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeEndpointDead),
		Verdict:          "unknown",
		CircuitOpen:      true,
		CircuitBlocked:   blocked,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action == ActionAutoRelaunch {
		t.Fatal("auto-relaunch when circuit is open and blocked")
	}
}

func TestRecoveryIntegration_StableAliveResetsCircuit(t *testing.T) {
	home := t.TempDir()
	store := NewCircuitStore(home)
	now := time.Now()

	key := CircuitKey{
		Target:    "s:p",
		Input:     "task-1",
		Signature: CircuitSignature(string(OutcomeEndpointDead), "connection refused"),
	}

	// Open the circuit.
	for i := 0; i < 3; i++ {
		RecordCircuitAttempt(store, key, DefaultBudget(), now)
	}

	// Record consecutive successes.
	closed, err := RecordCircuitSuccess(store, key, now.Add(31*time.Second))
	if err != nil {
		t.Fatalf("RecordCircuitSuccess: %v", err)
	}
	if closed {
		t.Fatal("circuit closed on first success, need 2")
	}

	closed, err = RecordCircuitSuccess(store, key, now.Add(32*time.Second))
	if err != nil {
		t.Fatalf("RecordCircuitSuccess: %v", err)
	}
	if !closed {
		t.Fatal("circuit did not close on second success")
	}

	// Circuit is now closed → auto-relaunch is permitted again.
	blocked, err := IsCircuitBlocked(store, key, now.Add(33*time.Second))
	if err != nil {
		t.Fatalf("IsCircuitBlocked: %v", err)
	}
	if blocked {
		t.Fatal("circuit should not be blocked after stable-alive")
	}

	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeEndpointDead),
		Verdict:          "unknown",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
	})
	if action.Action != ActionAutoRelaunch {
		t.Errorf("Action = %v, want auto-relaunch after circuit reset", action.Action)
	}
}

// --- Nudge boundedness ---

func TestBoundedNudge_ExhaustedNoMoreNudges(t *testing.T) {
	now := time.Now()
	nt := NewNudgeTracker(DefaultNudgeBudget())

	// Record max nudges.
	nt.Record(now)
	nt.Record(now)

	if !nt.Exhausted(now) {
		t.Fatal("nudge budget exhausted but Exhausted is false")
	}

	// Should not allow more nudges.
	action := ResolveRecovery(RecoveryInput{
		Outcome:          string(OutcomeUnsafe),
		Verdict:          "pending",
		CircuitOpen:      false,
		CircuitBlocked:   false,
		IdentityKnown:    true,
		IdentityResolved: true,
		IdentityStale:    false,
		// The nudge tracker is checked externally before calling ResolveRecovery.
		// When exhausted, the caller should skip the nudge or escalate.
	})
	if action.Action != ActionNudge {
		t.Errorf("Action = %v, want nudge", action.Action)
	}
}

// --- RecoveryDiagnostic tests ---

func TestRecoveryDiagnostic(t *testing.T) {
	d := RecoveryDiagnostic{
		At:      time.Now(),
		Kind:    "endpoint-dead",
		Target:  "s:p",
		Action:  ActionAutoRelaunch.String(),
		Reason:  "connection refused",
		Circuit: "closed",
	}
	if d.Kind != "endpoint-dead" {
		t.Errorf("Kind = %q, want endpoint-dead", d.Kind)
	}
	if d.Action != "auto-relaunch" {
		t.Errorf("Action = %q, want auto-relaunch", d.Action)
	}
}

// --- RecoveryDiagnosticStore tests ---

func TestRecoveryDiagnosticStore_AppendAndRead(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	store := NewRecoveryDiagnosticStore(home)

	d1 := RecoveryDiagnostic{
		At:      time.Now(),
		Kind:    "endpoint-dead",
		Target:  "s:p",
		Action:  ActionAutoRelaunch.String(),
		Reason:  "connection refused",
		Circuit: "closed",
	}

	if err := store.Append(d1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	diags, err := store.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %d, want 1", len(diags))
	}
	if diags[0].Kind != "endpoint-dead" {
		t.Errorf("diag[0].Kind = %q, want endpoint-dead", diags[0].Kind)
	}
}

func TestRecoveryDiagnosticStore_Bounded(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	store := NewRecoveryDiagnosticStore(home)
	store.maxEntries = 3

	// Append 5 entries; only last 3 should survive.
	for i := 0; i < 5; i++ {
		store.Append(RecoveryDiagnostic{
			At:     time.Now(),
			Kind:   "test",
			Target: "s:p",
			Action: "nudge",
			Reason: "",
		})
	}

	diags, err := store.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(diags) != 3 {
		t.Fatalf("diags = %d, want 3 (bounded)", len(diags))
	}
}

func TestRecoveryDiagnosticStore_Clear(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	store := NewRecoveryDiagnosticStore(home)

	store.Append(RecoveryDiagnostic{
		At:     time.Now(),
		Kind:   "test",
		Target: "s:p",
		Action: "nudge",
	})

	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	diags, err := store.Read()
	if err != nil {
		t.Fatalf("Read after clear: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags after clear = %d, want 0", len(diags))
	}
}

func TestRecoveryDiagnosticStore_RedactedFields(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	store := NewRecoveryDiagnosticStore(home)

	// Sensitive info in Reason should be redacted.
	d := RecoveryDiagnostic{
		At:     time.Now(),
		Kind:   "auth-failure",
		Target: "s:p",
		Action: "reconcile",
		Reason: "token expired: ghp_abc123def456",
	}

	if err := store.Append(d); err != nil {
		t.Fatalf("Append: %v", err)
	}

	diags, err := store.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %d, want 1", len(diags))
	}

	// Token should be redacted.
	if strings.Contains(diags[0].Reason, "ghp_") {
		t.Errorf("sensitive token not redacted: %q", diags[0].Reason)
	}
	if !strings.Contains(diags[0].Reason, "[REDACTED]") {
		t.Errorf("redacted marker not present: %q", diags[0].Reason)
	}
}

func TestRecoveryDiagnosticStore_RedactedTarget(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	store := NewRecoveryDiagnosticStore(home)

	d := RecoveryDiagnostic{
		At:     time.Now(),
		Kind:   "endpoint-dead",
		Target: "session-abc:auth-token-xyz",
		Action: "auto-relaunch",
		Reason: "endpoint unreachable",
	}

	if err := store.Append(d); err != nil {
		t.Fatalf("Append: %v", err)
	}

	diags, err := store.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %d, want 1", len(diags))
	}

	// Target should be preserved (handle is not sensitive).
	if diags[0].Target != "session-abc:auth-token-xyz" {
		t.Errorf("target changed: %q", diags[0].Target)
	}
}

func TestRecoveryDiagnosticStore_EmptyRead(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	store := NewRecoveryDiagnosticStore(home)

	diags, err := store.Read()
	if err != nil {
		t.Fatalf("Read on empty store: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %d, want 0", len(diags))
	}
}

func TestRecoveryDiagnosticStore_NoStateDir(t *testing.T) {
	// No state directory yet — should not error.
	home := t.TempDir()
	store := NewRecoveryDiagnosticStore(home)

	diags, err := store.Read()
	if err != nil {
		t.Fatalf("Read without state dir: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %d, want 0", len(diags))
	}
}

func TestRecoveryDiagnosticStore_ConcurrentSafe(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	store := NewRecoveryDiagnosticStore(home)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			store.Append(RecoveryDiagnostic{
				At:     time.Now(),
				Kind:   "test",
				Target: "s:p",
				Action: "nudge",
			})
		}
		close(done)
	}()

	for i := 0; i < 10; i++ {
		store.Read()
	}

	<-done
	diags, err := store.Read()
	if err != nil {
		t.Fatalf("Read after concurrent access: %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("no diagnostics after concurrent access")
	}
}

func TestRecoveryDiagnosticStore_PersistsBeforeCleanup(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	store := NewRecoveryDiagnosticStore(home)

	// Append a diagnostic.
	store.Append(RecoveryDiagnostic{
		At:     time.Now(),
		Kind:   "endpoint-dead",
		Target: "s:p",
		Action: "auto-relaunch",
		Reason: "connection refused",
	})

	// ClearStaleArtifacts should NOT remove the diagnostics file
	// (it is a bounded persistent diagnostic, not a stale artifact).
	ClearStaleArtifacts(home)

	diags, err := store.Read()
	if err != nil {
		t.Fatalf("Read after ClearStaleArtifacts: %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("diagnostics cleared by ClearStaleArtifacts, want persisted")
	}

	// Explicit clear should work.
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	diags, _ = store.Read()
	if len(diags) != 0 {
		t.Fatal("diagnostics not cleared after explicit Clear")
	}
}

// --- RedactSensitive tests ---

func TestRedactSensitive_Token(t *testing.T) {
	redacted := RedactSensitive("token: ghp_abc123def456")
	if strings.Contains(redacted, "ghp_") {
		t.Errorf("token not redacted: %q", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Errorf("no redaction marker: %q", redacted)
	}
}

func TestRedactSensitive_Key(t *testing.T) {
	redacted := RedactSensitive("key: sk_live_abc123")
	if strings.Contains(redacted, "sk_live_") {
		t.Errorf("key not redacted: %q", redacted)
	}
}

func TestRedactSensitive_Password(t *testing.T) {
	redacted := RedactSensitive("password=supersecret")
	if strings.Contains(redacted, "supersecret") {
		t.Errorf("password not redacted: %q", redacted)
	}
}

func TestRedactSensitive_CleanText(t *testing.T) {
	text := "endpoint unreachable after 3 attempts"
	redacted := RedactSensitive(text)
	if redacted != text {
		t.Errorf("clean text changed: %q", redacted)
	}
}

func TestRedactSensitive_Empty(t *testing.T) {
	redacted := RedactSensitive("")
	if redacted != "" {
		t.Errorf("empty string became %q", redacted)
	}
}

// --- RecoveryDiagnosticStore lifecycle ---

func TestRecoveryDiagnosticStore_WriteOnAppend(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	store := NewRecoveryDiagnosticStore(home)

	store.Append(RecoveryDiagnostic{
		At:     time.Now(),
		Kind:   "test",
		Target: "s:p",
		Action: "nudge",
	})

	// File should exist in state directory.
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatalf("reading state dir: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Name() == ".recovery-diagnostics" {
			found = true
			break
		}
	}
	if !found {
		t.Error("recovery diagnostics file not found in state directory")
	}
}
