package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Durable digest constants.
const (
	digestFile      = "state/.afk-digest"
	defaultWindow   = 60 * time.Second // default batch window before flushing routine wakes
	maxDeferDefault = 5 * time.Minute  // default max defer before alarm if digest stuck
)

// EscalationType classifies a batched wake entry for escalation routing.
type EscalationType int

const (
	EscalationRoutine     EscalationType = iota // Routine status update; batched
	EscalationDecision                          // Needs general decision
	EscalationFailure                           // Build/process failure
	EscalationCredential                        // Auth/credential issue
	EscalationReviewReady                       // PR ready for review
	EscalationWedge                             // Wedge condition alarm
)

var escalationNames = map[EscalationType]string{
	EscalationRoutine:     "routine",
	EscalationDecision:    "decision",
	EscalationFailure:     "failure",
	EscalationCredential:  "credential",
	EscalationReviewReady: "review-ready",
	EscalationWedge:       "wedge",
}

func (et EscalationType) String() string {
	if s, ok := escalationNames[et]; ok {
		return s
	}
	return "unknown"
}

// BatchedEntry is a single wake entry in the batched escalation digest.
type BatchedEntry struct {
	Kind    string         `json:"kind"`
	Key     string         `json:"key"`
	Payload string         `json:"payload"`
	Type    EscalationType `json:"type"`
	At      time.Time      `json:"at"`
}

// BatchedEscalation is the durable output of the digester, written to
// state/.afk-digest on flush. It accumulates all triage results over a window
// and flags each entry by escalation type.
type BatchedEscalation struct {
	Entries        []BatchedEntry `json:"entries"`
	RoutineCount   int            `json:"routine_count"`
	EscalatedCount int            `json:"escalated_count"`
	FirstAt        time.Time      `json:"first_at"`
	LastAt         time.Time      `json:"last_at"`
	WedgeAlarm     *WedgeAlarm    `json:"wedge_alarm,omitempty"`
	SafeTarget     *bool          `json:"safe_target,omitempty"`
	TargetVerdict  string         `json:"target_verdict,omitempty"`
}

// Digester accumulates triage Digests over a time window and flushes
// a BatchedEscalation to durable state when the window expires.
// Safe for concurrent use.
type Digester struct {
	mu             sync.Mutex
	entries        []BatchedEntry
	routineCount   int
	escalatedCount int
	firstAt        time.Time
	lastFlush      time.Time
	homeDir        string
	safeTarget     *bool
	targetVerdict  string
	window         time.Duration // batch window, defaults to defaultWindow
	maxDefer       time.Duration // max time before emitting defer alarm
}

// NewDigester creates a Digester scoped to the given home directory.
func NewDigester(homeDir string) *Digester {
	return &Digester{
		homeDir:  homeDir,
		window:   defaultWindow,
		maxDefer: maxDeferDefault,
	}
}

// SetWindow overrides the default batch window duration.
// Must be called before Start if a non-default window is desired.
func (d *Digester) SetWindow(window time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = window
}

// SetMaxDefer overrides the default max-defer threshold.
func (d *Digester) SetMaxDefer(maxDefer time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maxDefer = maxDefer
}

// FirstAt returns the timestamp of the first unflushed entry, or zero if empty.
func (d *Digester) FirstAt() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.firstAt
}

// EntryCount returns the total number of unflushed entries.
func (d *Digester) EntryCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.entries)
}

// MaxDeferDuration returns the max-defer threshold.
func (d *Digester) MaxDeferDuration() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.maxDefer
}

// SetTargetSafety records the general-pane safety verdict for inclusion
// in the next BatchedEscalation flush. Safe for concurrent use.
func (d *Digester) SetTargetSafety(safe bool, verdict string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := safe
	d.safeTarget = &s
	d.targetVerdict = verdict
}

// Feed adds triage results to the accumulator. A nil digest is a no-op.
func (d *Digester) Feed(digest *Digest) {
	if digest == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	for _, wd := range digest.Escalated {
		d.entries = append(d.entries, BatchedEntry{
			Kind:    wd.Kind,
			Key:     wd.Key,
			Payload: wd.Payload,
			Type:    classifyEscalationType(wd.Payload),
			At:      now,
		})
		d.escalatedCount++
	}

	for _, wd := range digest.Routines {
		d.entries = append(d.entries, BatchedEntry{
			Kind:    wd.Kind,
			Key:     wd.Key,
			Payload: wd.Payload,
			Type:    EscalationRoutine,
			At:      now,
		})
		d.routineCount++
	}

	if d.firstAt.IsZero() {
		d.firstAt = now
	}
}

