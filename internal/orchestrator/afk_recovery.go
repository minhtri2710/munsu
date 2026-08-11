package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// RecoveryAction is the recommended action for a recovery series.
type RecoveryAction int

const (
	// ActionNone means no recovery action is needed (endpoint is stable).
	ActionNone RecoveryAction = iota
	// ActionAutoRelaunch means the endpoint is authoritatively dead and
	// should be auto-relaunched. Only happens when the circuit is closed.
	ActionAutoRelaunch
	// ActionNudge means the endpoint is unresponsive (pending/unknown) and
	// should receive a bounded non-destructive nudge (inject attempt).
	ActionNudge
	// ActionReconcile means the endpoint identity is unknown or unresolved
	// and should be read-only reconciled before any recovery action.
	ActionReconcile
	// ActionQuarantine means the endpoint should be quarantined (identity
	// stale, or circuit open with repeated failures).
	ActionQuarantine
)

var actionNames = map[RecoveryAction]string{
	ActionNone:         "none",
	ActionAutoRelaunch: "auto-relaunch",
	ActionNudge:        "nudge",
	ActionReconcile:    "reconcile",
	ActionQuarantine:   "quarantine",
}

func (a RecoveryAction) String() string {
	if s, ok := actionNames[a]; ok {
		return s
	}
	return "none"
}

// RecoveryInput captures the endpoint and identity status used to resolve
// a recovery action. All fields are populated externally by the caller
// (the daemon triage cycle or the injector).
type RecoveryInput struct {
	Outcome   string `json:"outcome"`
	Verdict   string `json:"verdict"`
	Target    string `json:"target"`
	TaskKey   string `json:"task_key"`
	ErrorText string `json:"error_text,omitempty"`

	// Circuit state.
	CircuitOpen    bool `json:"circuit_open"`
	CircuitBlocked bool `json:"circuit_blocked"`

	// Identity state.
	IdentityKnown    bool `json:"identity_known"`
	IdentityResolved bool `json:"identity_resolved"`
	IdentityStale    bool `json:"identity_stale"`
}

// RecoveryDecision is the output of ResolveRecovery: the recommended action
// and a human-readable reason.
type RecoveryDecision struct {
	Action RecoveryAction `json:"action"`
	Reason string         `json:"reason"`
}

// ResolveRecovery resolves the correct recovery action for an endpoint given
// its outcome, identity status, and circuit state.
//
// Decision matrix:
//
//	Outcome            | Identity           | Circuit  | Action
//	-------------------|--------------------|----------|----------------
//	injected           | any                | any      | none (success)
//	any                | unknown/unresolved | any      | reconcile
//	any                | stale              | any      | quarantine
//	endpoint-dead      | known/resolved     | closed   | auto-relaunch
//	endpoint-dead      | known/resolved     | open     | quarantine
//	unsafe (pending)   | known/resolved     | closed   | nudge
//	unsafe (unknown)   | known/resolved     | closed   | nudge
//	backend-failed     | known/resolved     | closed   | nudge
//	capture-failed     | known/resolved     | closed   | nudge
//	unsafe/backend/capture-fail | known/resolved | open | escalate via quarantine
func ResolveRecovery(input RecoveryInput) RecoveryDecision {
	// 1. Success → no action.
	if input.Outcome == string(OutcomeInjected) {
		return RecoveryDecision{Action: ActionNone, Reason: "injection successful, endpoint stable"}
	}

	// 2. Identity unknown or unresolved → reconcile.
	if !input.IdentityKnown || !input.IdentityResolved {
		return RecoveryDecision{
			Action: ActionReconcile,
			Reason: fmt.Sprintf("identity unknown (known=%v, resolved=%v): reconcile before recovery",
				input.IdentityKnown, input.IdentityResolved),
		}
	}

	// 3. Identity stale → quarantine.
	if input.IdentityStale {
		return RecoveryDecision{
			Action: ActionQuarantine,
			Reason: "identity stale: quarantine to prevent duplicate launch",
		}
	}

	// 4. Circuit open → quarantine (suppress auto-relaunch).
	if input.CircuitOpen {
		return RecoveryDecision{
			Action: ActionQuarantine,
			Reason: fmt.Sprintf("circuit open: repeated identical failure (%s) — quarantine until resolved",
				input.Outcome),
		}
	}

	// 5. Authoritatively dead endpoint → auto-relaunch.
	if input.Outcome == string(OutcomeEndpointDead) {
		return RecoveryDecision{
			Action: ActionAutoRelaunch,
			Reason: fmt.Sprintf("endpoint authoritatively dead (%s): auto-relaunch", input.ErrorText),
		}
	}

	// 6. Unresponsive (unsafe/pending/unknown) or unverifiable (capture
	// failed) → bounded nudge. Never auto-relaunch: only authoritative
	// endpoint absence may authorize relaunch.
	if input.Outcome == string(OutcomeUnsafe) || input.Outcome == string(OutcomeBackendFailed) || input.Outcome == string(OutcomeCaptureFailed) {
		return RecoveryDecision{
			Action: ActionNudge,
			Reason: fmt.Sprintf("endpoint unresponsive (outcome=%s, verdict=%s): bounded nudge",
				input.Outcome, input.Verdict),
		}
	}

	// 7. Unknown outcome → none (safe default).
	return RecoveryDecision{Action: ActionNone, Reason: "unknown outcome, no recovery action"}
}

