# Tasks: ADR-0021 + ADR-0022

Delivery order is fail-fast (see `tasks/plan.md`). One task = one issue = one
`/herdr-delivery-workflow` run through the no-mistakes gate. Every task's
verification includes the project Definition of Done: `go build ./...`,
`go vet ./...`, `go test ./...`, `gofmt -l .` empty, deadcode + guards + citations green.

---

## Task T0: Land the docs upstream

**Description:** Commit and deliver, as one docs-only issue, both ADRs
(`docs/adr/0021-*.md`, `docs/adr/0022-*.md`), the plan artifacts
(`docs/plans/adr-0021-supervision/**`, `docs/plans/adr-0022-delivery-contract/**`),
and `tasks/plan.md` + `tasks/todo.md`. Delivery is no-mistakes and herdr agents
branch from `origin/main`, so every later task's cited spec and the Complete
checkpoint's ADR edits require these files upstream first. Docs-only: no code.

**Acceptance criteria:**
- [ ] Both ADRs + `docs/plans/**` + `tasks/*` on `origin/main` via the gate.
- [ ] CI citations gate green on the ADRs (docs/adr is in the covered set); `docs/plans/**` is citations-exempt.
- [ ] No code file touched; `invariants` job green.

**Verification:**
- [ ] `.github/scripts/citations.sh check` clean for the new ADRs.
- [ ] `git status` shows only the intended docs/tasks files.
- [ ] Gate green (no-mistakes).

**Dependencies:** None
**Files likely touched:** `docs/adr/0021-*.md`, `docs/adr/0022-*.md`, `docs/plans/**`, `tasks/plan.md`, `tasks/todo.md`
**Estimated scope:** S (docs-only)

---

## Task A1: Busy authority consuming the `Activity` axis

**Description:** Introduce one fleet-side owner that answers busy/idle/unknown/dead
by deriving from `backend.EndpointObservation`'s typed axes (consume the existing
producer-only `Activity`). Additive — no consumer switched yet.

**Acceptance criteria:**
- [ ] A single authority derives busy/idle/unknown/dead from `backend.EndpointObservation`.
- [ ] `unknown` maps to unknown (never idle); only a Fleet-authorized `Absent()` overrides busy; `blocked` stays diagnostic; `SourceEvent` hints never yield `Live()`/`Absent()`.
- [ ] No existing consumer changed; build + full test suite still green.

**Verification:**
- [ ] Tests: `go test ./internal/backend/... ./internal/orchestrator/...`
- [ ] Build: `go build ./...`
- [ ] Manual: P1a table test present and entered by the guards derivation.

**Dependencies:** None
**Files likely touched:** `internal/backend/endpoint_observation.go` (or a new fleet-side decision file), plus `_test.go`
**Estimated scope:** S–M

---

## Task A2: Retire duplicate observation types; switch `DispatchWake` to `backend.EndpointObservation`

**Description:** Delete the duplicate `EndpointObservationState` and
`EndpointObservation{State,Detail}` in `internal/orchestrator/wake_dispatch.go`;
switch `ProbePort.Probe` and `DispatchWake` to `backend.EndpointObservation`.
Behavior-preserving over the 7 coarse states — no `Activity` gating change yet.

**Acceptance criteria:**
- [ ] Orchestrator no longer declares a parallel observation enum/struct.
- [ ] `ProbePort`/`DispatchWake` consume `backend.EndpointObservation`; all probe adapters updated.
- [ ] Regression table over alive/starting/unresponsive/dead/unknown/stale-identity/unresolved yields identical dispatch decisions.

**Verification:**
- [ ] Tests: `go test ./internal/orchestrator/... ./internal/backend/...`
- [ ] Build: `go build ./...`; deadcode green (no orphan after deletion).
- [ ] Manual: grep confirms no remaining orchestrator-local `EndpointObservationState`.

**Dependencies:** A1
**Files likely touched:** `internal/orchestrator/wake_dispatch.go`, probe adapter(s), `internal/orchestrator/supervision_*.go`, `_test.go`
**Estimated scope:** M

---

## Task A3: Activity-aware dispatch gate

**Description:** Route the `DispatchWake` gate through the busy authority so a
busy endpoint is held and `unknown` is never dispatched as idle.

**Acceptance criteria:**
- [ ] `DispatchWake` consults the authority; `ActivityBusy` → hold (no competing turn); `ActivityUnknown` → not dispatched-as-idle.
- [ ] P1a invariants hold end-to-end at the gate.
- [ ] The "unknown ≠ idle" refusal branch is entered by a test.

**Verification:**
- [ ] Tests: `go test ./internal/orchestrator/...`
- [ ] Build: `go build ./...`; guards green.
- [ ] Manual: dispatch decision table shows busy-hold vs unknown-skip.

**Dependencies:** A2
**Files likely touched:** `internal/orchestrator/wake_dispatch.go`, `_test.go`
**Estimated scope:** S–M

---

## Task P1: Durable domain-neutral process-event source

**Description:** Extract a generic durable process-event source from the
merged-PR poll lane: register an event, capture the external result **before**
enqueuing a wake, gate re-announcement on a generation-keyed ack. Rides
`home.EnqueueWake`/`DrainWakes`; registry/records are files under the existing store.

