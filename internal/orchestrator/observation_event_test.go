// Tests for the orchestrator-owned native event wait policy:
// bounded wait, timeout, duplicate/out-of-order/stale suppression,
// event-to-reprobe, and polling fallback.
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/home"
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
	// A real adapter (e.g. exec.CommandContext) blocks without holding any
	// internal lock, and its bounded wait is driven by the context. Model that
	// here so concurrent callers' waits are genuinely cancellable and a
	// queued caller's short deadline is never masked by a long in-flight wait.
	if f.timeout > 0 {
		select {
		case <-ctx.Done():
			return backend.ObservationSignal{}, ctx.Err()
		case <-time.After(f.timeout):
		}
	}

	f.mu.Lock()
	f.waits++
	hasError := len(f.errors) > 0
	hasSignal := len(f.signals) > 0
	closed := f.closed
	var err error
	var sig backend.ObservationSignal
	if hasError {
		err = f.errors[0]
		f.errors = f.errors[1:]
	} else if hasSignal {
		sig = f.signals[0]
		f.signals = f.signals[1:]
	}
	f.mu.Unlock()

	if hasError {
		return backend.ObservationSignal{}, err
	}
	if hasSignal {
		return sig, nil
	}
	if closed {
		return backend.ObservationSignal{}, backend.ErrEventUnsupported
	}
	// No scripted outcome left: block until the bounded context elapses
	// (normal timeout), without holding the internal lock across the block.
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

// sequenceEventPort returns a different source set per Sources call, in order.
type sequenceEventPort struct {
	mu     sync.Mutex
	stages [][]EndpointSource
	next   int
}

func (p *sequenceEventPort) Sources(string) ([]EndpointSource, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next >= len(p.stages) {
		return nil, nil
	}
	s := p.stages[p.next]
	p.next++
	return s, nil
}

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
	for _, src := range []backend.ObservationSource{backend.SourceInvalid, backend.SourceProbe, backend.SourceDerived} {
		src := &fakeEventSource{signals: []backend.ObservationSignal{
			{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Activity: backend.ActivityBusy, Source: src, Cursor: "42"},
		}}
		port := staticEventPort(EndpointSource{
			Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
			Source:   src,
		})
		w := NewEventWaiter(port)
		_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), 50*time.Millisecond)
		if outcome != EventWaitTimeout {
			t.Fatalf("source %v: outcome = %v, want timeout (invalid signal rejected)", src, outcome)
		}
	}
}

// blockingEventSource models a real adapter whose Wait blocks until the
// context is done, WITHOUT holding an internal lock across the block (like
// exec.CommandContext on the real herdr adapter). It is used to prove that a
// queued caller's bounded wait is not blocked by an in-flight long wait.
type blockingEventSource struct {
	release chan struct{} // optional external release; when closed, Wait returns a signal
	// exited, when non-nil, records WHY the block ended: the context error on
	// ctx.Done (context.Canceled when the caller tore the fan-out down after
	// another endpoint won, context.DeadlineExceeded when the shared budget
	// ran out) or nil when released externally. Buffered by the test; the
	// send is non-blocking so an unread channel never stalls a fake.
	exited chan error
	// entered, when non-nil, receives once per Wait as it starts blocking.
	// Buffered by the test; the send is non-blocking like exited.
	entered chan struct{}
}

func (b *blockingEventSource) recordExit(reason error) {
	if b.exited == nil {
		return
	}
	select {
	case b.exited <- reason:
	default:
	}
}

func (b *blockingEventSource) Wait(ctx context.Context, endpoint backend.EndpointRef, after backend.EventCursor) (backend.ObservationSignal, error) {
	if b.entered != nil {
		select {
		case b.entered <- struct{}{}:
		default:
		}
	}
	select {
	case <-ctx.Done():
		b.recordExit(ctx.Err())
		return backend.ObservationSignal{}, ctx.Err()
	case <-b.release:
		b.recordExit(nil)
		return backend.ObservationSignal{
			Endpoint: endpoint,
			Activity: backend.ActivityIdle,
			Source:   backend.SourceEvent,
			Cursor:   "1",
		}, nil
	}
}

func (b *blockingEventSource) After(next, prev backend.EventCursor) bool { return next != prev }

// gatedEventSource models a real adapter whose Wait blocks until an external
// release and then returns a scripted signal. It signals when Wait has been
// entered so a test can deterministically order overlapping waits.
type gatedEventSource struct {
	entered chan struct{}
	release chan struct{}
	sig     backend.ObservationSignal
}

