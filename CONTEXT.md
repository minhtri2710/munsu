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

## Activation Record

The durable verdict that authorizes a migrated fleet to resume mutation. It binds an evidence digest, build identity, schema version, quarantine summary, and General approval.

## Task Generation

A monotonically increasing identity for one incarnation of a task lifecycle. Delivery evidence, reports, and resources must bind the exact generation they affect.

## Resource Lease

An exclusive claim on a task-owned endpoint or worktree. The lease carries an identity and fencing token so stale task generations cannot mutate or release the resource.

## Quarantine

A fail-closed state that isolates one corrupt or contradictory lifecycle from mutation while unrelated lifecycles continue. Leaving quarantine requires an authorized explicit action.

## Config Snapshot

An immutable, validated generation of resolved configuration used for the full duration of an operation. A newer snapshot takes effect only at an operation boundary.
