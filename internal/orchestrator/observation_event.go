// Native observation event wait policy (BEO-17/P1b).
//
// The ORCHESTRATOR owns the bounded wait: timeout, duplicate suppression,
// stale cursor/incarnation rejection, and polling fallback. The backend
// adapter owns all wire/protocol detail and returns normalized signals. A
// validated signal is a WAKE HINT ONLY: it never carries lifecycle, never
// sets a Task phase, and the watcher always re-probes the exact binding
// (runCycleWithProbeAndSender) before any recovery/relaunch/dispose decision.
package orchestrator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
)

// EventWaitOutcome classifies one bounded native-event wait. Every outcome
// except EventWaitSignal means "fall back to polling".
type EventWaitOutcome uint8

const (
	// EventWaitSignal means a validated wake/activity hint arrived; the caller
	// re-probes the exact binding before acting.
	EventWaitSignal EventWaitOutcome = iota
	// EventWaitTimeout means the bounded wait elapsed without a signal — a
	// normal outcome; the caller proceeds with its polling cadence.
	EventWaitTimeout
	// EventWaitUnsupported means the backend declares no native event surface.
	EventWaitUnsupported
	// EventWaitUnavailable means the event reader/transport is unavailable.
	EventWaitUnavailable
	// EventWaitProtocolMismatch means capability/version negotiation failed.
	EventWaitProtocolMismatch
	// EventWaitReaderFailure means the reader failed mid-wait; the caller
	// polls this cycle and treats the source as degraded.
	EventWaitReaderFailure
)

func (o EventWaitOutcome) String() string {
	switch o {
	case EventWaitSignal:
		return "signal"
	case EventWaitTimeout:
		return "timeout"
	case EventWaitUnsupported:
		return "unsupported"
	case EventWaitUnavailable:
		return "unavailable"
	case EventWaitProtocolMismatch:
		return "protocol-mismatch"
	case EventWaitReaderFailure:
		return "reader-failure"
	default:
		return "invalid"
	}
}

// PollFallback reports whether the outcome requires falling back to polling.
func (o EventWaitOutcome) PollFallback() bool { return o != EventWaitSignal }

// ObservationEventSource is the orchestrator's narrow view of the backend
// native event seam: it waits for a normalized signal for one exact bound
// endpoint. Wire/protocol details live in the backend adapter.
type ObservationEventSource interface {
	Wait(ctx context.Context, endpoint backend.EndpointRef, after backend.EventCursor) (backend.ObservationSignal, error)
}

// EndpointSource couples one exact bound endpoint with its native event
// source and the expected opaque incarnation (for stale-incarnation
// rejection). An endpoint without a source is omitted by the port.
type EndpointSource struct {
	Endpoint    backend.EndpointRef
	Incarnation string
	Source      ObservationEventSource
}

// ObservationEventPort resolves the native event sources for the bound
// endpoints of a home. Implemented by the CLI over backend adapters; a port
// that finds no native event surface keeps the watcher on pure polling.
type ObservationEventPort interface {
	Sources(homeDir string) ([]EndpointSource, error)
}

// EventWaiter owns the orchestrator-side bounded wait state for one watcher
// run: per-endpoint last cursor (in-memory only — no durable event resume
// across process restarts in P1b), duplicate/out-of-order suppression, stale
// cursor/incarnation rejection, and outcome classification.
type EventWaiter struct {
	port ObservationEventPort

	mu      sync.Mutex
	cursors map[string]backend.EventCursor
}

// NewEventWaiter constructs the bounded event wait state over a port.
func NewEventWaiter(port ObservationEventPort) *EventWaiter {
	return &EventWaiter{port: port, cursors: map[string]backend.EventCursor{}}
}

func endpointKey(endpoint backend.EndpointRef) string {
	return endpoint.Backend + ":" + endpoint.Handle
}

