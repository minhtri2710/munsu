// Native observation event seam (BEO-17/P1b).
//
// The adapter owns ALL wire/protocol detail — socket path, JSON-RPC method,
// event names, version/capability gate, reader lifecycle — and returns a
// normalized ObservationSignal. A signal is a WAKE/ACTIVITY HINT only: it
// deliberately carries no lifecycle field, so a native event can never assert
// dead/alive/starting and can never set a Task phase. Only a structured
// re-probe of the exact binding (ObserveEndpoint) may produce a lifecycle
// observation; the orchestrator always re-probes before recovery/relaunch/
// dispose.
package backend

import (
	"context"
	"errors"
	"time"
)

// EventCursor is an opaque, monotonically increasing position in the native
// event stream. Only the adapter interprets it; the orchestrator stores and
// compares it for duplicate/out-of-order suppression. An empty cursor means
// "from the current position" (no durable resume across restarts in P1b).
type EventCursor string

// ObservationSignal is the normalized wake/activity hint produced by a native
// event source (BEO-17/P1b). It carries endpoint identity, the opaque
// incarnation the adapter can attest (empty when it cannot — P1a rule: an
// adapter never fabricates incarnation), the activity hint, source, timestamp
// and cursor. It has NO lifecycle axis: an event hint is never dead/alive and
// never a Task phase.
type ObservationSignal struct {
	Endpoint    EndpointRef
	Incarnation string            // opaque; empty when the adapter cannot attest it
	Activity    Activity          // busy/idle/blocked/unknown hint
	Source      ObservationSource // SourceEvent
	ObservedAt  time.Time
	Cursor      EventCursor
	Detail      string
}

// Valid reports whether every axis of the signal is usable.
func (s ObservationSignal) Valid() bool {
	return s.Endpoint.Handle != "" && s.Activity.Valid() && s.Source.Valid()
}

// ObservationEventSource is the adapter-owned native event seam (BEO-17/P1b).
// Wait blocks until a normalized signal is available for the endpoint, the
// context deadline (bounded wait) is reached, or the reader fails. `after` is
// the last consumed cursor; the adapter should skip events already consumed.
//
// Return contract:
//   - (ObservationSignal, nil): a normalized wake/activity hint arrived.
//   - (zero, context.DeadlineExceeded): bounded wait elapsed — normal, caller
//     proceeds with its polling cadence.
//   - (zero, ErrEventUnsupported): backend has no native event surface —
//     caller keeps polling.
//   - (zero, ErrEventUnavailable): event reader/transport unavailable — caller
//     falls back to polling this cycle.
//   - (zero, ErrEventProtocolMismatch): capability/version negotiation failed —
//     caller falls back to polling, fail closed.
//   - (zero, ErrEventReaderFailure): reader/transport failure mid-wait —
//     caller falls back to polling this cycle and treats the source degraded.
type ObservationEventSource interface {
	Wait(ctx context.Context, endpoint EndpointRef, after EventCursor) (ObservationSignal, error)
}

// Sentinel errors classify the polling-fallback reason. They never carry a
// lifecycle reading and never authorize recovery.
var (
	// ErrEventUnsupported means the backend declares no native event surface
	// (capability unavailable). The caller keeps polling.
	ErrEventUnsupported = errors.New("backend has no native observation event source")
	// ErrEventUnavailable means the event reader/transport is not available
	// right now (missing binary, server/socket down, reader not started).
	ErrEventUnavailable = errors.New("native observation event source unavailable")
	// ErrEventProtocolMismatch means capability/version negotiation failed or
	// the wire protocol is outside the supported range. The caller falls back
	// to polling and never trusts the event surface.
	ErrEventProtocolMismatch = errors.New("native observation event protocol/capability mismatch")
	// ErrEventReaderFailure means the reader/transport failed mid-wait. The
	// caller falls back to polling this cycle; a dead reader must never make
	// the watcher silent.
	ErrEventReaderFailure = errors.New("native observation event reader failure")
)

// NormalizeActivityHint maps a backend-specific wire status string to the
// normalized Activity hint. Unknown/malformed statuses map to ActivityUnknown
// (never idle, never busy): missing evidence is unknown, not a claim.
func NormalizeActivityHint(status string) Activity {
	switch status {
	case "busy", "working":
		return ActivityBusy
	case "idle", "done":
		return ActivityIdle
	case "blocked":
		return ActivityBlocked
	case "unknown":
		return ActivityUnknown
	default:
		return ActivityUnknown
	}
}
