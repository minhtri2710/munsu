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

// sequenceEventPort returns a different source set per Sources call, in order
// (used to simulate an incarnation rollover between waits: same endpoint,
// new expected incarnation).
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
}

func (b *blockingEventSource) Wait(ctx context.Context, endpoint backend.EndpointRef, after backend.EventCursor) (backend.ObservationSignal, error) {
	select {
	case <-ctx.Done():
		return backend.ObservationSignal{}, ctx.Err()
	case <-b.release:
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
	src := &blockingEventSource{release: make(chan struct{})}
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

	// Let A's wait start, then caller B with a short bound on the same
	// endpoint must return within its own bound.
	time.Sleep(20 * time.Millisecond)
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

// TestEventWaiter_WaitForSignal_OverlappingIncarnationWaits is the blocker
// 1f11ad5e race regression: an OLD in-flight waiter (captured its generation
// before the blocking Source.Wait) returns AFTER a NEW incarnation already
// committed cursor 1. Its stale result must be rejected at compare-and-advance
// (generation mismatch) and must NOT roll the state back to the old
// incarnation, so the next event of the new stream (cursor 2) is still
// accepted.
func TestEventWaiter_WaitForSignal_OverlappingIncarnationWaits(t *testing.T) {
	ep := backend.EndpointRef{Backend: "herdr", Handle: "w:p"}

	oldSrc := &gatedEventSource{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		sig: backend.ObservationSignal{
			Endpoint:    ep,
			Incarnation: "inc-old",
			Activity:    backend.ActivityIdle,
			Source:      backend.SourceEvent,
			Cursor:      "43",
		},
	}
	newSrc := &fakeEventSource{signals: []backend.ObservationSignal{
		{Endpoint: ep, Incarnation: "inc-new", Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "1"},
		{Endpoint: ep, Incarnation: "inc-new", Activity: backend.ActivityIdle, Source: backend.SourceEvent, Cursor: "2"},
	}}
	w := NewEventWaiter(staticEventPort(
		EndpointSource{Endpoint: ep, Incarnation: "inc-old", Source: oldSrc},
		EndpointSource{Endpoint: ep, Incarnation: "inc-new", Source: newSrc},
	))

	// Old waiter A starts and blocks inside Source.Wait (its generation was
	// captured before Wait returned control).
	doneOld := make(chan struct{})
	var oldOutcome EventWaitOutcome
	go func() {
		defer close(doneOld)
		_, oldOutcome = w.waitEndpoint(context.Background(), EndpointSource{Endpoint: ep, Incarnation: "inc-old", Source: oldSrc})
	}()
	<-oldSrc.entered

	// Rollover: the new incarnation commits cursor 1.
	sig, outcome := w.waitEndpoint(context.Background(), EndpointSource{Endpoint: ep, Incarnation: "inc-new", Source: newSrc})
	if outcome != EventWaitSignal || sig.Cursor != "1" {
		t.Fatalf("new incarnation commit = (outcome %v, cursor %q), want (signal, %q)", outcome, sig.Cursor, "1")
	}

	// Old waiter A finally returns cursor 43: stale generation — rejected, and
	// the state must not be rolled back to inc-old/43.
	close(oldSrc.release)
	<-doneOld
	if oldOutcome != EventWaitTimeout {
		t.Fatalf("old in-flight result outcome = %v, want timeout (stale generation rejected)", oldOutcome)
	}

	// The next event of the new stream (cursor 2) is still accepted.
	sig, outcome = w.waitEndpoint(context.Background(), EndpointSource{Endpoint: ep, Incarnation: "inc-new", Source: newSrc})
	if outcome != EventWaitSignal || sig.Cursor != "2" {
		t.Fatalf("cursor 2 after rejected rollback = (outcome %v, cursor %q), want (signal, %q)", outcome, sig.Cursor, "2")
	}
}

// TestEventWaiter_WaitForSignal_ConcurrentSameIncarnationOrderedEvents is the
// blocker bebbc556 regression: two SAME-incarnation waits on the same endpoint
// return distinct, ordered cursors (1 then 2) and BOTH must be accepted. The
// rollover generation advances only on an incarnation change, so a concurrent
// same-incarnation cursor advance is not mistaken for a stale rollover result;
// the adapter-owned After compare-and-record accepts cursor 2 after cursor 1.
func TestEventWaiter_WaitForSignal_ConcurrentSameIncarnationOrderedEvents(t *testing.T) {
	ep := backend.EndpointRef{Backend: "herdr", Handle: "w:p"}
	srcA := &gatedEventSource{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		sig: backend.ObservationSignal{
			Endpoint:    ep,
			Incarnation: "inc-new",
			Activity:    backend.ActivityBusy,
			Source:      backend.SourceEvent,
			Cursor:      "1",
		},
	}
	srcB := &gatedEventSource{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		sig: backend.ObservationSignal{
			Endpoint:    ep,
			Incarnation: "inc-new",
			Activity:    backend.ActivityIdle,
			Source:      backend.SourceEvent,
			Cursor:      "2",
		},
	}
	es := func(src ObservationEventSource) EndpointSource {
		return EndpointSource{Endpoint: ep, Incarnation: "inc-new", Source: src}
	}
	w := NewEventWaiter(staticEventPort(es(srcA), es(srcB)))

	// Both same-incarnation waits start and block inside Source.Wait (their
	// generations are captured before Wait returned control).
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
		t.Fatalf("first same-incarnation wait = (outcome %v, cursor %q), want (signal, %q)", rA.outcome, rA.sig.Cursor, "1")
	}

	// Release B: cursor 2 is a valid ordered event of the SAME incarnation and
	// must ALSO be accepted (not rejected as a stale rollover result).
	close(srcB.release)
	rB := <-doneB
	if rB.outcome != EventWaitSignal || rB.sig.Cursor != "2" {
		t.Fatalf("second same-incarnation wait = (outcome %v, cursor %q), want (signal, %q)", rB.outcome, rB.sig.Cursor, "2")
	}
}

// TestEventWaiter_WaitForSignal_SeededSnapshotRolloverRejectsStale is the
// blocker ce0cded2 regression: cursor, recorded incarnation, and rollover
// generation are captured in ONE atomic pre-wait snapshot, so an old in-flight
// waiter can never read the old cursor together with the new generation (a
// TOCTOU that would defeat the stale guard). The test seeds an old cursor 42
// under inc-old, starts an old waiter that snapshots and blocks, commits a
// rollover (new cursor 1), then releases the old waiter (returns cursor 43):
// the stale result must be rejected and the new stream's next event (cursor 2)
// must still be accepted.
func TestEventWaiter_WaitForSignal_SeededSnapshotRolloverRejectsStale(t *testing.T) {
	ep := backend.EndpointRef{Backend: "herdr", Handle: "w:p"}

	seedSrc := &fakeEventSource{signals: []backend.ObservationSignal{
		{Endpoint: ep, Incarnation: "inc-old", Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "42"},
	}}
	oldSrc := &gatedEventSource{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		sig: backend.ObservationSignal{
			Endpoint:    ep,
			Incarnation: "inc-old",
			Activity:    backend.ActivityIdle,
			Source:      backend.SourceEvent,
			Cursor:      "43",
		},
	}
	newSrc := &fakeEventSource{signals: []backend.ObservationSignal{
		{Endpoint: ep, Incarnation: "inc-new", Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "1"},
		{Endpoint: ep, Incarnation: "inc-new", Activity: backend.ActivityIdle, Source: backend.SourceEvent, Cursor: "2"},
	}}
	w := NewEventWaiter(staticEventPort(
		EndpointSource{Endpoint: ep, Incarnation: "inc-old", Source: seedSrc},
		EndpointSource{Endpoint: ep, Incarnation: "inc-old", Source: oldSrc},
		EndpointSource{Endpoint: ep, Incarnation: "inc-new", Source: newSrc},
	))
	esOld := EndpointSource{Endpoint: ep, Incarnation: "inc-old", Source: oldSrc}
	esNew := EndpointSource{Endpoint: ep, Incarnation: "inc-new", Source: newSrc}

	// Seed: old incarnation commits cursor 42 (state {inc-old, 42, gen 1}).
	_, outcome := w.waitEndpoint(context.Background(), EndpointSource{Endpoint: ep, Incarnation: "inc-old", Source: seedSrc})
	if outcome != EventWaitSignal {
		t.Fatalf("seed outcome = %v, want signal", outcome)
	}

	// Old waiter A starts, captures its atomic snapshot (after=42, inc-old,
	// gen 1), then blocks inside Source.Wait.
	doneOld := make(chan struct{})
	var oldOutcome EventWaitOutcome
	go func() {
		defer close(doneOld)
		_, oldOutcome = w.waitEndpoint(context.Background(), esOld)
	}()
	<-oldSrc.entered

	// Rollover: new incarnation commits cursor 1.
	sig, outcome := w.waitEndpoint(context.Background(), esNew)
	if outcome != EventWaitSignal || sig.Cursor != "1" {
		t.Fatalf("rollover commit = (outcome %v, cursor %q), want (signal, %q)", outcome, sig.Cursor, "1")
	}

	// Old waiter A returns cursor 43: its snapshot predates the rollover
	// (generation advanced), so the result is stale — rejected WITHOUT rolling
	// state back to inc-old.
	close(oldSrc.release)
	<-doneOld
	if oldOutcome != EventWaitTimeout {
		t.Fatalf("old in-flight result outcome = %v, want timeout (stale snapshot generation rejected)", oldOutcome)
	}

	// The new stream's next event (cursor 2) is still accepted.
	sig, outcome = w.waitEndpoint(context.Background(), esNew)
	if outcome != EventWaitSignal || sig.Cursor != "2" {
		t.Fatalf("cursor 2 after rejected rollback = (outcome %v, cursor %q), want (signal, %q)", outcome, sig.Cursor, "2")
	}
}

// TestEventWaiter_SnapshotCursorStateAtomic verifies the single pre-wait
// snapshot returns cursor, recorded incarnation, and rollover generation as
// one consistent view (no split read), and that the 'after' hint is
// incarnation-scoped.
func TestEventWaiter_SnapshotCursorStateAtomic(t *testing.T) {
	ep := backend.EndpointRef{Backend: "herdr", Handle: "w:p"}
	w := NewEventWaiter(staticEventPort())

	// No state yet: empty snapshot.
	snap := w.snapshotCursorState(EndpointSource{Endpoint: ep, Incarnation: "inc-old"})
	if snap.after != "" || snap.incarnation != "" || snap.generation != 0 {
		t.Fatalf("empty-state snapshot = %+v, want {after:\"\", inc:\"\", gen:0}", snap)
	}

	// Seed: old incarnation commits cursor 42 (generation advances to 1).
	w.recordCursor(EndpointSource{Endpoint: ep, Incarnation: "inc-old"}, "42")
	snap = w.snapshotCursorState(EndpointSource{Endpoint: ep, Incarnation: "inc-old"})
	if snap.after != "42" || snap.incarnation != "inc-old" || snap.generation != 1 {
		t.Fatalf("old-state snapshot = %+v, want {after:42, inc:inc-old, gen:1}", snap)
	}

	// Same-incarnation cursor advance leaves the rollover generation stable
	// (cursor 43, still inc-old, gen 1) — the token changes ONLY on rollover.
	w.recordCursor(EndpointSource{Endpoint: ep, Incarnation: "inc-old"}, "43")
	snap = w.snapshotCursorState(EndpointSource{Endpoint: ep, Incarnation: "inc-old"})
	if snap.after != "43" || snap.incarnation != "inc-old" || snap.generation != 1 {
		t.Fatalf("advanced same-incarnation snapshot = %+v, want {after:43, inc:inc-old, gen:1}", snap)
	}

	// Rollover: new incarnation commits cursor 1; generation advances and the
	// old-incarnation waiter sees a fresh 'after' hint.
	w.recordCursor(EndpointSource{Endpoint: ep, Incarnation: "inc-new"}, "1")
	snap = w.snapshotCursorState(EndpointSource{Endpoint: ep, Incarnation: "inc-old"})
	if snap.after != "" || snap.incarnation != "inc-new" || snap.generation != 2 {
		t.Fatalf("post-rollover snapshot (old es) = %+v, want {after:\"\", inc:inc-new, gen:2}", snap)
	}
	snap = w.snapshotCursorState(EndpointSource{Endpoint: ep, Incarnation: "inc-new"})
	if snap.after != "1" || snap.incarnation != "inc-new" || snap.generation != 2 {
		t.Fatalf("post-rollover snapshot (new es) = %+v, want {after:1, inc:inc-new, gen:2}", snap)
	}
}

// TestEventWaiter_WaitForSignal_MultiEndpointWake is the blocker-3
// regression: endpoint A blocks for its whole budget while endpoint B has a
// signal ready. The fan-out must deliver B's signal promptly instead of
// starving B behind A's blocking wait.
func TestEventWaiter_WaitForSignal_MultiEndpointWake(t *testing.T) {
	home := t.TempDir()
	blockSrc := &blockingEventSource{release: make(chan struct{})}
	sigSrc := &fakeEventSource{signals: []backend.ObservationSignal{
		{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:q"}, Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "1"},
	}}
	port := staticEventPort(
		EndpointSource{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"}, Source: blockSrc},
		EndpointSource{Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:q"}, Source: sigSrc},
	)
	w := NewEventWaiter(port)

	start := time.Now()
	sig, outcome := w.WaitForSignal(context.Background(), home, 300*time.Millisecond)
	elapsed := time.Since(start)
	if outcome != EventWaitSignal {
		t.Fatalf("outcome = %v, want signal from B", outcome)
	}
	if sig.Endpoint.Handle != "w:q" {
		t.Errorf("signal endpoint = %+v, want w:q", sig.Endpoint)
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("B's signal was delayed by A's blocking wait: elapsed %v", elapsed)
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

// TestEventWaiter_WaitForSignal_IncarnationRolloverResetsCursor is the
// regression for blocker 3caa8122: cursor state must be scoped to the expected
// incarnation, so a rollover that reuses the same backend/handle with a fresh
// stream restarting at a LOWER cursor is not suppressed by stale state from
// the previous incarnation.
func TestEventWaiter_WaitForSignal_IncarnationRolloverResetsCursor(t *testing.T) {
	ep := backend.EndpointRef{Backend: "herdr", Handle: "w:p"}
	// Old incarnation stream consumed up to cursor 42; the new incarnation
	// restarts at cursor 1 (a reset stream). Without incarnation-aware cursor
	// state, After("1", "42") = false and every new event would be
	// suppressed until the cursor exceeds 42.
	src := &fakeEventSource{signals: []backend.ObservationSignal{
		{Endpoint: ep, Activity: backend.ActivityIdle, Source: backend.SourceEvent, Cursor: "42"},
		{Endpoint: ep, Activity: backend.ActivityBusy, Source: backend.SourceEvent, Cursor: "1"},
	}}
	port := &sequenceEventPort{stages: [][]EndpointSource{
		{{Endpoint: ep, Incarnation: "inc-old", Source: src}},
		{{Endpoint: ep, Incarnation: "inc-new", Source: src}},
	}}
	w := NewEventWaiter(port)

	// First wait: old incarnation, cursor 42 accepted and recorded.
	_, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second)
	if outcome != EventWaitSignal {
		t.Fatalf("old-incarnation outcome = %v, want signal", outcome)
	}
	// Second wait: same backend/handle but the expected incarnation changed
	// (rollover); cursor 1 of the fresh stream must be accepted, not
	// suppressed by the recorded 42.
	sig, outcome := w.WaitForSignal(context.Background(), t.TempDir(), time.Second)
	if outcome != EventWaitSignal {
		t.Fatalf("rollover outcome = %v, want signal (cursor reset on incarnation change); got signal=%+v", outcome, sig)
	}
	if sig.Cursor != "1" {
		t.Errorf("rollover signal cursor = %q, want %q", sig.Cursor, "1")
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
	mustWriteMeta(t, homeDir, "task-1", map[string]string{"backend": "herdr", "window": "w:p", "endpoint_incarnation": "inc-1", "home": homeDir})

	sig := backend.ObservationSignal{
		Endpoint: backend.EndpointRef{Backend: "herdr", Handle: "w:p"},
		Activity: backend.ActivityIdle,
		Source:   backend.SourceEvent,
		Cursor:   "1",
	}
	src := &fakeEventSource{signals: []backend.ObservationSignal{sig}}
	port := fakeEventPort{sources: []EndpointSource{{Endpoint: sig.Endpoint, Incarnation: "inc-1", Source: src}}}

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
		reason, _ := run(homeDir, neverTicker, sigCh, probe, raceCycleSender{}, raceTestHooks{}, NoopRetirementPort{}, raceTaskStatePort{}, port)
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
