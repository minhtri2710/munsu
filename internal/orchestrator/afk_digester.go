package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// Durable digest constants.
const (
	digestFile    = "state/.afk-digest"
	defaultWindow = 60 * time.Second // default batch window before flushing routine wakes
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
	Ref     string         `json:"ref,omitempty"`
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
	window         time.Duration // batch window, defaults to defaultWindow
}

// NewDigester creates a Digester scoped to the given home directory.
func NewDigester(homeDir string) *Digester {
	return &Digester{
		homeDir: homeDir,
		window:  defaultWindow,
	}
}

// SetWindow overrides the default batch window duration.
// Must be called before Start if a non-default window is desired.
func (d *Digester) SetWindow(window time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = window
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
			Ref:     wd.Ref,
			Type:    classifyEscalationType(wd.Key, wd.Payload),
			At:      now,
		})
		d.escalatedCount++
	}

	for _, wd := range digest.Routines {
		d.entries = append(d.entries, BatchedEntry{
			Kind:    wd.Kind,
			Key:     wd.Key,
			Payload: wd.Payload,
			Ref:     wd.Ref,
			Type:    EscalationRoutine,
			At:      now,
		})
		d.routineCount++
	}

	if d.firstAt.IsZero() {
		d.firstAt = now
	}
}

// FeedWedge adds a wedge entry to the accumulator.
func (d *Digester) FeedWedge(reason string, at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.entries = append(d.entries, BatchedEntry{
		Kind:    "wedge",
		Key:     "daemon",
		Payload: reason,
		Type:    EscalationWedge,
		At:      at,
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
// accumulator only after the durable write succeeds. Returns nil if the
// accumulator is empty.
func (d *Digester) Flush(now time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.entries) == 0 {
		return nil
	}

	be := BatchedEscalation{
		Entries:        append([]BatchedEntry(nil), d.entries...),
		RoutineCount:   d.routineCount,
		EscalatedCount: d.escalatedCount,
		FirstAt:        d.firstAt,
		LastAt:         now,
	}

	path := filepath.Join(d.homeDir, digestFile)
	if existing, err := readDigestFile(path); err != nil {
		return fmt.Errorf("read existing digest: %w", err)
	} else if existing != nil {
		be.Entries = append(existing.Entries, be.Entries...)
		be.RoutineCount += existing.RoutineCount
		be.EscalatedCount += existing.EscalatedCount
		if be.FirstAt.IsZero() || (!existing.FirstAt.IsZero() && existing.FirstAt.Before(be.FirstAt)) {
			be.FirstAt = existing.FirstAt
		}
	}

	data, err := json.MarshalIndent(be, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal batched escalation: %w", err)
	}
	if err := writeDigestFile(path, data); err != nil {
		return err
	}

	d.entries = nil
	d.routineCount = 0
	d.escalatedCount = 0
	d.firstAt = time.Time{}
	d.lastFlush = now
	return nil
}

func readDigestFile(path string) (*BatchedEscalation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var be BatchedEscalation
	if err := json.Unmarshal(data, &be); err != nil {
		return nil, fmt.Errorf("unmarshal digest file: %w", err)
	}
	return &be, nil
}

func writeDigestFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create digest directory: %w", err)
	}
	if err := home.AtomicWrite(path, data, 0644); err != nil {
		return fmt.Errorf("install digest file: %w", err)
	}
	return nil
}

// classifyEscalationType determines the escalation type from a payload: a
// recognised status verb of a status-line payload, or of a report payload
// after its "<key>: " prefix, alone decides; prose keywords classify only a
// payload with no recognised verb.
func classifyEscalationType(key, payload string) EscalationType {
	if verb := domain.LineVerb(strings.TrimPrefix(payload, key+": ")); domain.IsValidStatusState(verb) {
		switch verb {
		case "needs-decision", "blocked":
			return EscalationDecision
		case "failed":
			return EscalationFailure
		case "done", "review-ready", "delivered":
			return EscalationReviewReady
		default:
			return EscalationRoutine
		}
	}
	lower := strings.ToLower(payload)
	switch {
	case containsAny(lower, "needs-decision", "blocked", "which", "decision"):
		return EscalationDecision
	case containsAny(lower, "failure", "broken", "error"):
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