func (g *gatedEventSource) Wait(ctx context.Context, endpoint backend.EndpointRef, after backend.EventCursor) (backend.ObservationSignal, error) {
	select {
	case <-g.entered:
	default:
		close(g.entered)
	}
	select {
	case <-ctx.Done():
		return backend.ObservationSignal{}, ctx.Err()
	case <-g.release:
		return g.sig, nil
	}
}

func (g *gatedEventSource) After(next, prev backend.EventCursor) bool { return next != prev }

// TestEventWaiter_WaitForSignal_ConcurrentBoundedWait is the blocker-2
// regression: caller A holds a long in-flight wait on an endpoint while caller
// B waits on the SAME endpoint with a short timeout. B must return within its
// own bound (the per-endpoint mutex is never held across the blocking
// Source.Wait).
func TestEventWaiter_WaitForSignal_ConcurrentBoundedWait(t *testing.T) {
	home := t.TempDir()
	src := &blockingEventSource{release: make(chan struct{}), entered: make(chan struct{}, 1)}
	port := staticEventPort(EndpointSource{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Source:   src,
	})
	w := NewEventWaiter(port)

	// Caller A: long bounded wait that blocks the source.
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		w.WaitForSignal(context.Background(), home, 2*time.Second)
	}()

	// Once A is blocked inside Source.Wait, caller B with a short bound on the
	// same endpoint must return within its own bound.
	select {
	case <-src.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("caller A never entered Source.Wait")
	}
	start := time.Now()
	_, outcome := w.WaitForSignal(context.Background(), home, 50*time.Millisecond)
	elapsed := time.Since(start)
	if outcome != EventWaitTimeout {
		t.Fatalf("outcome = %v, want timeout", outcome)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("B was blocked by A's long wait: elapsed %v, want <= ~50ms", elapsed)
	}

	close(src.release)
	<-doneA
}

// TestEventWaiter_WaitForSignal_ConcurrentOrderedEvents: two concurrent waits
// on the same endpoint return distinct, ordered cursors (1 then 2) and BOTH
// must be accepted; the adapter-owned After compare-and-record accepts cursor
// 2 after cursor 1.
func TestEventWaiter_WaitForSignal_ConcurrentOrderedEvents(t *testing.T) {
	ep := backend.EndpointRef{Backend: "herdr", Handle: "w:p"}
	srcA := &gatedEventSource{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		sig: backend.ObservationSignal{
			Endpoint: ep,
			Activity: backend.ActivityBusy,
			Source:   backend.SourceEvent,
			Cursor:   "1",
		},
	}
	srcB := &gatedEventSource{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		sig: backend.ObservationSignal{
			Endpoint: ep,
			Activity: backend.ActivityIdle,
			Source:   backend.SourceEvent,
			Cursor:   "2",
		},
	}
	es := func(src ObservationEventSource) EndpointSource {
		return EndpointSource{Endpoint: ep, Source: src}
	}
	w := NewEventWaiter(staticEventPort(es(srcA), es(srcB)))

	// Both waits start and block inside Source.Wait.
	type result struct {
		sig     backend.ObservationSignal
		outcome EventWaitOutcome
	}
	doneA := make(chan result, 1)
	doneB := make(chan result, 1)
	go func() {
		sig, outcome := w.waitEndpoint(context.Background(), es(srcA))
		doneA <- result{sig, outcome}
	}()
	go func() {
		sig, outcome := w.waitEndpoint(context.Background(), es(srcB))
		doneB <- result{sig, outcome}
	}()
	<-srcA.entered
	<-srcB.entered

	// Release A first: cursor 1 accepted.
	close(srcA.release)
	rA := <-doneA
	if rA.outcome != EventWaitSignal || rA.sig.Cursor != "1" {
		t.Fatalf("first wait = (outcome %v, cursor %q), want (signal, %q)", rA.outcome, rA.sig.Cursor, "1")
	}

	// Release B: cursor 2 is a valid ordered event and must ALSO be accepted.
	close(srcB.release)
	rB := <-doneB
	if rB.outcome != EventWaitSignal || rB.sig.Cursor != "2" {
		t.Fatalf("second wait = (outcome %v, cursor %q), want (signal, %q)", rB.outcome, rB.sig.Cursor, "2")
	}
}

