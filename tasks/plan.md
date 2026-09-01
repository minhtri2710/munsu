# Implementation Plan: ADR-0021 Supervision Foundation + ADR-0022 Delivery Contract

## Overview

Deliver two ADRs as 8 vertical-slice tasks, one issue at a time through munsu's
no-mistakes gate. ADR-0021 (G1/G2/G3) adds one busy authority, a durable
process-event source, and fire-once condition→action. ADR-0022 (G5) makes a
task's delivery mode durable. Every task leaves `go build ./...` and `go test
./...` green, respects P1a (`unknown != idle != dead`, Fleet-only
`Live()`/`Absent()`), adds no storage tech (ADR-0019), keeps the wake queue as
the sole delivery substrate (ADR-0015), and keeps lifecycle authority in
`taskauthority` (ADR-0008).

Specs: `docs/plans/adr-0021-supervision/SPEC-*.md`,
`docs/plans/adr-0022-delivery-contract/SPEC-delivery-contract.md`.
Capability map: `docs/plans/adr-0021-supervision/CAPABILITY-MAP.md`.

## Architecture Decisions

- **Three independent delivery chains** — `{A1→A2→A3}` (busy-authority),
  `{P1→P2}` (process-event-source), `{D1→D2}` (delivery-contract) — plus `C1`
  (condition-action) which needs only `P1`. Chains are logically parallel but
  the no-mistakes gate serializes merges, so a single fail-fast order is given
  below.
- **Additive-first within each module.** Introduce the new primitive as
  additive (green, no consumer switch), then consolidate/retire the duplicate in
  a later task. This keeps every merge reversible and every task a clean slice.
- **Consolidation is deletion, not a new abstraction** — retiring the duplicate
  `EndpointObservationState` in `orchestrator/wake_dispatch.go` and the ephemeral
  `meta["mode"]` source-of-truth are the point, per "one live contract".

## Dependency graph

```
busy-authority:   A1 ─→ A2 ─→ A3
process-event:    P1 ─→ P2
condition-action:            C1   (depends on P1)
delivery-contract: D1 ─→ D2       (independent of ADR-0021)
```

## Recommended serial delivery order (fail-fast)

0. **T0** — land the docs (both ADRs + `docs/plans/**` + `tasks/*`) through the gate FIRST. Delivery is no-mistakes and herdr agents branch from `origin/main`, so the specs A1 cites and the ADRs the Complete checkpoint edits must exist upstream before any code task. Also gets CI's citations/invariants verdict on the ADRs early.
1. **A1** — busy authority (additive, low risk, sets the contract)
2. **P1** — process-event source (foundational, crash-matrix risk → early)
3. **A2** — consolidate observation types, delete orchestrator duplicate
4. **A3** — Activity-aware dispatch gating
5. **P2** — subsume merged-PR retirement into the generic source
6. **C1** — condition→action fire-once
7. **D1** — durable per-task delivery contract
8. **D2** — recorded authorized fallback

## Task List (index — full items in tasks/todo.md)

### Phase 0: Land the docs
- [ ] T0: Commit both ADRs + `docs/plans/**` + `tasks/*` through the gate

### Checkpoint: Docs upstream
- [ ] ADRs + specs + plan exist on `origin/main`; CI citations/invariants green

### Phase 1: Foundational primitives
- [ ] A1: Busy authority consuming the `Activity` axis
- [ ] P1: Durable domain-neutral process-event source

### Checkpoint: Foundation
- [ ] `go test ./...` + `-race` home lane green; both primitives additive, no consumer regressed

### Phase 2: Consolidation
- [ ] A2: Retire duplicate observation types, switch `DispatchWake` to `backend.EndpointObservation`
- [ ] A3: Activity-aware dispatch gate (busy → hold, unknown ≠ idle)
- [ ] P2: Merged-PR retirement re-expressed as a process-event instance

### Checkpoint: ADR-0021 core
- [ ] Single busy authority; no duplicate enum; retirement rides the generic source

### Phase 3: Fire-once + delivery contract
- [ ] C1: condition→action registration, fire-once on stable-true
- [ ] D1: Durable delivery contract on the canonical task
- [ ] D2: Authorized fallback recorded as a transition

### Checkpoint: Complete
- [ ] Both ADRs implemented; ADR statuses flipped Proposed→Accepted; deadcode + guards + citations green

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| A2 retiring the orchestrator `EndpointObservation{State,Detail}` changes `ProbePort` signature, touching all probe adapters | Med | Behavior-preserving regression table over the 7 coarse states before A3 changes any gating; split from A3 |
| P1 crash-ordering (capture-before-wake) regressed | High | Port the crash-injection matrix from `retirement_poll_test.go`; run `-race`; capture-before-wake guard must be test-entered |
| A3 folds `unknown` into `idle` by accident | High | P1a table test is acceptance-blocking; guards job derives the refusal branch from the tree |
| D1 double-source-of-truth (meta vs canonical) during transition | Med | Make `meta["mode"]` a projection in the same task; no dual-write, per dev policy |
| No-mistakes gate serializes 8 issues → long tail | Low | Fail-fast order puts foundational/high-risk first; independent chains can branch early |

## Open Questions (carried from specs, resolve at task start)

- A1/A2 home: resolved in A1 — fleet decision type at `internal/fleet/busy_authority.go` over `backend.EndpointObservation` (aliased `EndpointStatus`); A2 retires the `orchestrator` duplicate.
- P1: process-event registry record schema (files under existing store).
- C1: "stable-true" = single confirmed edge vs N-consecutive; auto-clear vs explicit.
- D1: does `local-only` participate in the contract or stay spawn-scoped.
- D2: does the durable canonical contract SUBSUME the existing `CapabilityAttestation` mode fields (RequestedMode/EffectiveMode/FallbackReason) or FEED FROM them; relate to it, never a parallel record.

## Task tracking

Tasks are also mirrored in `tasks-axi` (the project-designated tracker) with the
dependency graph encoded via `--blocked-by`: `adr-t0` (in flight) → `adr-a1`,
`adr-p1`, `adr-d1`; `adr-a2` (←a1) → `adr-a3`; `adr-p2`, `adr-c1` (←p1);
`adr-d2` (←d1). `tasks/todo.md` (authored by the agent, not user-designated)
holds the full per-task acceptance criteria; `tasks-axi ready` gives the next
unblocked task. Mark each `done` with its PR url as it merges to unblock the next.

## Delivery mechanics

Each task below is one issue → `/herdr-delivery-workflow` (one branch,
coordinator + path-owned agents, exact-head review) → no-mistakes gate. Do not
batch two tasks into one issue.
