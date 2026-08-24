// Herdr native observation event source (BEO-17/P1b).
//
// This adapter owns ALL herdr wire/protocol detail: the CLI invocation for
// agent status wait, the version/capability negotiation gate, and response
// parsing. It returns a normalized ObservationSignal (activity hint + cursor)
// and never fabricates lifecycle: a native event can never assert dead/alive.
//
// Capability/version gate: the herdr agent-wait surface is only used when the
// negotiated capability proves the surface (READY + CapAgentWait). Until
// capability negotiation and real-binary evidence qualify it, the native
// event lane stays proposed/experimental: any gate failure falls back to
// polling with a typed error (see observation_event.go return contract).
package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HerdrEventSource implements ObservationEventSource over the herdr CLI's
// agent status-wait surface. The CLI path is injectable for contract tests
// ("" resolves from PATH). The capability gate is negotiated lazily and
// cached for the process lifetime.
type HerdrEventSource struct {
	// Session is the herdr session name used for every CLI call.
	Session string
	// CLIPath overrides the herdr binary location; "" resolves from PATH.
	CLIPath string

	once sync.Once
	info CapabilityInfo
}

// capability negotiates the herdr capability/version once and caches it.
func (s *HerdrEventSource) capability() CapabilityInfo {
	s.once.Do(func() { s.info = ProbeHerdrCapability(s.CLIPath) })
	return s.info
}

// negotiate classifies the negotiated capability into the typed event-seam
// fallback contract. READY + CapAgentWait is the only usable state.
func (s *HerdrEventSource) negotiate() error {
	return negotiateHerdrCapability(s.capability())
}

func negotiateHerdrCapability(info CapabilityInfo) error {
	switch info.State {
	case HerdrReady:
		if !info.Flags.Has(CapAgentWait) {
			return fmt.Errorf("%w: herdr protocol %d lacks agent-wait event surface", ErrEventUnsupported, info.Protocol)
		}
		return nil
	case HerdrAbsent:
		return fmt.Errorf("%w: herdr binary not found on PATH", ErrEventUnavailable)
	case HerdrFailed:
		return fmt.Errorf("%w: herdr schema probe failed: %s", ErrEventUnavailable, info.Err)
	case HerdrUnsupported:
		return fmt.Errorf("%w: herdr protocol %d outside supported range [%d, %d]", ErrEventProtocolMismatch, info.Protocol, MinSupportedProtocol, MaxSupportedProtocol)
	default:
		return fmt.Errorf("%w: unexpected herdr capability state %q", ErrEventUnavailable, info.State)
	}
}

// Wait performs a bounded wait on the herdr agent status surface. It returns
// a normalized ObservationSignal (activity hint + cursor) or a typed fallback
// error per the ObservationEventSource contract. The signal is a wake hint
// only: it never carries lifecycle and never sets a Task phase.
func (s *HerdrEventSource) Wait(ctx context.Context, endpoint EndpointRef, after EventCursor) (ObservationSignal, error) {
	if err := s.negotiate(); err != nil {
		return ObservationSignal{}, err
	}
	if endpoint.Handle == "" {
		return ObservationSignal{}, fmt.Errorf("%w: event wait requires an endpoint handle", ErrEventUnavailable)
	}

	bin := s.CLIPath
	if bin == "" {
		var err error
		bin, err = exec.LookPath("herdr")
		if err != nil {
			return ObservationSignal{}, fmt.Errorf("%w: herdr not found on PATH", ErrEventUnavailable)
		}
	}

	// Bounded wait: derive the CLI timeout from the context deadline so the
	// process exits when the orchestrator's bounded wait elapses.
	timeoutMS := int64(30_000) // fallback bound when no deadline
	if dl, ok := ctx.Deadline(); ok {
		rem := time.Until(dl)
		if rem <= 0 {
			return ObservationSignal{}, context.DeadlineExceeded
		}
		timeoutMS = rem.Milliseconds()
		if timeoutMS < 1 {
			timeoutMS = 1
		}
	}

	args := []string{"--session", s.Session, "agent", "wait", endpoint.Handle,
		"--timeout", strconv.FormatInt(timeoutMS, 10)}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(cmd.Environ(), "HERDR_SESSION="+s.Session)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			// Bounded wait elapsed — normal; caller proceeds to poll.
			return ObservationSignal{}, ctx.Err()
		}
		// Reconstruct the CLI error message (exit envelope may carry a JSON
		// error body on stderr) so typed classification below can see it.
		werr := err
		if ee, ok := err.(*exec.ExitError); ok {
			combined := strings.TrimSpace(string(ee.Stderr))
			if combined == "" {
				combined = strings.TrimSpace(string(out))
			}
			if combined != "" {
				werr = fmt.Errorf("%s", combined)
			}
		}
		if isHerdrProtocolMismatch(werr) {
			return ObservationSignal{}, fmt.Errorf("%w: %v", ErrEventProtocolMismatch, werr)
		}
		if isHerdrWaitTimeout(werr) {
			// The herdr CLI's own bounded wait elapsed and it reported a
			// structured timeout. This is the same normal bounded-wait
			// outcome as a context deadline: wrap context.DeadlineExceeded so
			// the orchestrator classifies it as a timeout (poll fallback)
			// instead of a generic reader failure. Caller cancellation still
			// takes precedence via ctx.Err() above.
			return ObservationSignal{}, fmt.Errorf("%w: %v", context.DeadlineExceeded, werr)
		}
		if isNotFoundErr(werr) {
			// A vanished pane during an event wait is an absence hint, but a
			// native event must not self-assert lifecycle: the orchestrator
			// re-probes the exact binding before any policy decision.
			return ObservationSignal{}, fmt.Errorf("%w: pane absent during event wait; re-probe required: %v", ErrEventReaderFailure, werr)
		}
		return ObservationSignal{}, fmt.Errorf("%w: %v", ErrEventReaderFailure, werr)
	}

	sig, perr := s.parseAgentWait(endpoint, out)
	if perr != nil {
		return ObservationSignal{}, fmt.Errorf("%w: %v", ErrEventReaderFailure, perr)
	}
	return sig, nil
}

