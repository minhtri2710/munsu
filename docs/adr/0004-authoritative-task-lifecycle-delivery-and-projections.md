# 0004. Authoritative Task Lifecycle, Delivery Transactions, and Projections

* **Status:** Accepted; implementation pending
* **Date:** 2026-07-30
* **Extends:** ADR-0002 (durable lifecycle and clean breaks), ADR-0003 (project-scoped config)
* **Triggered by:** `munsu-workflow-incident-report-2026-07-30.md`

## Context

The incident exposed several representations of one task disagreeing simultaneously: backlog state, task metadata, status logs, panes, delivery evidence, mailbox records, and fleet snapshots. Handoff moved backlog records without creating the representation required by `brief` and `spawn`; manually running `task add` then created duplicate identities. Open green pull requests remained `in_flight`, stale status lines remained visible after newer evidence, and merge authorization was entangled with lifecycle state.

The same fragmentation caused ambiguous task lookup across General and Captain homes, unsafe primary-first resolution, missing linked-Issue closure, undocumented dependency reinterpretation, and pause instructions that survived only in conversational context.

## Decision

### 1. One authoritative Task aggregate

A current task is one typed aggregate identified by `(TaskID, TaskGeneration)` and owned by exactly one rank/home.

```go
type Task struct {
    ID         string
    Generation uint64
    Owner      OwnerRef
    Definition TaskDefinition
    Lifecycle  TaskLifecycle
}
```

`TaskDefinition` owns description, kind, project, requirements, done criteria, dependencies, Issue links, Delivery Plan, and context hints. `TaskLifecycle` owns phase, revision, applicable dispatch holds, and dispatch interpretation evidence. Endpoint, worktree, delivery, and recovery records are separate immutable or generation-bound bindings.

Backlog Markdown, `.meta`, `.status`, briefs, fleet snapshots, and inbox summaries are projections or audit records. They are not competing current-state authorities.

### 2. Atomic ownership and handoff

`backlog add` creates a complete queued Task Generation. Handoff transfers that same generation atomically: the source records transfer and the destination receives the complete definition and lifecycle. A failed handoff leaves source ownership intact. `task add` no longer creates a parallel task identity.

Task resolution collects all candidate owners. It auto-selects only one proven current owner. Multiple active owners are corrupt state and enter quarantine; a bare ID never returns the first match.

### 3. Lifecycle phases and projections

The canonical pre-merge phase is `delivered`: a PR/MR is open, its immutable identity and head SHA match the task, and the provider supplies effective approval and terminal green checks. Provider-boundary enforcement and terminal reconciliation follow ADR-0010; adapters fail closed when the exact authorized merge conditions cannot be enforced.

A Ship Soldier cannot report `resolved` as a substitute for delivery. The Soldier supplies evidence; the assigned Captain or General runs idempotent `PrepareDelivery`, preserves the child Task ID with stable obligation key `delivery`, and transitions the aggregate. Scout completion uses `done`; its parent projects `ReportReady` where applicable.

Current projections derive from the Task lifecycle, durable decisions, accepted activities, and provider evidence. Append-only `.status` files remain audit history only. Fleet output separates active, needs-attention, historical/superseded, and quarantined records.

### 4. Merge authorization and transactions

Merge authorization is a separate durable Decision/Hold bound to exact Task Generation, PR identity, and head SHA. External provider truth is reconciled even without munsu authorization, but is recorded as `merged_external_unapproved`; approval is never fabricated.

`munsu delivery pr-merge --teardown` may remain an ergonomic wrapper, but it orchestrates two durable transactions:

1. `MergeDelivery`
2. `RetireTask`

The wrapper returns a typed composite result. Cleanup failure is non-zero and explicitly says the remote merge succeeded; retry resumes retirement without re-running merge.

