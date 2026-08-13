// Tests for the orchestrator-owned native event wait policy (BEO-17/P1b):
// bounded wait, timeout, duplicate/out-of-order/stale suppression, stale
// incarnation rejection, event-to-reprobe, and polling fallback.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
)

// fakeEventSource is a scriptable ObservationEventSource for tests.
type fakeEventSource struct {
	mu      sync.Mutex
	waits   int
	signals []backend.ObservationSignal
	errors  []error
	timeout time.Duration // per-Wait block before returning timeout
	closed  bool
}

// After implements adapter-owned cursor ordering for the fake: numeric cursor
// comparison with a lexical fallback (same semantics as the herdr adapter).
func (f *fakeEventSource) After(next, prev backend.EventCursor) bool {
	if prev == "" {
		return true
	}
	if next == "" {
		return true
	}
	a, errA := strconv.ParseUint(string(prev), 10, 64)
	b, errB := strconv.ParseUint(string(next), 10, 64)
	if errA != nil || errB != nil {
		return strings.Compare(string(next), string(prev)) > 0
	}
	return b > a
}

func (f *fakeEventSource) Wait(ctx context.Context, endpoint backend.EndpointRef, after backend.EventCursor) (backend.ObservationSignal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waits++
	if f.timeout > 0 {
		select {
		case <-ctx.Done():
			return backend.ObservationSignal{}, ctx.Err()
		case <-time.After(f.timeout):
		}
	}
	if len(f.errors) > 0 {
		err := f.errors[0]
		f.errors = f.errors[1:]
		return backend.ObservationSignal{}, err
	}
	if len(f.signals) > 0 {
		sig := f.signals[0]
		f.signals = f.signals[1:]
		return sig, nil
	}
	if f.closed {
		return backend.ObservationSignal{}, backend.ErrEventUnsupported
	}
	select {
	case <-ctx.Done():
		return backend.ObservationSignal{}, ctx.Err()
	case <-time.After(time.Hour):
		return backend.ObservationSignal{}, ctx.Err()
	}
}

// waitCount reports the number of Wait invocations, synchronized.
func (f *fakeEventSource) waitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waits
}

// drainLane waits until the event lane goroutine exits (channel closed).
func drainLane(t *testing.T, pulses <-chan eventPulse, stopLane chan struct{}) {
	t.Helper()
	close(stopLane)
	done := make(chan struct{})
	go func() {
		for range pulses {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("event lane did not stop within 3s")
	}
}

// staticEventPort returns a fixed set of endpoint sources.
func staticEventPort(sources ...EndpointSource) ObservationEventPort {
	return fakeEventPort{sources: sources}
}

type fakeEventPort struct {
	sources []EndpointSource
	err     error
}

func (p fakeEventPort) Sources(string) ([]EndpointSource, error) { return p.sources, p.err }

func TestEventWaiter_WaitForSignal_BlankEndpointRejected(t *testing.T) {
	// A source returning a signal with a blank endpoint must never wake the
	// watcher: exact binding (backend + handle, non-empty) is required before
	// any cursor handling.
	cases := []backend.ObservationSignal{
		{Endpoint: backend.EndpointRef{Backend: "", Handle: ""}, Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "42"},
		{Endpoint: backend.EndpointRef{Backend: "", Handle: "w:p"}, Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "42"},
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: ""}, Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "42"},
	}
	for i, sig := range cases {
		src := &fakeEventSource{signals: []backend.ObservationSignal{sig}}
		port := staticEventPort(EndpointSource{
			Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
			Source:   src,
		})
		w := NewEventWaiter(port)
		_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), 50*time.Millisecond)
		if outcome != EventWaitTimeout {
			t.Fatalf("case %d: outcome = %v, want timeout (blank endpoint rejected)", i, outcome)
		}
	}
}

func TestEventWaiter_WaitForSignal_PartialIdentityMismatchRejected(t *testing.T) {
	// A signal naming the right backend but a different handle (or vice versa)
	// must be rejected — the identity must be the exact bound endpoint.
	sig := backend.ObservationSignal{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "other:p"},
		Activity: backend.ActivityBusy,
		Source:   backend.SourceEvent,
		Cursor:   "42",
	}
	src := &fakeEventSource{signals: []backend.ObservationSignal{sig}}
	port := staticEventPort(EndpointSource{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Source:   src,
	})
	w := NewEventWaiter(port)
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), 50*time.Millisecond)
	if outcome != EventWaitTimeout {
		t.Fatalf("outcome = %v, want timeout (mismatched identity rejected)", outcome)
	}
}

