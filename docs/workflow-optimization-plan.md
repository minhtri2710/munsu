# munsu Whole-Workflow Optimization Plan

Status: audit and ordered implementation plan; no product behavior changed.

## Evidence inventory

| Friction | Repository evidence | Consequence |
|---|---|---|
| Backend and delivery mode are repeated at multiple boundaries | `COMMANDS.md` exposes `spawn --mode` and `--backend`; `AGENTS.md` documents mode precedence in `internal/fleet/spawn_spawn.go`; `docs/architecture.md` requires explicit adapter selection and fail-closed behavior | Operators must restate settings and can accidentally create a launch that disagrees with the resolved project configuration. |
| Scout scope/runtime is mostly procedural | `COMMANDS.md` documents `brief --scout`, `spawn --kind scout`, and `promote`, but no bounded runtime/scope knobs are exposed in the workflow contract | Scout work can run without a mechanically visible budget or explicit scope evidence. |
| Supervision is wake-driven at the semantic boundary but still polling underneath | `SUPERVISION.md` says `watch` is event-driven, then specifies a 5-second loop, status scans, pane checks, and three stale polls; `COMMANDS.md` exposes both `watch`/wake commands and reduced-cadence `afk` polling | Wake delivery and polling can duplicate work and make latency/ownership hard to reason about. |
| Report/relay/ack has recently needed repeated repair | Recent commits `49081d58` (terminal scout report lifecycle) and `d0e15e9e` (local-only scout relay closure) both add lifecycle-specific fixes and E2E coverage. `internal/orchestrator/captain_relay.go` still reconciles separate local and parent paths. | The report path has multiple delivery branches and is vulnerable to “done but not acknowledged” or teardown-visible partial state. |
| Teardown has special-case forced/normal paths | `docs/self-hosting.md` says scout teardown warns on unresolved holds and accepts `--force`; `internal/backend/session_backend_herdr.go` carries durable teardown IDs, workspace labels, and deny lists. Recent `d0e15e9e` adds an E2E proving local-only scout teardown can be normal. | Resource cleanup and lifecycle completion are coupled to topology-specific exceptions. |
| Lead→Peer topology policy is not a single enforced workflow rule | `CONTEXT.md` defines General→Captain→Soldier rank ownership and Captain–Project Binding; ADR-0008 §4/§8 says ownership is scoped and General must not mutate Captain-owned task state. The CLI docs expose Captain and General operations but do not present one concise Lead→Peer dispatch policy. | Direct General→Soldier and Captain-mediated paths can diverge in configuration, observation, and relay behavior. |

### Recent E2E signal

The branch base includes two adjacent fixes with explicit E2E/regression tests:

- `49081d58`: terminal scout `report done` completes the canonical lifecycle before uplink.
- `d0e15e9e`: local-only scout terminal reporting closes the relay locally so normal teardown succeeds.

These are concrete evidence that terminal report, relay acknowledgement, and teardown are one workflow seam—not independent cleanup concerns.

## Peer review

A bounded independent Peer review was delegated to Antigravity with a read-only scope covering `CONTEXT.md`, ADR-0008, recent scout report/relay commits, supervision, configuration, teardown, and topology. The Peer confirmed the ownership constraint (Task Authority owns lifecycle; Orchestrator owns Uplink/supervision; Fleet owns workforce execution) and independently highlighted the recent scout-report/relay fixes as the strongest concrete signal. The delegated process exited before completing its repository inventory, so no unsupported Peer claim is treated as evidence; the findings above are independently verified against the repository.

## Ordered PR-sized slices

1. **Canonical workflow evidence and topology matrix (this artifact).** Record one path for General→Soldier and Captain→Soldier, identify each config snapshot boundary, and define report/ack/teardown invariants. Acceptance: reviewers can trace every command to its owner under ADR-0008; no runtime behavior changes.
2. **Config handoff de-duplication.** Resolve backend, harness, and delivery mode once into the immutable Config Snapshot at spawn boundary; pass the digest through the existing assignment/binding path; reject conflicting CLI restatements. Acceptance: tests cover default/project/explicit precedence, digest stability, and conflict failure; no new public flags.
3. **Scout budget as existing brief/task data, not a new subsystem.** Add validated scope and runtime-budget fields only where the current brief/task aggregate already owns scout definition. Acceptance: missing/expired/over-budget outcomes are typed and idempotent; no migration path.
4. **Supervision ownership/latency measurement.** Instrument the existing watcher cycle and wake delivery (without changing semantics) to expose scan count, stale age, wake-to-claim latency, and duplicate suppression. Acceptance: deterministic unit tests plus one integration assertion; metrics remain internal.
5. **Single report→relay→ack state transition.** Collapse branching behind Orchestrator’s existing Uplink lifecycle and preserve local-only closure as a topology policy. Acceptance: crash/replay, duplicate report, Captain relay, General direct, and normal teardown tests all prove `Intent → Durable → Delivered → Acked → Retired`.
6. **Topology policy hardening.** Make General→Soldier direct dispatch and Captain-mediated dispatch explicit policy choices at the Fleet boundary; ensure Captain-owned state is never read/mutated by General. Acceptance: policy matrix tests reject ambiguous parent/home/config combinations.
7. **Teardown convergence.** Make retirement consume canonical terminal/ack evidence and return typed partial/unknown outcomes; retain `--force` only as an explicit operator escape for unresolved holds. Acceptance: no-force succeeds after canonical completion, refuses genuinely unresolved work, and repeated teardown is idempotent.
8. **End-to-end workflow gate.** Add one fresh-home integration scenario from init/session-start through spawn, report, relay/ack, supervision wake, and teardown for both topology policies. Acceptance: `go build ./...`, `go vet ./...`, and `go test ./...` pass; no legacy or parallel lifecycle path remains.

## Risks and ordering rationale

- Do not begin with broad refactoring: the recent fixes show the report/relay/teardown seam is active and should be characterized before changing ownership.
- Do not introduce migration or compatibility state; ADR-0008 §11 requires a fresh v1 path.
- The first behavior slice should be the lowest-risk measurement/config boundary work after this audit, because it can fail closed without changing task or Uplink semantics.
- Scout budgets and topology enforcement depend on canonical config assignment and ownership evidence, so they follow slice 2.
- Full end-to-end changes come last, after each owner has focused contract coverage.

## Verification run

Run from the repository root after this artifact is added:

```sh
go build ./...
go vet ./...
go test ./...
```
