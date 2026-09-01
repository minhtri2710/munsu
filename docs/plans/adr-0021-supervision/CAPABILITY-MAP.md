# Capability Map: ADR-0021 Supervision Foundation

Source: `docs/adr/0021-durable-process-event-supervision-and-one-busy-authority.md`
(gaps G1/G2/G3 from the firstmate → munsu parity refresh). Approved 2026-08-31.

| Module id | Responsibility | Depends on |
|---|---|---|
| `busy-authority` | One fleet-side owner of the busy/idle/unknown/dead question that **consumes** the existing `backend.Activity` axis on `backend.EndpointObservation` (previously producer-only; A3 routes `DispatchWake` through it). Retired the duplicate coarse `EndpointObservationState` + `EndpointObservation{State,Detail}` re-declared in `internal/orchestrator/wake_dispatch.go` (A2), and re-expressed the `DispatchWake` probe gate against the single authority (A3). | — |
| `process-event-source` | Generalize the merged-PR poll lane (`orchestrator.DiscoverAllChecks` + `fleet.RetireMergedPoll`) into a domain-neutral, durable process-event source: capture the external result **before** enqueuing any wake; a generation-keyed acknowledgement is the only thing that stops re-announcement. Rides `home.EnqueueWake`/`DrainWakes`; no new storage tech (ADR-0019). | — |
| `condition-action` | Register a (condition, action) pair; fire the action at most once on the first stably-true observation, recorded by a durable fired-marker; re-evaluation after firing is a no-op until the registration is cleared. | `process-event-source` |

**Build order:** `busy-authority` ∥ `process-event-source` → `condition-action`

## Boundary notes

- `busy-authority` and `process-event-source` are fully independent — cutting one does not force rewriting the other's requirements. They ship as two separate issues, in parallel.
- `condition-action` needs the durable source registration/fire-once substrate from `process-event-source`, so it is sequenced after it.
- All three ride the existing watcher loop + durable wake queue. No new aggregate or authority: task lifecycle stays in `internal/taskauthority` (ADR-0008); this initiative adds an observation *consumer* (`busy-authority`) and a supervision *source* (`process-event-source` / `condition-action`).

## Invariants binding every module (P1a / BEO-16)

- `unknown != idle != dead`. `unknown` is never folded into `idle`.
- Only Fleet authorizes `Live()` / `Absent()`; adapters produce observations, never conclude liveness. Event-derived hints (`backend.SourceEvent`, BEO-17/P1b) are wake/activity hints only, never lifecycle truth.
- The durable wake queue + lease is the sole wake-delivery substrate (ADR-0015).
- No new storage technology; registries and markers are files under the existing hand-rolled store (ADR-0019).

Per-module specs: `SPEC-busy-authority.md`, `SPEC-process-event-source.md`, `SPEC-condition-action.md`.
