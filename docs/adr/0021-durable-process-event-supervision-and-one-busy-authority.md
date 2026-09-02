# 0021. Durable Process-Event Supervision and One Busy Authority

* **Status:** Accepted
* **Date:** 2026-08-31
* **Extends:** ADR-0005 (mutation fencing / typed endpoint observation), ADR-0008 (task-authority owns lifecycle), ADR-0019 (single-binary hand-rolled store)
* **Triggered by:** firstmate → munsu parity refresh (2026-07-21 baseline → 2026-08-31), gaps G1/G2/G3; port-mapping P1b row ("Herdr native busy/event transport")

## Context

munsu supervises endpoints by polling. The watcher in
`internal/orchestrator/supervision_watcher.go` runs a fixed-interval loop and,
for one narrow case — a merged PR check — already implements a condition→action
shape: `DiscoverAllChecks` enumerates durable check plugins and
`RetireMergedPoll` acts once the external condition (PR merged) is observed,
with crash-safe ordering (record before publication, poll-removal last). That
lane is the only place munsu waits on an external, out-of-band condition without
holding an agent turn.

firstmate generalized this pattern along two axes munsu has not:

1. **Process-event supervision (G1/G2):** a first-class, durable source that
   waits on an arbitrary blocking external process or condition, captures its
   result durably *before* announcing, and wakes an agent at most once — instead
   of spending one agent turn per re-check. Re-announcement across restarts is
   gated by a generation-keyed acknowledgement.

2. **A single busy authority (G3):** one owner that answers "is this endpoint
   busy / idle / unknown / dead" and that every consumer routes through.

On axis 2 munsu is further along than it looks, and this is the load-bearing
detail. The typed observation model already exists:
`internal/backend/endpoint_observation.go` defines orthogonal axes
`LifecycleState`, `Responsiveness`, `Freshness`, and `Activity` on
`EndpointObservation`. But `Activity` was **producer-only** when this ADR was
written — nothing in the dispatch path consumed it — though the fleet busy
authority now derives its reading from `Activity` and `DispatchWake` routes
through that authority, so it is consumed. Meanwhile a *second*, coarser
`EndpointObservationState` enum lived in
`internal/orchestrator/wake_dispatch.go` and gated dispatch on its own
heuristic until it was retired. Two overlapping representations of the same question violated munsu's
"one live contract" doctrine.

## Evidence

* `internal/backend/endpoint_observation.go` — `Activity` axis (`type Activity
  uint8` at line 122) with an unknown state; `EndpointObservation` carries it,
  but no fleet decision reads it.
* `internal/orchestrator/wake_dispatch.go` — a duplicate
  `EndpointObservationState` enum (line 41) drives dispatch independently.
* `internal/orchestrator/supervision_check.go` `DiscoverAllChecks` +
  `internal/fleet/retirement_poll.go` `RetireMergedPoll` — the existing
  crash-safe condition→action lane, specialized to merged-PR retirement.
* `internal/home/watcher_state.go` `EnqueueWake` / `DrainWakes` — the durable,
  lease-fenced wake queue that is the only wake-delivery substrate (ADR-0015).
* BEO-16 / P1a invariant: `unknown != idle != dead`; adapters never conclude
  liveness or absence — only Fleet authorizes `Live()` / `Absent()`.

## Decision

### 1. One busy authority that consumes the existing `Activity` axis

Establish a single fleet-side owner for the busy/idle/unknown/dead question. It
consumes the already-typed `Activity` on `EndpointObservation`; it does not
introduce a parallel representation. The coarse `EndpointObservationState` in
`internal/orchestrator/wake_dispatch.go` is **retired** and its dispatch gate
re-expressed against the single authority — a straight consolidation onto one
live contract, not a new abstraction.

The authority preserves the P1a invariants verbatim: `unknown` is never folded
into `idle`; only endpoint death (a Fleet-authorized `Absent()`) overrides a
busy reading; adapters remain observation producers and never conclude
liveness. "Unknown" propagates as unknown to every consumer.

### 2. Generalize the condition→action lane into a durable process-event source

Promote the merged-PR poll lane from a retirement special case to a
domain-neutral process-event source: register a (condition, action) pair; the
watcher evaluates the condition on its existing interval; the action runs at
most once when the condition is stably true. The load-bearing durability rule,
carried over from `RetireMergedPoll`'s crash ordering and firstmate's
process-event contract, is fixed here:

* The condition's result is captured durably **before** any wake is enqueued.
* A generation-keyed acknowledgement is the *only* thing that stops
  re-announcement; a restart before acknowledgement re-delivers, a restart after
  it does not.

This reuses the durable wake queue (`EnqueueWake` / `DrainWakes`) as the sole
delivery substrate. It introduces no new storage technology (ADR-0019); the
process-event registry and fired-markers are files under the same store.

### 3. Fire-once semantics on stable-true

A registered condition→action fires exactly once per registration, on the first
stably-true observation, recorded by a durable fired-marker. Re-evaluation after
firing is a no-op until the registration is cleared. This is what lets the
watcher wait on a blocking external process across many poll cycles while
spending zero agent turns until the single wake.

### Non-goals / boundaries

* No new store, daemon, or transport — this rides the existing watcher loop,
  wake queue, and hand-rolled store.
* Lifecycle authority stays in `internal/taskauthority` (ADR-0008); this ADR
  adds an observation *consumer* and a supervision *source*, not a new authority
  over task state.
* The interactive board (firstmate #2659) and herdr-native event transport
  remain downstream of this foundation and are out of scope here.