// FeedWedge adds a wedge alarm entry to the accumulator.
func (d *Digester) FeedWedge(alarm *WedgeAlarm) {
	if alarm == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	d.entries = append(d.entries, BatchedEntry{
		Kind:    "wedge",
		Key:     "daemon",
		Payload: alarm.Reason,
		Type:    EscalationWedge,
		At:      alarm.DetectedAt,
	})
	d.escalatedCount++
}

// ShouldFlush reports whether the digest window has elapsed since the last flush
// and there are entries to flush. The window is measured from either the
// last flush time or the first entry time, whichever is more recent.
func (d *Digester) ShouldFlush(now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.entries) == 0 {
		return false
	}
	// Use first entry time as reference if never flushed; otherwise use last flush.
	reference := d.lastFlush
	if reference.IsZero() {
		reference = d.firstAt
	}
	return now.Sub(reference) >= d.window
}

// Flush writes the BatchedEscalation to state/.afk-digest and resets the
// accumulator. Returns nil if the accumulator is empty.
//
// Self-handle logic: when the target is safe (composer Empty) and ALL entries
// are routine (no escalations), the routines are self-handled and no digest
// is written — the General is never disturbed for routine status updates.
// When safe and there are some escalations, routine entries are still
// self-handled (filtered out) and only escalations are written.
func (d *Digester) Flush(now time.Time) error {
	d.mu.Lock()
	entries := d.entries
	routineCount := d.routineCount
	escalatedCount := d.escalatedCount
	firstAt := d.firstAt
	safeTarget := d.safeTarget
	targetVerdict := d.targetVerdict

	d.entries = nil
	d.routineCount = 0
	d.escalatedCount = 0
	d.firstAt = time.Time{}
	d.lastFlush = now
	d.safeTarget = nil
	d.targetVerdict = ""
	d.mu.Unlock()

	if len(entries) == 0 && safeTarget == nil {
		return nil
	}

	// Self-handle routine entries when the target is safe.
	// When safe and only routines, discard entirely (no General overhead).
	// When safe with some escalations, filter routines out.
	// Wedge alarms are NEVER self-handled — they always reach the General.
	// No-entries-with-safety verdict is an info-only digest, NOT self-handle.
	var selfHandledRoutines int
	if safeTarget != nil && *safeTarget && len(entries) > 0 {
		filtered := make([]BatchedEntry, 0, len(entries))
		for _, e := range entries {
			if e.Type != EscalationRoutine {
				filtered = append(filtered, e)
			}
		}
		selfHandledRoutines = len(entries) - len(filtered)
		entries = filtered
		routineCount = 0 // routines self-handled, not in digest
		escalatedCount = len(entries)

		// If nothing remains after self-handle, return early — no General needed.
		if len(entries) == 0 {
			fmt.Fprintf(os.Stderr, "afk: self-handled %d routine wake(s), target safe\n", selfHandledRoutines)
			return nil
		}
	}
	be := BatchedEscalation{
		Entries:        entries,
		RoutineCount:   routineCount,
		EscalatedCount: escalatedCount,
		FirstAt:        firstAt,
		LastAt:         now,
		SafeTarget:     safeTarget,
		TargetVerdict:  targetVerdict,
	}

	// If no entries but we have a safety verdict, write an info-only digest.
	// This lets the general see safety state even in quiet windows.

	data, err := json.MarshalIndent(be, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal batched escalation: %w", err)
	}

	path := filepath.Join(d.homeDir, digestFile)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create digest directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write digest file: %w", err)
	}

	return nil
}

// classifyEscalationType determines the escalation type from a payload string.
func classifyEscalationType(payload string) EscalationType {
	lower := strings.ToLower(payload)
	switch {
	case containsAny(lower, "needs-decision", "blocked", "which", "decision"):
		return EscalationDecision
	case containsAny(lower, "failed:", "failure", "broken", "error"):
		return EscalationFailure
	case containsAny(lower, "credential", "auth", "token", "login"):
		return EscalationCredential
	case containsAny(lower, "pr ready", "checks green", "ready in branch", "merged", "review"):
		return EscalationReviewReady
	default:
		return EscalationRoutine
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