Merge mutation outcomes include `merge_failed`, `merged_verified`, `already_merged`, `merged_metadata_failed`, and `merged_cleanup_failed`. If provider mutation returns an error and read-only reconciliation cannot determine remote truth, persist a `MergeAttempt` with `remote_unknown`. Never retry that mutation attempt. Read-only reconciliation continues with bounded backoff; a verified-open PR closes the attempt as failed and permits a new attempt ID, while persistent uncertainty escalates to operator attention.

### 5. Linked Issue policy — RETIRED by ADR-0011

This section is retired. munsu does not own Issue closure on delivery: the PR body author writes the closing keyword and the provider enforces it at merge. The `IssueLink` model and its delivery guard were deleted; see ADR-0011 for the decision and for the front door a future auto-close guarantee must come through.

### 6. Delivery-mode transitions

A task has a revisioned `DeliveryPlan` with requested mode, effective mode, and exact allowed fallbacks. A Soldier does not silently change mode. A parent-owned durable Decision changes mode, unless project policy pre-authorizes the exact transition and reason. Known capability failure before spawn selects the effective mode before launch; failure discovered later preserves work and evidence while transitioning the Delivery Plan.

Mode readiness is proven by a context-scoped, typed capability attestation bound to project, execution home, harness, gate agent, executable identity, and resolved config. Cached attestations require unchanged input digest, matching context, and valid TTL. Irreversible mode-specific operations revalidate capability.

### 7. Dependency interpretation and dispatch holds

When directive order differs from the dependency-ready set, the Captain writes a `DispatchInterpretation` containing the directive revision, dependency snapshot digest, selected tasks, divergence class, explanation, and evidence. Safe parent-spec-to-executable-child interpretation may report-and-proceed only under configured autonomy; ambiguous or material intent conflicts require a Decision.

Pause is a durable scoped `DispatchHold`, not prose and not a task phase. Holds may target a Captain, project, task set, or dependency subgraph. Applicable holds block handoff/start/spawn while queued tasks remain queued. Releasing a hold does not automatically start work.

### 8. Backlog CLI clean break

The public vocabulary separates queries from mutations:

* `munsu backlog ready` — query-only readiness projection.
* `munsu backlog unblock <id>` — remove a dependency/manual block.
* `munsu backlog reopen <id>` — reopen terminal work under validated policy, creating a new Task Generation when history must be preserved.
* `munsu backlog start <id>` — atomically validate dependencies, holds, watcher health, ownership, capacity, and compatibility before entering in-flight.

`backlog add --start` and mutating `backlog ready <id>` are removed. Incorrect forms return typed, directly runnable corrections.

### 9. Bounded Context Manifest

Complex tasks may carry a revision-bound `ContextManifest` combining author hints with bounded repository evidence. It provides paths, symbols, line ranges, tests, commands, invariants, reasons, confidence, and digests rather than embedding whole files. The default budget is at most ten entries, including no more than three implementation seams, three test/helper seams, and two architecture/convention references. Expansion is explicit and revisioned.

## Consequences

* Task identity, ownership, lifecycle, delivery, and readiness have one authority.
* Handoff followed by brief/spawn no longer requires manual duplicate registration.
* Open green PRs gain a truthful pre-merge phase without calling them merged.
* Merge and Issue reconciliation expose partial success and unknown remote truth without unsafe retries.
* Pauses and dependency reinterpretations survive restart and are auditable.
* Existing task/backlog/meta state requires an explicit conflict-detecting migration.
* CLI and projection consumers must be refactored to use the aggregate rather than parsing local files independently.

## Rejected Alternatives

* Permanent synchronization between backlog and task metadata: preserves dual authority.
* Primary-first task lookup: silently chooses the wrong owner.
* Treating `resolved` as Ship completion: conflates wake/Decision closure with delivery.
* Binary merge result (`error` or success): cannot represent remote success with local metadata/cleanup failure.
* Inferring Issue identity from task ID or PR body alone: insufficient authority and policy.
* Silent delivery fallback or dependency reinterpretation: hides material changes in intent.
* Pausing by changing every queued task phase: destroys queue/dependency truth.
