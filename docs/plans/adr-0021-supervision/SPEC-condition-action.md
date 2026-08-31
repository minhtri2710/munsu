# Spec: condition-action

Module id: `condition-action` · Map: `CAPABILITY-MAP.md` · ADR: 0021 Decision 3
Depends on: `process-event-source`

## Objective

Let a caller register a **(condition, action)** pair that fires the action
**exactly once**, on the first stably-true observation of the condition, and
never again until the registration is cleared. This turns the durable
process-event source into a declarative watcher: instead of one agent turn per
re-check, the watcher evaluates the condition on its existing interval and
spends a single wake when it resolves.

Success = a registration fires once on stable-true (recorded by a durable
fired-marker), re-evaluation after firing is a no-op, and a restart never
double-fires an already-fired registration.

Who benefits: every supervision rule that is naturally "when X becomes true, do
Y once" (retirement, readiness gating, later the interactive board). It is the
ergonomic surface over `process-event-source`.

## Tech Stack

Go (module `munsu`), stdlib only. Built directly on `process-event-source`'s
durable registry and the watcher loop. No new storage tech (ADR-0019).

## Commands

```sh
go build ./...
go vet ./...
go test ./internal/orchestrator/...
go test -race ./internal/orchestrator/... ./internal/home/...
go test ./...
gofmt -l .
```

## Project Structure

```
internal/orchestrator/supervision_watcher.go → interval loop that evaluates registered conditions
<process-event-source new file>              → durable registry this module registers against
<new>                                         → condition→action registration + fired-marker semantics
```

## Code Style

Match the watcher's existing evaluate-on-tick shape. Fire-once is a durable
marker check, not an in-memory flag:

```go
// stable-true fires once; the fired-marker is durable so a restart never re-fires.
if cond.StablyTrue(obs) && !reg.HasFiredMarker() {
    reg.RunAction()          // via process-event-source: capture-before-wake
    reg.WriteFiredMarker()   // durable; re-eval after this is a no-op
}
```

## Testing Strategy

Go `testing`, `-race` for registry/queue interaction. Cover:
- fires exactly once on first stable-true; a second true tick is a no-op.
- transient true→false→true before "stable" does not fire early (define
  stability in Plan — e.g. N consecutive observations or a single confirmed edge).
- restart after fired-marker written → no re-fire.
- restart after action ran but before fired-marker written → inherits
  `process-event-source`'s at-least-once (re-fire allowed, ack/marker is the stop).
- clearing a registration allows a fresh fire.

Guard-coverage: the "already-fired → no-op" refusal and the "not-yet-stable →
do not fire" branch must each be entered by a test.

## Boundaries

- **Always:** durable fired-marker before treating a registration as fired; delegate delivery to `process-event-source` (capture-before-wake, wake queue).
- **Ask first:** the definition of "stable" (edge vs N-consecutive); registration record schema.
- **Never:** hold fire-once state only in memory; bypass `process-event-source` for delivery; add storage tech; spend a turn per re-check.

## Success Criteria

1. A registration fires its action exactly once on first stable-true; later true
   ticks are no-ops.
2. Fire-once survives restart via a durable fired-marker (no double-fire).
3. Delivery goes through `process-event-source` (capture-before-wake, single
   wake, generation-keyed ack).
4. `go test ./...`, `-race`, `go vet`, `gofmt -l .` clean; deadcode and guards
   green.

## Open Questions

- Stability definition: single confirmed edge vs N consecutive observations.
  Decide in Plan; affects the transient-flap test.
- Does a fired registration auto-clear or require explicit clearing? (Prefer
  explicit; auto-clear only if a concrete rule needs re-arming.)
