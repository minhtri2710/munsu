# Spec: busy-authority

Module id: `busy-authority` · Map: `CAPABILITY-MAP.md` · ADR: 0021 Decision 1

## Objective

Give munsu **one** fleet-side owner of the "is this endpoint busy / idle /
unknown / dead" question, and route every consumer through it.

The typed `backend.Activity` axis (`busy`/`idle`/`blocked`/`unknown`) is carried
on the canonical `backend.EndpointObservation` but is **not yet read by any
dispatch decision** — Activity-aware gating is the A3 follow-up. Until A2, the
orchestrator also re-declared a *second*, coarse `EndpointObservationState`
enum plus an `EndpointObservation{State, Detail}` struct that `DispatchWake`
gated on. A2 retired that duplicate: `DispatchWake` and `ProbePort.Probe` now
consume `backend.EndpointObservation` directly and gate on its derived `State()`
over the same seven coarse outcomes (alive / starting / unresponsive / dead /
unknown / stale-identity / unresolved), so every per-state dispatch decision is
unchanged. The remaining gap is the single authority itself (A1) and routing the
gate through it (A3), not the observation representation — munsu's "one live
contract" doctrine is restored at the representation level.

Success = the coarse orchestrator duplicate is gone, the dispatch gate consults
the single authority, and the authority's answer is derived from the existing
typed axes — with the P1a invariants preserved verbatim.

Who benefits: the wake-dispatch path (correct busy/idle gating instead of a
lifecycle-only summary), and every future consumer (herdr event transport, the
Lavish board) that needs one busy answer.

## Tech Stack

Go (module `munsu`), stdlib only. cobra for any CLI surface. No new deps, no new
storage tech (ADR-0019).

## Commands

```sh
go build ./...
go vet ./...
go test ./internal/orchestrator/... ./internal/backend/...
go test ./...            # full gate before delivery
gofmt -l .               # must be empty
```

## Project Structure

```
internal/backend/endpoint_observation.go   → typed axes (Activity, Lifecycle, …); the source of truth this module consumes
internal/orchestrator/wake_dispatch.go      → DispatchWake; consumes backend.EndpointObservation directly (orchestrator-local duplicate retired in A2)
internal/orchestrator/supervision_*.go      → watcher-side observation flow
internal/fleet/busy_authority.go              → the single busy authority (fleet-side owner); home decided in A1 as a fleet decision type over backend.EndpointObservation (aliased EndpointStatus), not in backend or orchestrator
```

## Code Style

Match `endpoint_observation.go`: `uint8` typed enums with `Invalid` at iota 0, a
`String()` and a `Valid()`, orthogonal axes never collapsed. Example of the
consumption shape the authority must preserve (unknown never becomes idle):

```go
// Only endpoint death (Fleet-authorized Absent()) overrides a busy reading.
switch obs.Activity {
case backend.ActivityBusy:
    return BusyHeld            // hold; do not dispatch a competing turn
case backend.ActivityUnknown:
    return Unknown             // never treated as idle
case backend.ActivityIdle:
    return Idle
}
```

## Testing Strategy

Go `testing`, table-driven, tests beside the code (`_test.go`). Cover:
- `unknown` maps to unknown (never idle) at the authority and at the dispatch gate.
- Only a Fleet-authorized `Absent()` overrides a busy reading; an adapter probe alone does not.
- `blocked` stays a diagnostic attention state, not a lifecycle conclusion.
- Event-derived observations (`backend.SourceEvent`) contribute busy/idle hints but never `Live()`/`Absent()`.
- After the retire, `DispatchWake`'s gate produces the same decisions on the shared authority (regression table over alive/starting/unresponsive/dead/unknown/stale-identity/unresolved).

Guard-coverage: the retired-duplicate path and the "unknown ≠ idle" refusal must each be entered by a test (the `guards` job derives its set from the tree).

## Boundaries

- **Always:** preserve P1a verbatim (`unknown != idle != dead`; only Fleet authorizes `Live()`/`Absent()`); run `go test ./...` before delivery.
- **Ask first:** any change to the `backend.Activity` axis values or `EndpointObservation` shape; moving lifecycle authority out of `taskauthority`.
- **Never:** collapse `unknown` into `idle`/`dead`; let an adapter conclude liveness; introduce a third busy representation; add storage tech.

## Success Criteria

1. The orchestrator's duplicate `EndpointObservationState` and
   `EndpointObservation{State,Detail}` are removed; `wake_dispatch.go` no longer
   declares a parallel enum.
2. Exactly one authority answers busy/idle/unknown/dead, deriving from
   `backend.EndpointObservation`'s typed axes.
3. `DispatchWake` gates through that authority and consumes the `Activity` axis
   (a busy endpoint is held; unknown is not dispatched-as-idle).
4. All P1a invariant tests pass; `go test ./...`, `go vet`, `gofmt -l .` clean;
   deadcode and guards jobs green.

## Open Questions

- Home for the authority: resolved in A1 as a fleet-side decision type at
  `internal/fleet/busy_authority.go`, operating on `backend.EndpointObservation`
  (aliased `EndpointStatus`); adapters remain producers only. The `orchestrator`
  duplicate was retired in A2 (it is gone, not moved here).
- Does any current consumer other than `DispatchWake` read the coarse
  orchestrator enum today? (Grep at Plan start; each must be re-pointed.)