// TestEventWaiter_WaitForSignal_MultiEndpointWake is the blocker-3
// regression: endpoint A blocks for its whole budget while endpoint B has a
// signal ready. The fan-out must deliver B's signal promptly instead of
// starving B behind A's blocking wait.
//
// "Promptly" is asserted as ORDERING, not wall-clock. The old guard measured
// elapsed <= 250ms against a 300ms budget — 1.2x headroom, so one delayed
// goroutine wake-up on a loaded runner or plain -race overhead could redden
// `main` with no regression behind it. Instead, A's blocking source records
// why its wait ended: on a healthy fan-out B's signal wins first and the
// caller's deferred cancel tears A down (context.Canceled) with the budget
// untouched; if B were starved behind A, A would run to the shared deadline
// (context.DeadlineExceeded) and B would only be looked at afterwards. That
// distinction is a property of the wait's shape, so runner slowness does not
// change the verdict — a starved fan-out fails on a fast machine, and a
// healthy one passes on any machine slow enough to still finish inside the 2s
// budget.
//
// The trade-off, stated plainly: this test is ORDERING-ONLY. It bounds no
// latency at all below the 2s budget — sleep a second before the fan-out loop
// and it still passes. A wake-latency regression that stays under budget is
// out of scope here and needs its own (benchmark-shaped) guard.
func TestEventWaiter_WaitForSignal_MultiEndpointWake(t *testing.T) {
	home := t.TempDir()
	blockSrc := &blockingEventSource{release: make(chan struct{}), exited: make(chan error, 1)}
	sigSrc := &fakeEventSource{signals: []backend.ObservationSignal{
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:q"}, Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "1"},
	}}
	port := staticEventPort(
		EndpointSource{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Source: blockSrc},
		EndpointSource{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:q"}, Source: sigSrc},
	)
	w := NewEventWaiter(port)

	// The budget is a safety net, not the signal under test: the healthy path
	// never spends it (B's signal is ready before the wait starts) and nothing
	// asserts how much of it elapsed.
	sig, outcome := w.WaitForSignal(context.Background(), home, 2*time.Second)
	if outcome != EventWaitSignal {
		t.Fatalf("outcome = %v, want signal from B", outcome)
	}
	if sig.Endpoint.Handle != "w:q" {
		t.Errorf("signal endpoint = %+v, want w:q", sig.Endpoint)
	}

	// A's wait is already unblocked here: WaitForSignal's deferred cancel runs
	// before it returns, so this receive cannot hang on a healthy fan-out. The
	// timeout is not for that case — it is for the regression where the fan-out
	// never waits on A at all: an unguarded receive would then block until
	// `go test -timeout` panics the whole package (10 minutes on CI) instead of
	// failing this one test in seconds.
	//
	// context.Canceled is unambiguous here only because it is paired with the
	// outcome != EventWaitSignal gate above AND because ctx is
	// context.Background(): the only cancel that can reach A is WaitForSignal's
	// own deferred one. If anyone later passes a cancellable ctx into this test,
	// that pair weakens and the assertion needs rethinking.
	select {
	case reason := <-blockSrc.exited:
		if !errors.Is(reason, context.Canceled) {
			t.Fatalf("A's blocking wait ended with %v, want context.Canceled: the fan-out's "+
				"teardown of A did not happen as expected — either B's signal was delivered "+
				"only after A consumed the shared budget (B starved behind A), or the "+
				"deferred cancel never ran", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("A's wait never ended: the fan-out never waited on A at all")
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
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Activity: backend.ActivityBusy,
		Source:   backend.SourceEvent,
		Cursor:   "42",
	}}}
	port := staticEventPort(EndpointSource{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Source:   src,
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
	if outcome != EventWaitNoSources {
		t.Fatalf("outcome = %v, want no-sources retry", outcome)
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

func TestBetterOutcome(t *testing.T) {
	cases := []struct {
		a, b EventWaitOutcome
		want EventWaitOutcome
	}{
		{EventWaitTimeout, EventWaitTimeout, EventWaitTimeout},
		{EventWaitTimeout, EventWaitReaderFailure, EventWaitReaderFailure},
		{EventWaitReaderFailure, EventWaitUnavailable, EventWaitReaderFailure}, // equal rank: first wins
		{EventWaitUnavailable, EventWaitUnsupported, EventWaitUnsupported},
		{EventWaitUnsupported, EventWaitProtocolMismatch, EventWaitUnsupported}, // equal rank: first wins
		{EventWaitProtocolMismatch, EventWaitReaderFailure, EventWaitProtocolMismatch},
	}
	for _, tc := range cases {
		if got := betterOutcome(tc.a, tc.b); got != tc.want {
			t.Errorf("betterOutcome(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestEventLane_EventToReprobe runs the FULL watcher loop (run) with a fake
// probe, a never-firing ticker, and a scripted event source: a validated
// signal must trigger an immediate re-probe cycle, and the probe must be
// invoked with the exact bound endpoint's meta. The ticker never fires, so any
// probe invocation is attributable to the event lane (event-to-reprobe order).
func TestEventLane_EventToReprobe(t *testing.T) {
	homeDir := t.TempDir()
	mustWriteMeta(t, homeDir, "task-1", map[string]string{"backend": "herdr", "window": "w:p", "home": homeDir})

	sig := backend.ObservationSignal{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Activity: backend.ActivityIdle,
		Source:   backend.SourceEvent,
		Cursor:   "1",
	}
	src := &fakeEventSource{signals: []backend.ObservationSignal{sig}}
	port := fakeEventPort{sources: []EndpointSource{{Endpoint: sig.Endpoint, Source: src}}}

	// Probe records every invocation with its meta (exact binding evidence).
	var probeMu sync.Mutex
	var probeCalls []map[string]string
	probe := countingProbe{fn: func(_ string, meta map[string]string) (bool, error) {
		probeMu.Lock()
		defer probeMu.Unlock()
		probeCalls = append(probeCalls, meta)
		return true, nil
	}}

	// Never-firing ticker: only the event lane can drive a cycle, so any probe
	// invocation proves event-to-reprobe.
	neverTicker := func(time.Duration) *time.Ticker { return time.NewTicker(time.Hour) }
	sigCh := make(chan os.Signal)
	done := make(chan *WakeReason, 1)
	go func() {
		reason, _ := run(homeDir, neverTicker, sigCh, probe, raceCycleSender{}, raceTestHooks{}, NoopRetirementPort{}, nil, acceptingCheckValidationPort{}, raceTaskStatePort{}, port, NoopDeliveryRecoveryPort{})
		done <- reason
	}()

	// The event lane must deliver the signal and trigger an immediate re-probe
	// cycle: the probe is invoked with the exact bound endpoint's meta.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		probeMu.Lock()
		n := len(probeCalls)
		probeMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	probeMu.Lock()
	calls := probeCalls
	probeMu.Unlock()
	if len(calls) == 0 {
		t.Fatal("event did not trigger a probe cycle within 3s")
	}
	foundExact := false
	for _, meta := range calls {
		if meta["window"] == "w:p" && meta["backend"] == "herdr" {
			foundExact = true
		}
	}
	if !foundExact {
		t.Errorf("probe never invoked with the exact bound endpoint meta; got %v", calls)
	}
	if src.waitCount() == 0 {
		t.Error("event source was never waited on")
	}

	// Stop the watcher and drain the lane.
	close(sigCh)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not stop after signal")
	}
}

// TestEventLane_RetriesUntilSourceAppears verifies a watcher that starts
// before any endpoint is bound keeps its native-event lane alive and delivers
// once the backend source becomes available.
func TestEventLane_RetriesUntilSourceAppears(t *testing.T) {
	homeDir := t.TempDir()
	sig := backend.ObservationSignal{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Activity: backend.ActivityIdle,
		Source:   backend.SourceEvent,
		Cursor:   "1",
	}
	src := &fakeEventSource{signals: []backend.ObservationSignal{sig}}
	port := &sequenceEventPort{stages: [][]EndpointSource{
		nil,
		{{Endpoint: sig.Endpoint, Source: src}},
	}}
	stopLane := make(chan struct{})
	pulses := startEventLane(homeDir, NewEventWaiter(port), stopLane)

	select {
	case pulse, ok := <-pulses:
		if !ok {
			t.Fatal("event lane closed before source appeared")
		}
		if pulse.outcome != EventWaitSignal || pulse.signal != sig {
			t.Fatalf("pulse = %+v, want signal %+v", pulse, sig)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("event lane did not deliver after source appeared")
	}
	close(stopLane)
}

// TestEventLane_ClosesOnNoEventSurface verifies a genuine unsupported or
// protocol-mismatch outcome closes the lane for good. A valid signal is queued
// behind the terminal error, so a lane that retried would deliver it.
func TestEventLane_ClosesOnNoEventSurface(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"unsupported", backend.ErrEventUnsupported},
		{"protocol-mismatch", backend.ErrEventProtocolMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig := backend.ObservationSignal{
				Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
				Activity: backend.ActivityIdle,
				Source:   backend.SourceEvent,
				Cursor:   "1",
			}
			src := &fakeEventSource{errors: []error{tc.err}, signals: []backend.ObservationSignal{sig}}
			port := staticEventPort(EndpointSource{Endpoint: sig.Endpoint, Source: src})
			stopLane := make(chan struct{})
			defer close(stopLane)
			pulses := startEventLane(t.TempDir(), NewEventWaiter(port), stopLane)

			select {
			case p, ok := <-pulses:
				if ok {
					t.Fatalf("lane retried after %s and delivered %+v; want closed", tc.name, p)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("lane did not close after %s", tc.name)
			}
			if got := src.waitCount(); got != 1 {
				t.Fatalf("waits = %d, want 1", got)
			}
		})
	}
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
	// close nor emit. The retry's second Wait bounds that claim: the lane is
	// blocked in it, so anything it emitted or a close is already observable.
	deadline := time.After(5 * time.Second)
	for src.waitCount() < 2 {
		select {
		case p, ok := <-pulses:
			if ok {
				t.Fatalf("unexpected pulse: %v", p.outcome)
			}
			t.Fatal("lane closed on transient reader failure; watcher must keep polling")
		case <-deadline:
			t.Fatalf("waits = %d, want a retry after reader failure", src.waitCount())
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case p, ok := <-pulses:
		if ok {
			t.Fatalf("unexpected pulse: %v", p.outcome)
		}
		t.Fatal("lane closed on transient reader failure; watcher must keep polling")
	default:
	}
	drainLane(t, pulses, stopLane)
}

// TestEventLane_RaceWatcherAndRecovery is the race lane: an event reader
// (event lane goroutine) + a polling watcher (ticker cycles) + concurrent
// recovery (parallel cycles) all run against the same home. Run under -race,
// it must not race on cursor state or wake markers.
func TestEventLane_RaceWatcherAndRecovery(t *testing.T) {
	homeDir := t.TempDir()
	mustWriteMeta(t, homeDir, "task-1", map[string]string{"backend": "herdr", "window": "w:p", "home": homeDir})
	mustWriteMeta(t, homeDir, "task-2", map[string]string{"backend": "herdr", "window": "w:q", "home": homeDir})

	// Event reader: emits a fresh-cursor signal each wait so the lane keeps
	// delivering wake hints concurrently with polling.
	src := &raceEventSource{}
	port := fakeEventPort{sources: []EndpointSource{
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Source: src},
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:q"}, Source: src},
	}}

	probe := countingProbe{fn: func(string, map[string]string) (bool, error) { return true, nil }}
	sender := raceCycleSender{}
	hooks := raceTestHooks{}
	states := raceTaskStatePort{}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var pulseCount, cycleCount atomic.Int64

	// Event reader lane.
	wg.Add(1)
	go func() {
		defer wg.Done()
		stopLane := make(chan struct{})
		pulses := startEventLane(homeDir, NewEventWaiter(port), stopLane)
		for {
			select {
			case <-stop:
				close(stopLane)
				return
			case _, ok := <-pulses:
				if !ok {
					close(stopLane)
					return
				}
				pulseCount.Add(1)
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
				if _, err := RunCycleWithProbeAndSender(homeDir, probe, sender, hooks, NoopRetirementPort{}, nil, acceptingCheckValidationPort{}, states); err != nil {
					return
				}
				cycleCount.Add(1)
			}
		}()
	}

	// Stop once the lane and the cycles have demonstrably overlapped: neither
	// side stops before stop closes, so both counts grew concurrently.
	deadline := time.Now().Add(10 * time.Second)
	for pulseCount.Load() < 20 || cycleCount.Load() < 50 {
		if time.Now().After(deadline) {
			close(stop)
			wg.Wait()
			t.Fatalf("no overlap: %d event pulses, %d cycles; want >= 20 and >= 50", pulseCount.Load(), cycleCount.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
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
func (raceCycleSender) Send(string, map[string]string, string) home.BoundSendResult {
	return home.BoundSendResult{}
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
	metaPath, err := home.MetaFilePath(homeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, b, 0644); err != nil {
		t.Fatal(err)
	}
}