**Acceptance criteria:**
- [ ] Result captured durably before any wake; generation-keyed ack is the sole re-announcement stop.
- [ ] Crash matrix passes under `-race`: crash before-wake → re-delivered; after-wake before-ack → re-delivered; after-ack → not; generation mismatch → old ack does not suppress new registration.
- [ ] Unresolved condition spends zero wakes per poll cycle.

**Verification:**
- [ ] Tests: `go test -race ./internal/orchestrator/... ./internal/home/...`
- [ ] Build: `go build ./...`
- [ ] Manual: capture-before-wake guard entered by a test.

**Dependencies:** None
**Files likely touched:** new source file under `internal/orchestrator/` (or `internal/fleet/`), `internal/home/watcher_state.go` (read only if possible), `_test.go`
**Estimated scope:** M

---

## Task P2: Re-express merged-PR retirement as a process-event instance

**Description:** Rebuild `fleet.RetireMergedPoll` / `orchestrator.DiscoverAllChecks`
merged-PR retirement as one instance of the generic process-event source,
subsuming the bespoke lane (one live contract). Behavior-preserving.

**Acceptance criteria:**
- [ ] Merged-PR retirement uses the generic source; no parallel bespoke poll lane remains.
- [ ] Existing `retirement_poll_test.go` crash/quarantine tests pass unchanged in intent.
- [ ] No behavior change to retirement outcomes.

**Verification:**
- [ ] Tests: `go test ./internal/fleet/... ./internal/orchestrator/...`
- [ ] Build: `go build ./...`; deadcode green.
- [ ] Manual: retirement crash-ordering preserved.

**Dependencies:** P1
**Files likely touched:** `internal/fleet/retirement_poll.go`, `internal/orchestrator/supervision_check.go`, `_test.go`
**Estimated scope:** M

---

## Task C1: condition→action registration, fire-once on stable-true

**Description:** Register a (condition, action) pair on top of the process-event
source; fire the action exactly once on first stable-true, recorded by a durable
fired-marker; re-evaluation after firing is a no-op until cleared.

**Acceptance criteria:**
- [ ] Fires exactly once on first stable-true; a later true tick is a no-op.
- [ ] Fire-once survives restart (durable fired-marker, no double-fire); transient true→false→true does not fire early.
- [ ] Delivery goes through the process-event source (capture-before-wake, single wake).

**Verification:**
- [ ] Tests: `go test -race ./internal/orchestrator/...`
- [ ] Build: `go build ./...`; guards green.
- [ ] Manual: "already-fired → no-op" and "not-yet-stable → no fire" branches each test-entered.

**Dependencies:** P1
**Files likely touched:** new condition→action file under `internal/orchestrator/`, `_test.go`
**Estimated scope:** M

---

## Task D1: Durable per-task delivery contract

**Description:** Record the resolved delivery mode on the canonical task
(`internal/taskauthority`) at first spawn; later generations read the recorded
contract; `meta["mode"]` becomes a projection. Registry feeds only the first
resolution and cannot silently override a recorded contract. Disambiguate the
`ScaffoldOptions.Mode` name collision (commit type, not delivery mode).

**Acceptance criteria:**
- [ ] First spawn records the resolved mode via a `taskauthority` op; second generation reads it (registry change between spawns does not silently flip it).
- [ ] `meta["mode"]` is a projection of the durable contract, not its source.
- [ ] `ScaffoldOptions.Mode` collision disambiguated; `+yolo` still does not relax `RequireNoMistakes`.

**Verification:**
- [ ] Tests: `go test ./internal/fleet/... ./internal/taskauthority/...`
- [ ] Build: `go build ./...`; guards green.
- [ ] Manual: re-spawn with changed registry keeps the recorded mode.

**Dependencies:** None
**Files likely touched:** `internal/taskauthority/` (aggregate field + op), `internal/fleet/spawn_config_snapshot.go`, `internal/fleet/brief.go`, `_test.go`
**Estimated scope:** M

---

## Task D2: Authorized fallback recorded as a transition

**Description:** Make the no-mistakes → direct-PR fallback
(`fleet.preflightNoMistakes` + late capability loss) mutate the recorded contract
through a `taskauthority` transition op (from/to/reason/generation) instead of a
silent fresh re-resolution.

**Acceptance criteria:**
- [ ] Authorized fallback records a transition (from-mode, to-mode, reason, generation) on the canonical task.
- [ ] No divergent fresh resolution per spawn; the contract always states the mode in force and how it got there.
- [ ] No-mistakes gate semantics unchanged (ADR-0016); the recorded-fallback branch is test-entered.

**Verification:**
- [ ] Tests: `go test ./internal/fleet/... ./internal/taskauthority/...`
- [ ] Build: `go build ./...`; guards green.
- [ ] Manual: capability-loss path produces a recorded transition, not a silent flip.

**Dependencies:** D1
**Files likely touched:** `internal/fleet/delivery_preflight.go`, `internal/taskauthority/`, `_test.go`
**Estimated scope:** M

---

## Checkpoints

### Checkpoint: Foundation (after A1, P1)
- [ ] `go test ./...` and `-race` home lane green; both primitives additive; no consumer regressed.

### Checkpoint: ADR-0021 core (after A2, A3, P2)
- [ ] One busy authority, no duplicate enum; retirement rides the generic source; P1a table green.

### Checkpoint: Complete (after C1, D1, D2)
- [ ] Both ADRs implemented; flip ADR-0021/0022 Status Proposed→Accepted; deadcode + guards + citations green. Review with human.