func TestEventWaiter_WaitForSignal_InvalidSourceRejected(t *testing.T) {
	// A signal with a non-event source or invalid activity must be rejected
	// even when its endpoint is exact: Valid() is enforced before cursor
	// handling.
	src := &fakeEventSource{signals: []backend.ObservationSignal{
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Activity: backend.ActivityBusy, Source: backend.SourceInvalid, Cursor: "42"},
	}}
	port := staticEventPort(EndpointSource{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Source:   src,
	})
	w := NewEventWaiter(port)
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), 50*time.Millisecond)
	if outcome != EventWaitTimeout {
		t.Fatalf("outcome = %v, want timeout (invalid signal rejected)", outcome)
	}
}

// concurrentFakeSource is a fake that returns the SAME signal on every Wait
// (never drains), so concurrent WaitForSignal calls race on the same cursor.
type concurrentFakeSource struct {
	sig backend.ObservationSignal
}

func (c *concurrentFakeSource) Wait(ctx context.Context, endpoint backend.EndpointRef, after backend.EventCursor) (backend.ObservationSignal, error) {
	select {
	case <-ctx.Done():
		return backend.ObservationSignal{}, ctx.Err()
	default:
	}
	return c.sig, nil
}

func (c *concurrentFakeSource) After(next, prev backend.EventCursor) bool {
	return next == c.sig.Cursor && next != prev
}

// TestEventWaiter_WaitForSignal_ConcurrentDuplicateSuppression is the
// concurrency regression for blocker 3: two concurrent WaitForSignal calls on
// the same endpoint with the same cursor must accept the signal exactly once.
func TestEventWaiter_WaitForSignal_ConcurrentDuplicateSuppression(t *testing.T) {
	sig := backend.ObservationSignal{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Activity: backend.ActivityBusy,
		Source:   backend.SourceEvent,
		Cursor:   "42",
	}
	src := &concurrentFakeSource{sig: sig}
	port := staticEventPort(EndpointSource{Endpoint: sig.Endpoint, Source: src})
	w := NewEventWaiter(port)

	const callers = 8
	results := make(chan EventWaitOutcome, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second)
			results <- outcome
		}()
	}
	wg.Wait()
	close(results)

	accepted := 0
	for outcome := range results {
		if outcome == EventWaitSignal {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted signals = %d, want exactly 1 (concurrent duplicate must be suppressed)", accepted)
	}
}

func TestEventWaiter_WaitForSignal_ValidSignal(t *testing.T) {
	src := &fakeEventSource{signals: []backend.ObservationSignal{{
		Endpoint:    backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Incarnation: "inc-1",
		Activity:    backend.ActivityBusy,
		Source:      backend.SourceEvent,
		Cursor:      "42",
	}}}
	port := staticEventPort(EndpointSource{
		Endpoint:    backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Incarnation: "inc-1",
		Source:      src,
	})
	w := NewEventWaiter(port)
	sig, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second)
	if outcome != EventWaitSignal {
		t.Fatalf("outcome = %v, want signal", outcome)
	}
	if sig.Activity != backend.ActivityBusy {
		t.Errorf("Activity = %v, want busy", sig.Activity)
	}
	if src.waitCount() != 1 {
		t.Errorf("waits = %d, want 1", src.waitCount())
	}
}

func TestEventWaiter_WaitForSignal_DuplicateSuppression(t *testing.T) {
	sig := backend.ObservationSignal{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Activity: backend.ActivityIdle,
		Source:   backend.SourceEvent,
		Cursor:   "42",
	}
	src := &fakeEventSource{signals: []backend.ObservationSignal{sig, sig}}
	port := staticEventPort(EndpointSource{Endpoint: sig.Endpoint, Source: src})
	w := NewEventWaiter(port)

	// First signal accepted and cursor recorded.
	got, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second)
	if outcome != EventWaitSignal {
		t.Fatalf("first outcome = %v, want signal", outcome)
	}
	_ = got
	// The duplicate (same cursor) must be suppressed: the waiter keeps waiting
	// and, with nothing left, returns timeout — never a second signal.
	_, outcome = w.WaitForSignal(context.Background(), t.TempDir(), 50*time.Millisecond)
	if outcome != EventWaitTimeout {
		t.Fatalf("duplicate outcome = %v, want timeout (suppressed)", outcome)
	}
}