// agentWaitResult models the subset of the herdr agent-wait response the
// adapter normalizes. Wire shape is adapter-owned; unknown fields are ignored.
type agentWaitResult struct {
	AgentStatus    string `json:"agent_status"`
	StateChangeSeq uint64 `json:"state_change_seq"`
	PaneID         string `json:"pane_id"`
}

// parseAgentWait normalizes the herdr agent-wait JSON response into an
// ObservationSignal. It is a pure adapter-side normalization: only the
// activity hint and cursor are extracted; no lifecycle is inferred. Missing/
// malformed status normalizes to ActivityUnknown (never idle, never busy).
func (s *HerdrEventSource) parseAgentWait(endpoint EndpointRef, raw []byte) (ObservationSignal, error) {
	var res agentWaitResult
	if err := json.Unmarshal(raw, &res); err != nil {
		// The herdr CLI may emit an error envelope instead of a result on
		// some non-zero paths; treat unparseable output as a reader failure.
		return ObservationSignal{}, fmt.Errorf("unparseable agent-wait response: %v", err)
	}
	sig := ObservationSignal{
		Endpoint:    endpoint,
		Incarnation: "", // an adapter cannot attest the opaque launch incarnation (P1a rule)
		Activity:    NormalizeActivityHint(res.AgentStatus),
		Source:      SourceEvent,
		ObservedAt:  time.Now().UTC(),
		Detail:      "herdr agent wait: " + res.AgentStatus,
	}
	if res.StateChangeSeq > 0 {
		sig.Cursor = EventCursor(strconv.FormatUint(res.StateChangeSeq, 10))
	}
	if res.PaneID != "" && endpoint.Handle != "" && res.PaneID != endpoint.Handle {
		sig.Detail += fmt.Sprintf(" (response pane %q != requested %q)", res.PaneID, endpoint.Handle)
	}
	return sig, nil
}

// After implements the adapter-owned cursor ordering for the herdr surface:
// it reports whether a signal cursor advances past the consumed cursor. Cursor
// semantics stay adapter-owned (numeric state_change_seq, lexical fallback for
// non-numeric); the orchestrator never parses or compares cursors itself.
// Empty cursors compare as "no position" and are accepted (the orchestrator's
// duplicate/out-of-order suppression is authoritative).
func (s *HerdrEventSource) After(sigC, prev EventCursor) bool {
	if prev == "" {
		return true
	}
	if sigC == "" {
		return true
	}
	a, errA := strconv.ParseUint(string(prev), 10, 64)
	b, errB := strconv.ParseUint(string(sigC), 10, 64)
	if errA != nil || errB != nil {
		// Non-numeric cursors fall back to lexical comparison.
		return strings.Compare(string(sigC), string(prev)) > 0
	}
	return b > a
}

var _ ObservationEventSource = (*HerdrEventSource)(nil)