// --- Nudge budget ---

// NudgeBudget bounds how many non-destructive nudges are allowed per window.
type NudgeBudget struct {
	MaxNudges int           `json:"max_nudges"`
	Window    time.Duration `json:"window"`
	Cooldown  time.Duration `json:"cooldown"`
}

// DefaultNudgeBudget returns the default nudge budget: 2 nudges per 5-minute
// window, 30-second cooldown between nudges.
func DefaultNudgeBudget() NudgeBudget {
	return NudgeBudget{
		MaxNudges: 2,
		Window:    5 * time.Minute,
		Cooldown:  30 * time.Second,
	}
}

// NudgeTracker tracks bounded nudge attempts for an endpoint.
type NudgeTracker struct {
	mu     sync.Mutex
	budget NudgeBudget
	nudges []time.Time
	lastAt time.Time
}

// NewNudgeTracker creates a nudge tracker with the given budget.
func NewNudgeTracker(budget NudgeBudget) *NudgeTracker {
	return &NudgeTracker{
		budget: budget,
	}
}

// Record records one nudge attempt at time now.
func (nt *NudgeTracker) Record(now time.Time) {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	nt.prune(now)
	nt.nudges = append(nt.nudges, now)
	nt.lastAt = now
}

// Exhausted reports whether the nudge budget is exhausted (max nudges reached
// within the window). The caller should skip nudging and escalate instead.
func (nt *NudgeTracker) Exhausted(now time.Time) bool {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	nt.prune(now)
	return len(nt.nudges) >= nt.budget.MaxNudges
}

// InCooldown reports whether the cooldown is still active since the last nudge.
func (nt *NudgeTracker) InCooldown(now time.Time) bool {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	if nt.lastAt.IsZero() {
		return false
	}
	return now.Sub(nt.lastAt) < nt.budget.Cooldown
}

// prune removes nudges outside the budget window.
func (nt *NudgeTracker) prune(now time.Time) {
	cutoff := now.Add(-nt.budget.Window)
	j := 0
	for _, n := range nt.nudges {
		if n.After(cutoff) {
			nt.nudges[j] = n
			j++
		}
	}
	nt.nudges = nt.nudges[:j]
	if len(nt.nudges) == 0 {
		nt.lastAt = time.Time{}
	}
}

// --- Redacted diagnostics ---

const recoveryDiagnosticsFile = "state/.recovery-diagnostics"

// RecoveryDiagnostic is a single bounded redacted diagnostic entry.
// Sensitive fields (tokens, keys, credentials) are redacted before storage.
type RecoveryDiagnostic struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Target  string    `json:"target"`
	Action  string    `json:"action"`
	Reason  string    `json:"reason,omitempty"`
	Circuit string    `json:"circuit,omitempty"`
}