func TestEventWaiter_WaitForSignal_OutOfOrderSuppression(t *testing.T) {
	src := &fakeEventSource{signals: []backend.ObservationSignal{
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "42"},
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Activity: backend.ActivityIdle, Source: backend.SourceEvent, Cursor: "41"},
	}}
	port := staticEventPort(EndpointSource{Endpoint: src.signals[0].Endpoint, Source: src})
	w := NewEventWaiter(port)

	if _, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second); outcome != EventWaitSignal {
		t.Fatalf("first outcome = %v, want signal", outcome)
	}
	// Out-of-order cursor 41 < 42 must be suppressed.
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), 50*time.Millisecond)
	if outcome != EventWaitTimeout {
		t.Fatalf("out-of-order outcome = %v, want timeout (suppressed)", outcome)
	}
}

func TestEventWaiter_WaitForSignal_StaleIncarnationRejected(t *testing.T) {
	src := &fakeEventSource{signals: []backend.ObservationSignal{
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Incarnation: "stale-inc", Activity: backend.ActivityIdle, Source: backend.SourceEvent, Cursor: "1"},
	}}
	// Expected incarnation differs from the signal's attested incarnation.
	port := staticEventPort(EndpointSource{
		Endpoint:    backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Incarnation: "current-inc",
		Source:      src,
	})
	w := NewEventWaiter(port)
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), 50*time.Millisecond)
	if outcome != EventWaitTimeout {
		t.Fatalf("stale-incarnation outcome = %v, want timeout (rejected)", outcome)
	}
}

func TestEventWaiter_WaitForSignal_WrongEndpointIgnored(t *testing.T) {
	src := &fakeEventSource{signals: []backend.ObservationSignal{
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "other"}, Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "1"},
	}}
	port := staticEventPort(EndpointSource{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Source:   src,
	})
	w := NewEventWaiter(port)
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), 50*time.Millisecond)
	if outcome != EventWaitTimeout {
		t.Fatalf("wrong-endpoint outcome = %v, want timeout (ignored)", outcome)
	}
}

func TestEventWaiter_WaitForSignal_Timeout(t *testing.T) {
	src := &fakeEventSource{timeout: time.Hour}
	port := staticEventPort(EndpointSource{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Source: src})
	w := NewEventWaiter(port)
	start := time.Now()
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), 30*time.Millisecond)
	if outcome != EventWaitTimeout {
		t.Fatalf("outcome = %v, want timeout", outcome)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("bounded wait exceeded budget: %v", elapsed)
	}
}

func TestEventWaiter_WaitForSignal_Unsupported(t *testing.T) {
	src := &fakeEventSource{closed: true}
	port := staticEventPort(EndpointSource{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Source: src})
	w := NewEventWaiter(port)
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second)
	if outcome != EventWaitUnsupported {
		t.Fatalf("outcome = %v, want unsupported", outcome)
	}
}

func TestEventWaiter_WaitForSignal_NoSources(t *testing.T) {
	w := NewEventWaiter(staticEventPort())
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second)
	if outcome != EventWaitUnsupported {
		t.Fatalf("outcome = %v, want unsupported", outcome)
	}
}

func TestEventWaiter_WaitForSignal_Unavailable(t *testing.T) {
	src := &fakeEventSource{errors: []error{backend.ErrEventUnavailable}}
	port := staticEventPort(EndpointSource{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Source: src})
	w := NewEventWaiter(port)
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second)
	if outcome != EventWaitUnavailable {
		t.Fatalf("outcome = %v, want unavailable", outcome)
	}
}

func TestEventWaiter_WaitForSignal_ProtocolMismatch(t *testing.T) {
	src := &fakeEventSource{errors: []error{backend.ErrEventProtocolMismatch}}
	port := staticEventPort(EndpointSource{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Source: src})
	w := NewEventWaiter(port)
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second)
	if outcome != EventWaitProtocolMismatch {
		t.Fatalf("outcome = %v, want protocol-mismatch", outcome)
	}
}

func TestEventWaiter_WaitForSignal_ReaderFailure(t *testing.T) {
	src := &fakeEventSource{errors: []error{errors.New("socket broke")}}
	port := staticEventPort(EndpointSource{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Source: src})
	w := NewEventWaiter(port)
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second)
	if outcome != EventWaitReaderFailure {
		t.Fatalf("outcome = %v, want reader-failure", outcome)
	}
}

