# Spec: process-event-source

Module id: `process-event-source` · Map: `CAPABILITY-MAP.md` · ADR: 0021 Decision 2

## Objective

Let munsu wait on an arbitrary blocking external process/condition **without
holding an agent turn**, and wake an agent at most once when it resolves. Today
the only such lane is a retirement special case: `orchestrator.DiscoverAllChecks`
enumerates durable check plugins and `fleet.RetireMergedPoll` acts once a PR is
observed merged, with crash-safe ordering (record before publication,
poll-removal last). Generalize that into a domain-neutral, durable process-event
source.

Success = a caller can register a durable process-event whose external result is
captured **before** any wake is enqueued, and where a generation-keyed
acknowledgement is the *only* thing that stops re-announcement — so a restart
before ack re-delivers, a restart after ack does not. Zero agent turns are spent
per poll cycle; exactly one wake is spent on resolution.

Who benefits: any supervision that must block on out-of-band work (the merged-PR
retirement becomes one instance), and `condition-action` (built on top).

## Tech Stack

Go (module `munsu`), stdlib only. Rides the existing watcher loop and durable
wake queue. No new storage tech (ADR-0019): the process-event registry and
result records are files under the existing hand-rolled store.

## Commands

```sh
go build ./...
go vet ./...
go test ./internal/orchestrator/... ./internal/fleet/... ./internal/home/...
go test -race ./internal/home/...     # queue/lease concurrency
go test ./...                          # full gate before delivery
gofmt -l .
```

## Project Structure

```
internal/orchestrator/supervision_check.go   → DiscoverAllChecks (the lane to generalize)
internal/fleet/retirement_poll.go            → RetireMergedPoll (crash-safe ordering to preserve, now one instance)
internal/home/watcher_state.go               → EnqueueWake / DrainWakes (the sole delivery substrate)
internal/orchestrator/supervision_watcher.go → the fixed-interval loop that evaluates registered events
<new>                                         → domain-neutral process-event source + durable registry — home decided in Plan
```

## Code Style

Match `retirement_poll.go`'s crash ordering: capture/record the durable result,
then publish, then remove the poll marker — never reorder. Ack is generation-keyed:

```go
// Durable order (must not be reordered):
//  1. capture external result → record durably
//  2. enqueue wake (home.EnqueueWake)
//  3. only a matching-generation ack clears re-announcement
if rec.Generation == ackedGeneration {
    return // already announced+acked; no re-wake
}
```

## Testing Strategy

Go `testing`; `-race` for the queue/lease interactions. Reuse the crash-injection
style already in `retirement_poll_test.go` (`TestRetireMergedPoll_CrashBefore…`).
Cover the full crash matrix:
- crash **after capture, before wake** → wake re-delivered on restart (at-least-once).
- crash **after wake, before ack** → re-delivered (ack is the only stop).
- crash **after ack** → not re-delivered (at-most-once past ack).
- generation mismatch → old generation's ack does not suppress a new registration.
- one poll cycle over an unresolved condition spends **zero** wakes.

Guard-coverage: the "capture-before-wake refused/reordered" guard and the
generation-mismatch branch must each be entered by a test.

## Boundaries

- **Always:** capture the result before enqueuing any wake; keep the wake queue + lease as the only delivery path; preserve `RetireMergedPoll`'s crash ordering when it becomes an instance of the generic source.
- **Ask first:** changing the check-plugin on-disk format; changing wake-queue record shape in `home`.
- **Never:** wake before durable capture; add a second delivery substrate; add storage tech; spend an agent turn per re-check.

## Success Criteria

1. A domain-neutral process-event source exists; merged-PR retirement is
   re-expressed as one instance of it (no parallel bespoke lane left behind).
2. Result is durably captured before any wake; generation-keyed ack is the sole
   re-announcement stop; the crash matrix above passes under `-race`.
3. An unresolved condition consumes zero agent turns across poll cycles; a
   resolved one consumes exactly one wake.
4. `go test ./...`, `-race` home lane, `go vet`, `gofmt -l .` clean; deadcode
   and guards green.

## Open Questions

- Registry home + record schema: decided in `internal/orchestrator/process_event_source.go`
  (flat `v1-*.json` records under `home.StateDir`/`.process-events`; no new storage tech, ADR-0019).
- Does the generic source subsume `RetireMergedPoll` entirely, or wrap it?
  (Prefer subsume — one live contract — unless a bounded constraint blocks it.)