// WaitForSignal performs ONE bounded wait across the home's bound endpoints
// that declare a native event source. It returns a normalized signal with
// EventWaitSignal on success, or the classified fallback outcome. A signal is
// accepted only when its endpoint identity matches the exact binding, its
// incarnation (when attested) matches the expected incarnation, and its cursor
// advances past the last consumed cursor (duplicate/out-of-order/stale
// rejection). Timeout is a normal outcome — the caller polls.
func (w *EventWaiter) WaitForSignal(ctx context.Context, homeDir string, timeout time.Duration) (backend.ObservationSignal, EventWaitOutcome) {
	if w == nil || w.port == nil {
		return backend.ObservationSignal{}, EventWaitUnsupported
	}
	sources, err := w.port.Sources(homeDir)
	if err != nil {
		return backend.ObservationSignal{}, EventWaitUnavailable
	}
	if len(sources) == 0 {
		// No bound endpoint declares a native event surface: pure polling.
		return backend.ObservationSignal{}, EventWaitUnsupported
	}

	deadline := time.Now().Add(timeout)
	for _, es := range sources {
		if es.Source == nil {
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return backend.ObservationSignal{}, EventWaitTimeout
		}
		waitCtx, cancel := context.WithDeadline(ctx, deadline)
		after := w.lastCursor(es.Endpoint)
		sig, werr := es.Source.Wait(waitCtx, es.Endpoint, after)
		cancel()

		if werr != nil {
			if errors.Is(werr, context.DeadlineExceeded) || errors.Is(werr, context.Canceled) {
				// Bounded wait elapsed for this source; try the next one with
				// the remaining budget before classifying as a timeout.
				continue
			}
			switch {
			case errors.Is(werr, backend.ErrEventUnsupported):
				return backend.ObservationSignal{}, EventWaitUnsupported
			case errors.Is(werr, backend.ErrEventUnavailable):
				return backend.ObservationSignal{}, EventWaitUnavailable
			case errors.Is(werr, backend.ErrEventProtocolMismatch):
				return backend.ObservationSignal{}, EventWaitProtocolMismatch
			default:
				return backend.ObservationSignal{}, EventWaitReaderFailure
			}
		}

		// Signal validation (orchestrator-owned): exact binding identity,
		// incarnation match, and cursor advancement.
		if sig.Endpoint.Handle != "" && sig.Endpoint.Handle != es.Endpoint.Handle {
			continue // wrong endpoint: ignore
		}
		if sig.Endpoint.Backend != "" && sig.Endpoint.Backend != es.Endpoint.Backend {
			continue
		}
		if es.Incarnation != "" && sig.Incarnation != "" && sig.Incarnation != es.Incarnation {
			continue // stale incarnation: reject
		}
		if !backend.CursorAfter(sig.Cursor, after) {
			continue // duplicate/out-of-order: suppress
		}
		w.recordCursor(es.Endpoint, sig.Cursor)
		return sig, EventWaitSignal
	}
	return backend.ObservationSignal{}, EventWaitTimeout
}

func (w *EventWaiter) lastCursor(endpoint backend.EndpointRef) backend.EventCursor {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cursors[endpointKey(endpoint)]
}

func (w *EventWaiter) recordCursor(endpoint backend.EndpointRef, cursor backend.EventCursor) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if cursor != "" {
		w.cursors[endpointKey(endpoint)] = cursor
	}
}

// eventPulse is the bridge from the event lane goroutine to the watcher loop.
type eventPulse struct {
	signal  backend.ObservationSignal
	outcome EventWaitOutcome
}

// eventWaitBudget is the bounded wait used by the event lane between poll
// cycles. It is deliberately short so the watcher never starves its signal
// handling or poll cadence; a timeout is normal and the ticker drives the
// next poll.
var eventWaitBudget = watcherPollInterval

// startEventLane launches the bounded native-event wait lane for a watcher
// run. It pushes only validated signals (EventWaitSignal) to the returned
// channel; every other outcome is absorbed internally and the lane keeps
// waiting, so the watcher's polling ticker remains the cadence authority and
// a dead/absent reader never silences the watcher. The lane stops when stopCh
// is closed (cancelling any in-flight bounded wait) or when the home has no
// native event surface.
func startEventLane(homeDir string, waiter *EventWaiter, stopCh <-chan struct{}) <-chan eventPulse {
	if waiter == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-stopCh:
		case <-stopWatch:
		}
		cancel()
	}()
	out := make(chan eventPulse, 1)
	go func() {
		defer close(out)
		defer close(stopWatch)
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			waitCtx, wcancel := context.WithTimeout(ctx, eventWaitBudget)
			sig, outcome := waiter.WaitForSignal(waitCtx, homeDir, eventWaitBudget)
			wcancel()
			if ctx.Err() != nil {
				// Lane stopped while waiting.
				return
			}
			switch outcome {
			case EventWaitSignal:
				select {
				case out <- eventPulse{signal: sig, outcome: outcome}:
				case <-stopCh:
					return
				}
			case EventWaitUnsupported, EventWaitProtocolMismatch:
				// No native event surface for this home/run: the lane has
				// nothing to wait on. Stop the lane; the watcher keeps pure
				// polling (never silent).
				return
			case EventWaitTimeout:
				// Bounded wait elapsed normally; the ticker drives the next
				// poll. Keep waiting for the next signal.
			case EventWaitUnavailable, EventWaitReaderFailure:
				// Reader hiccup: brief backoff, then retry the bounded wait;
				// the ticker continues polling meanwhile.
				select {
				case <-stopCh:
					return
				case <-time.After(time.Second):
				}
			}
		}
	}()
	return out
}