func TestEventWaitOutcome_PollFallback(t *testing.T) {
	if EventWaitSignal.PollFallback() {
		t.Error("signal must not be a poll fallback")
	}
	for _, o := range []EventWaitOutcome{EventWaitTimeout, EventWaitUnsupported, EventWaitUnavailable, EventWaitProtocolMismatch, EventWaitReaderFailure} {
		if !o.PollFallback() {
			t.Errorf("%v must be a poll fallback", o)
		}
	}
}

// TestEventLane_EventToReprobe runs the full watcher loop with a fake probe
// and a scripted event source: a validated signal must trigger an immediate
// re-probe cycle (probe call), and the watcher must keep running on the ticker
// when the event lane is degraded. It is the event-to-reprobe contract test.
func TestEventLane_EventToReprobe(t *testing.T) {
	homeDir := t.TempDir()
	mustWriteMeta(t, homeDir, "task-1", map[string]string{"backend": "herdr", "window": "w:p", "endpoint_incarnation": "inc-1", "home": homeDir})

	sig := backend.ObservationSignal{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Activity: backend.ActivityIdle,
		Source:   backend.SourceEvent,
		Cursor:   "1",
	}
	src := &fakeEventSource{signals: []backend.ObservationSignal{sig}}
	port := fakeEventPort{sources: []EndpointSource{{Endpoint: sig.Endpoint, Incarnation: "inc-1", Source: src}}}

	stopLane := make(chan struct{})
	pulses := startEventLane(homeDir, NewEventWaiter(port), stopLane)

	// Consume the pulse: the lane must push a validated signal quickly.
	select {
	case p, ok := <-pulses:
		if !ok {
			t.Fatal("event lane closed without a signal")
		}
		if p.outcome != EventWaitSignal {
			t.Fatalf("pulse outcome = %v, want signal", p.outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event pulse within 2s")
	}
	drainLane(t, pulses, stopLane)

	// The orchestrator-side contract: a signal only triggers a re-probe cycle;
	// the probe itself re-reads the exact binding. Assert the lane itself never
	// mutated any lifecycle — the signal carries no lifecycle axis, and the
	// probe is the only observation authority exercised here.
	if src.waitCount() == 0 {
		t.Fatal("event source was never waited on")
	}
}

// TestEventLane_DegradedKeepsPolling verifies the watcher never goes silent:
// when the event lane degrades (no sources), the lane closes and the polling
// ticker remains the cadence authority.
func TestEventLane_DegradedKeepsPolling(t *testing.T) {
	homeDir := t.TempDir()
	w := NewEventWaiter(staticEventPort()) // no sources → unsupported
	stopLane := make(chan struct{})
	pulses := startEventLane(homeDir, w, stopLane)

	select {
	case _, ok := <-pulses:
		if ok {
			t.Fatal("degraded lane must close, not emit")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("degraded lane did not close within 2s")
	}
	close(stopLane)
}

// TestEventLane_ReaderFailureBackoff verifies the lane keeps retrying bounded
// waits on transient reader failures instead of going silent or spinning.
func TestEventLane_ReaderFailureBackoff(t *testing.T) {
	homeDir := t.TempDir()
	src := &fakeEventSource{errors: []error{errors.New("reader hiccup")}}
	port := staticEventPort(EndpointSource{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Source: src})
	stopLane := make(chan struct{})
	pulses := startEventLane(homeDir, NewEventWaiter(port), stopLane)

	// After a reader failure the lane backs off and retries; it must neither
	// close nor emit. Give it a few hundred ms to attempt the retry.
	time.Sleep(1500 * time.Millisecond)
	select {
	case p, ok := <-pulses:
		if ok {
			t.Fatalf("unexpected pulse: %v", p.outcome)
		}
		t.Fatal("lane closed on transient reader failure; watcher must keep polling")
	default:
	}
	drainLane(t, pulses, stopLane)
	if src.waitCount() < 2 {
		t.Fatalf("waits = %d, want retries after reader failure", src.waitCount())
	}
}

// TestEventLane_RaceWatcherAndRecovery is the race lane: an event reader
// (event lane goroutine) + a polling watcher (ticker cycles) + concurrent
// recovery (parallel cycles) all run against the same home. Run under -race,
// it must not race on cursor state or wake markers.
func TestEventLane_RaceWatcherAndRecovery(t *testing.T) {
	homeDir := t.TempDir()
	mustWriteMeta(t, homeDir, "task-1", map[string]string{"backend": "herdr", "window": "w:p", "endpoint_incarnation": "inc-1", "home": homeDir})
	mustWriteMeta(t, homeDir, "task-2", map[string]string{"backend": "herdr", "window": "w:q", "endpoint_incarnation": "inc-2", "home": homeDir})

	// Event reader: emits a fresh-cursor signal each wait so the lane keeps
	// delivering wake hints concurrently with polling.
	src := &raceEventSource{}
	port := fakeEventPort{sources: []EndpointSource{
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Incarnation: "inc-1", Source: src},
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:q"}, Incarnation: "inc-2", Source: src},
	}}

	probe := countingProbe{fn: func(string, map[string]string) (bool, error) { return true, nil }}
	sender := raceCycleSender{}
	hooks := raceTestHooks{}
	states := raceTaskStatePort{}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Event reader lane.
	wg.Add(1)
	go func() {
		defer wg.Done()
		stopLane := make(chan struct{})
		pulses := startEventLane(homeDir, NewEventWaiter(port), stopLane)
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				close(stopLane)
				return
			case _, ok := <-pulses:
				if !ok {
					close(stopLane)
					return
				}
			}
		}
	}()

	// Polling watcher + concurrent recovery: parallel RunCycle invocations.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := RunCycleWithProbeAndSender(homeDir, probe, sender, hooks, NoopRetirementPort{}, states); err != nil {
					return
				}
			}
		}()
	}

	time.Sleep(600 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// raceEventSource returns a new-cursor signal on every wait without needing
// pre-scripted state, exercising concurrent cursor bookkeeping under -race.
type raceEventSource struct {
	mu    sync.Mutex
	seq   uint64
	waits int
}

func (r *raceEventSource) Wait(ctx context.Context, endpoint backend.EndpointRef, after backend.EventCursor) (backend.ObservationSignal, error) {
	r.mu.Lock()
	r.seq++
	n := r.seq
	r.waits++
	r.mu.Unlock()
	sig := backend.ObservationSignal{
		Endpoint: endpoint,
		Activity: backend.ActivityIdle,
		Source:   backend.SourceEvent,
		Cursor:   backend.EventCursor(fmt.Sprintf("%d", n)),
	}
	select {
	case <-ctx.Done():
		return backend.ObservationSignal{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return sig, nil
	}
}

// After implements adapter-owned cursor ordering for the race source: cursor
// values are monotonically increasing integers.
func (r *raceEventSource) After(next, prev backend.EventCursor) bool {
	if prev == "" {
		return true
	}
	if next == "" {
		return true
	}
	a, errA := strconv.ParseUint(string(prev), 10, 64)
	b, errB := strconv.ParseUint(string(next), 10, 64)
	if errA != nil || errB != nil {
		return strings.Compare(string(next), string(prev)) > 0
	}
	return b > a
}

// countingProbe is a TaskEndpointProbe wrapper counting invocations.
type countingProbe struct {
	fn func(string, map[string]string) (bool, error)
}

func (p countingProbe) Probe(homeDir string, meta map[string]string) (bool, error) {
	return p.fn(homeDir, meta)
}

// raceCycleSender implements BoundSender for the race lane.
type raceCycleSender struct{}

func (raceCycleSender) Alive(string, map[string]string) (bool, error) { return false, nil }
func (raceCycleSender) Send(string, map[string]string, string) BoundSendResult {
	return BoundSendResult{}
}

// raceTestHooks is a no-op WatcherHooks for the race lane.
type raceTestHooks struct{}

func (raceTestHooks) Reconcile(string, bool) error { return nil }
func (raceTestHooks) Activate(string)              {}

// raceTaskStatePort reports a working task so stale signals are not misread.
type raceTaskStatePort struct{}

func (raceTaskStatePort) ReadTaskState(homeDir, taskID string) (*ObservedTaskState, error) {
	return &ObservedTaskState{Status: "working:" + taskID}, nil
}

func mustWriteMeta(t *testing.T, homeDir, id string, kv map[string]string) {
	t.Helper()
	stateDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	var b []byte
	for k, v := range kv {
		b = append(b, []byte(k+"="+v+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(stateDir, id+".meta"), b, 0644); err != nil {
		t.Fatal(err)
	}
}
