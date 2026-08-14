# Domain glossary

## Actor Rank

The hierarchical rank of an agent instance operating in the fleet:
- `general`: Fleet Orchestrator (highest authority).
- `captain`: Persistent Domain Supervisor.
- `soldier`: Short-lived Task Worker.

Specified via `MUNSU_ROLE=general|captain|soldier`.

## Task Kind

The functional classification of a task assigned to a soldier or captain:
- `ship`: Code delivery task (implements features/fixes, subject to no-mistakes delivery pipeline).
- `scout`: Investigation or audit task (produces research report).
- `captain-supervisor`: Persistent supervisor lane for domain management.

## Branch Prefix

The git branch naming prefix for soldier worktrees. Default is `mu/<task-id>` (configured via `config/branch-prefix`). Legacy `fm/` prefix is deprecated and unsupported (Clean Break Policy).

## Uplink Report

A material report sent from a lower rank to exactly one parent rank.

## Delivery Notification

An immediate signal that a durable Uplink Report is waiting for the receiving rank.

## Processing Ack

Confirmation that the receiving agent has accepted an Uplink Report into its context. It does not mean that any follow-up action has been completed.

## Resilient Backend Fallback

Automatic failover to `tmux` with a `stderr` warning and an `IsFallback` signal when a custom terminal session backend (`herdr`, `orca`, `zellij`) encounters resolution failure.

## Wake Lease

An exclusive lock lease acquired on a queued wake record in `internal/orchestrator` during claim/drain operations to prevent concurrent processing by multiple supervisors.

The lease is renewable and carries an owner identity and fencing token. Expiry permits redelivery; a stale owner cannot acknowledge the wake after takeover.

## Uplink Obligation

The delivery lifecycle for one sender's Uplink Reports under a Task ID and key. Each generation is superseded only by a newer generation and closes when the exact report receives a Processing Ack.

## Task Generation

A monotonically increasing identity for one incarnation of a task lifecycle. Delivery evidence, reports, and resources must bind the exact generation they affect.

## Task Revision

The monotonic ordering of authoritative mutations within one Task Generation. It advances when that generation's definition, lifecycle, bindings, dispatch evidence, or durable decisions change. It does not identify a new lifecycle incarnation.

## Task Operation

One idempotent authoritative mutation identified by a stable Operation ID and request digest. Repeating the same identity and intent returns the original outcome; reusing the identity for different intent is a conflict.

## Task Authority

The single authority that evaluates task lifecycle, readiness, and durable dispatch-control intent against the latest Authoritative Task Aggregate. It owns semantic transitions and audit outcomes; persistence and projections do not independently decide task state. See [ADR-0008](docs/adr/0008-owner-clean-architecture-and-pre-public-v1-reset.md) §2 (extending ADR-0007).

## Resource Lease

An exclusive claim on a task-owned endpoint or worktree. The lease carries an identity and fencing token so stale task generations cannot mutate or release the resource.

## Quarantine

A fail-closed state that isolates one corrupt or contradictory lifecycle from mutation while unrelated lifecycles continue. Leaving quarantine requires an authorized explicit action.

## Config Snapshot

An immutable, validated generation of resolved configuration used for the full duration of an operation. A newer snapshot takes effect only at an operation boundary. *Resolved* means the fleet base overlaid with the owning project's Project Overlay (plus boundary-translated environment overrides). The General is the single resolution authority; Captains and Soldiers consume the resolved snapshot for their project's scope.

## Project Overlay

The project-scoped configuration layer (soldier dispatch profile set, delivery mode, harness, and operational knobs) that overlays the fleet base for every Soldier spawned for that project, whether by its owning Captain or by the General directly. Different projects carry different overlays, so one General can supervise many projects under different settings.

## Captain–Project Binding

The 1:1 domain rule that a Captain supervises exactly one project and a project has at most one owning Captain. A project may exist without a Captain (for General ad-hoc dispatch). The binding is the authoritative scope for task observation and config resolution, so a bare task or Captain ID never silently selects the wrong home.

## Authoritative Task Aggregate

The single current-state authority for a task, identified by Task ID and Task Generation and owned by exactly one rank/home. Definition and lifecycle are authoritative; backlog Markdown, `.meta`, `.status`, briefs, inbox summaries, and fleet snapshots are projections or audit records. Handoff transfers the same generation atomically. See [ADR-0008](docs/adr/0008-owner-clean-architecture-and-pre-public-v1-reset.md) §2 and §8 (replacing ADR-0004 where conflicting).

## Delivery Transaction

Delivery preparation verifies immutable provider identity, head SHA, and required checks before entering the pre-merge `delivered` phase. Merge authorization is a separate Decision bound to Task Generation and head SHA. Merge and retirement are separate durable transactions with typed partial/unknown outcomes. munsu does not own Issue closure: the PR body author writes the closing keyword and the provider enforces it at merge. See [ADR-0004](docs/adr/0004-authoritative-task-lifecycle-delivery-and-projections.md) and [ADR-0010](docs/adr/0010-munsu-does-not-own-issue-closure.md).

## Dispatch Hold

A durable scoped overlay that blocks new handoff/start/spawn without changing queued or dependency state. Holds survive restart, compose conservatively, and require an explicit release; release does not auto-start work. See [ADR-0004](docs/adr/0004-authoritative-task-lifecycle-delivery-and-projections.md).

## Endpoint Binding

The immutable Task-Generation-bound identity and lease for a runtime endpoint. Typed observations distinguish alive, starting, unresponsive, dead, unknown, stale identity, and unresolved. Mutable task metadata is only a projection. See [ADR-0005](docs/adr/0005-runtime-bindings-supervision-recovery-and-mutation-fencing.md).

## Worktree Binding

The immutable Task-Generation-bound worktree lease, repository identity, Git directory/common directory, path, and initial head. Managed Git mutation capabilities validate this binding before modifying local history, files, or remote refs. See [ADR-0005](docs/adr/0005-runtime-bindings-supervision-recovery-and-mutation-fencing.md).

## Watcher Lease

The typed per-home identity and heartbeat for the only watcher authorized to mutate that home's lifecycle. General supervises Captain watcher leases without directly processing Captain-owned task state. See [ADR-0005](docs/adr/0005-runtime-bindings-supervision-recovery-and-mutation-fencing.md).

## Runtime Identity

The running executable's canonical path and digest plus embedded build provenance, contract version, and integration digest.
