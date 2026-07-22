package afk

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Backend is the minimal interface for sending keystrokes to a pane.
// session.Backend satisfies this interface.
type Backend interface {
	SendKeys(windowID, text string) error
}

const (
	// injectCooldown is the minimum time between re-injecting the same
	// escalation entry. Prevents duplicate flood.
	injectCooldown = 60 * time.Second
)

// InjectOutcome is a typed diagnostic for injection attempts.
type InjectOutcome string

const (
	OutcomeInjected      InjectOutcome = "injected"
	OutcomeUnsafe        InjectOutcome = "unsafe"
	OutcomeAFK           InjectOutcome = "afk"
	OutcomeEndpointDead  InjectOutcome = "endpoint-dead"
	OutcomeBackendFailed InjectOutcome = "backend-failed"
)

// InjectResult carries the outcome and diagnostics of an injection attempt.
type InjectResult struct {
	Outcome InjectOutcome `json:"outcome"`
	Verdict string        `json:"verdict,omitempty"`
	Target  string        `json:"target,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// InjectEvent records a single inject event for inspection and testing.
type InjectEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Target    string    `json:"target"`
	Verdict   string    `json:"verdict"`
	Entries   int       `json:"entries"`
}

// Injector handles sending escalation payloads to the resolved general pane.
// Every injection is gated by hard safety checks:
//
//  1. Consent flag (state/.afk) must exist
//  2. Target pane must be configured
//  3. IsSafeInjectTarget must return Empty immediately before SendKeys
//  4. Dedup: identical entries within cooldown are not re-injected
//  5. Every payload starts with the sentinel FM_INJECT_MARK (U+2063)
type Injector struct {
	backend      Backend
	capture      PaneCapture
	homeDir      string
	lastInjected map[string]time.Time // dedupKey → last inject time
	lastEvents   []InjectEvent
	mu           sync.Mutex
}

// NewInjector creates an Injector. All fields are required.
func NewInjector(backend Backend, capture PaneCapture, homeDir string) *Injector {
	return &Injector{
		backend:      backend,
		capture:      capture,
		homeDir:      homeDir,
		lastInjected: make(map[string]time.Time),
	}
}

// InjectIfSafe checks all safety gates and, if all pass, sends the
// sentinel-marked escalation payload to the resolved general pane.
// Returns true if injection occurred, false if deferred.
//
// Safety gates (all must pass; first failure short-circuits):
//  1. Consent flag (state/.afk) must exist
//  2. Resolved general pane handle must be non-empty
//  3. IsSafeInjectTarget must return (true, Empty) immediately before SendKeys
//  4. Duplicate entries within cooldown are skipped (best-effort dedup)
//  5. Sentinel prefix is prepended to the payload
func (inj *Injector) InjectIfSafe(escalation *BatchedEscalation) (bool, error) {
	// Gate 1: Consent flag.
	if !IsActive(inj.homeDir) {
		return false, nil
	}

	// Gate 2: Resolve and validate target ownership.
	target, err := ResolveTargetWithSource(inj.homeDir)
	if err != nil || ValidateTargetOwnership(&target) != nil {
		return false, nil
	}
	paneHandle := target.Handle

	// Gate 3: Re-check IsSafeInjectTarget immediately before SendKeys
	// (capture state may have changed since the last triage check).
	safe, verdict, err := IsSafeInjectTarget(inj.capture, paneHandle)
	if err != nil || !safe {
		return false, nil
	}

	// Nothing to inject.
	if len(escalation.Entries) == 0 && escalation.WedgeAlarm == nil {
		return false, nil
	}

	// Gate 4: Dedup — skip if ALL entries were injected within cooldown.
	if !inj.isAnyFresh(escalation) {
		return false, nil
	}

	// Gate 5: Build payload with sentinel prefix.
	payload := formatPayload(escalation)
	markedPayload := Mark(payload)

	// Send to general pane.
	if err := inj.backend.SendKeys(paneHandle, markedPayload); err != nil {
		return false, fmt.Errorf("send-keys to general pane %q: %w", paneHandle, err)
	}

	// Record inject event + update dedup timestamps.
	now := time.Now()
	inj.mu.Lock()
	for _, entry := range escalation.Entries {
		inj.lastInjected[dedupKey(entry)] = now
	}
	inj.lastEvents = append(inj.lastEvents, InjectEvent{
		Timestamp: now,
		Target:    paneHandle,
		Verdict:   verdict.String(),
		Entries:   len(escalation.Entries),
	})
	inj.mu.Unlock()

	return true, nil
}

// LastEvents returns a copy of recent inject events for inspection.
func (inj *Injector) LastEvents() []InjectEvent {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	events := make([]InjectEvent, len(inj.lastEvents))
	copy(events, inj.lastEvents)
	return events
}

// isAnyFresh returns true when at least one entry in the escalation has
// NOT been injected within cooldown. Returns true for empty entries
// (nothing to dedup against).
func (inj *Injector) isAnyFresh(escalation *BatchedEscalation) bool {
	if len(escalation.Entries) == 0 {
		return true
	}
	now := time.Now()
	inj.mu.Lock()
	defer inj.mu.Unlock()
	allRecent := true
	for _, entry := range escalation.Entries {
		key := dedupKey(entry)
		if lastAt, seen := inj.lastInjected[key]; !seen || now.Sub(lastAt) > injectCooldown {
			allRecent = false
			break
		}
	}
	return !allRecent
}

// dedupKey returns a unique key for a BatchedEntry for dedup tracking.
func dedupKey(e BatchedEntry) string {
	return e.Kind + "/" + e.Key + "/" + e.Payload
}

// formatPayload builds a concise one-line escalation payload for injection.
// The payload is prefixed with FM_INJECT_MARK by the caller (InjectIfSafe).
func formatPayload(be *BatchedEscalation) string {
	var parts []string

	// Wedge alarms come first.
	if be.WedgeAlarm != nil {
		parts = append(parts, "[wedge] "+be.WedgeAlarm.Reason)
	}

	// Non-routine escalated entries.
	for _, entry := range be.Entries {
		if entry.Type != EscalationRoutine {
			parts = append(parts, fmt.Sprintf("[%s] %s", entry.Type, entry.Payload))
		}
	}

	summary := fmt.Sprintf("[AFK] %d escalated", be.EscalatedCount)
	if be.RoutineCount > 0 {
		summary += fmt.Sprintf(" + %d routine", be.RoutineCount)
	}
	if len(parts) > 0 {
		summary += " | " + strings.Join(parts, "; ")
	}
	return summary
}

// DirectInject sends a sentinel-marked message directly to a terminal pane
// after verifying the composer is safe (empty). Used by report_cmd for
// immediate parent pane injection of material state changes.
//
// Unlike InjectIfSafe (which resolves the target internally from config),
// DirectInject takes an explicit parentTarget pane handle.
// Returns the typed InjectResult with outcome, verdict, and target diagnostics.
func DirectInject(backend Backend, capture PaneCapture, parentTarget, msg, eventID string) InjectResult {
	// Check composer safety.
	safe, verdict, err := IsSafeInjectTarget(capture, parentTarget)
	if err != nil {
		return InjectResult{
			Outcome: OutcomeEndpointDead,
			Verdict: verdict.String(),
			Target:  parentTarget,
			Error:   fmt.Sprintf("capture failed: %v", err),
		}
	}
	if !safe {
		return InjectResult{
			Outcome: OutcomeUnsafe,
			Verdict: verdict.String(),
			Target:  parentTarget,
		}
	}

	// Build payload with sentinel prefix and optional event ID.
	payload := Mark(msg)
	if eventID != "" {
		payload = fmt.Sprintf("%s [event=%s]", payload, eventID)
	}

	if err := backend.SendKeys(parentTarget, payload); err != nil {
		return InjectResult{
			Outcome: OutcomeBackendFailed,
			Verdict: verdict.String(),
			Target:  parentTarget,
			Error:   fmt.Sprintf("send-keys failed: %v", err),
		}
	}

	return InjectResult{
		Outcome: OutcomeInjected,
		Verdict: verdict.String(),
		Target:  parentTarget,
	}
}