// RecoveryDiagnosticStore persists bounded redacted diagnostics that survive
// across sessions. The last N entries are kept; older entries are pruned on
// append. Diagnostics are NOT removed by ClearStaleArtifacts — they persist
// until explicitly cleared by Clear or by the caller after review.
type RecoveryDiagnosticStore struct {
	homeDir    string
	mu         sync.Mutex
	maxEntries int
}

// NewRecoveryDiagnosticStore creates a diagnostic store for the given home.
func NewRecoveryDiagnosticStore(homeDir string) *RecoveryDiagnosticStore {
	return &RecoveryDiagnosticStore{
		homeDir:    homeDir,
		maxEntries: 20,
	}
}

// Append adds a redacted diagnostic to the store. Older entries beyond the
// max are pruned. Sensitive fields in the Reason are auto-redacted.
func (s *RecoveryDiagnosticStore) Append(d RecoveryDiagnostic) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Redact sensitive content.
	d.Reason = RedactSensitive(d.Reason)

	// Read existing entries.
	diags, err := s.readLocked()
	if err != nil {
		return err
	}

	diags = append(diags, d)

	// Prune to max entries.
	if len(diags) > s.maxEntries {
		diags = diags[len(diags)-s.maxEntries:]
	}

	return s.writeLocked(diags)
}

// Read returns all stored diagnostics. Returns empty slice, nil error when
// no diagnostics exist or the file doesn't exist yet.
func (s *RecoveryDiagnosticStore) Read() ([]RecoveryDiagnostic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

// Clear removes all stored diagnostics.
func (s *RecoveryDiagnosticStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(nil)
}

// readLocked reads diagnostics from disk. Must be called with s.mu held.
func (s *RecoveryDiagnosticStore) readLocked() ([]RecoveryDiagnostic, error) {
	path := filepath.Join(s.homeDir, recoveryDiagnosticsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading recovery diagnostics: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var diags []RecoveryDiagnostic
	if err := json.Unmarshal(data, &diags); err != nil {
		return nil, fmt.Errorf("unmarshal recovery diagnostics: %w", err)
	}
	return diags, nil
}

// writeLocked writes diagnostics to disk. Must be called with s.mu held.
func (s *RecoveryDiagnosticStore) writeLocked(diags []RecoveryDiagnostic) error {
	path := filepath.Join(s.homeDir, recoveryDiagnosticsFile)
	if len(diags) == 0 {
		// Remove the file when empty.
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing recovery diagnostics: %w", err)
		}
		return nil
	}
	data, err := json.MarshalIndent(diags, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recovery diagnostics: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating diagnostics directory: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// -- Sensitive data redaction --

// sensitivePatterns matches common sensitive token/key/credential patterns
// for redaction. Each pattern must match the full sensitive value.
var sensitivePatterns = []*regexp.Regexp{
	// GitHub tokens: ghp_, gho_, ghu_, ghs_, ghm_, gh_
	regexp.MustCompile(`(?i)(?:ghp|gho|ghu|ghs|ghm|gh)_[a-zA-Z0-9]{4,}`),
	// Stripe keys: sk_live_, rk_live_, pk_live_
	regexp.MustCompile(`(?i)(?:sk|rk|pk)_(?:live|test)_[a-zA-Z0-9]+`),
	// AWS access keys: AKIA...
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	// Generic API keys/tokens/passwords following common patterns.
	regexp.MustCompile(`(?i)(?:token|secret|password|passwd|credential|key|api_key|apikey)[=:]\s*\S{4,}`),
	// Bearer tokens.
	regexp.MustCompile(`(?i)bearer\s+\S{10,}`),
	// Basic auth credentials.
	regexp.MustCompile(`(?i)basic\s+[a-zA-Z0-9+/=]{10,}`),
	// SSH private keys.
	regexp.MustCompile(`-----BEGIN\s+(?:RSA|DSA|EC|OPENSSH)\s+PRIVATE\s+KEY-----`),
}

// RedactSensitive replaces sensitive data in s with "[REDACTED]".
func RedactSensitive(s string) string {
	result := s
	for _, pat := range sensitivePatterns {
		result = pat.ReplaceAllString(result, "[REDACTED]")
	}
	return result
}
