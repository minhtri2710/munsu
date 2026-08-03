# Seam-Elimination Inventory — Legacy Task Authority Store/Authority Elimination

Status: read-only analysis (Supervisor-requested) for #406 criterion-4 completion planning.
Date: 2026-08-03
Prepared by: Lead (5331588)

## Purpose

Classify every remaining legacy `Authority`/`Store`/`taskauthorityfs` operation and
production caller by its TRUE owner and minimum replacement capability, so the #406 criterion-4
completion can be sequenced without a dependency cycle (no issue depending on its own
descendants; #412/#413/#414 are blocked by #406).

## Current state (verified head `885cb837`)

- `internal/taskauthority/storecontract/` — REMOVED.
- `internal/taskauthority/store.go` — still present (Store interface).
- `internal/taskauthority/memstore.go` — still present (in-memory fake).
- Legacy `taskauthority.New(store)` + `taskauthorityfs.NewStore(home)` — still used by callers.
- Canonical `Canonical` surface covers: Create/Get/List/ListHolds/Start/Block/Unblock/Complete/
  Reopen/Readiness/AddHold/ReleaseHold/BindWorktree/BindEndpoint + durable receipts + recovery.

## Legacy `Authority` operations → true owner

| Operation | True owner | Canonical coverage now? | Minimum replacement capability |
|---|---|---|---|
| Create/Get/List/Start/Block/Unblock/Complete/Reopen/Readiness/AddHold/ReleaseHold/BindWorktree/ConfirmSpawn | Task Authority (owner primitives) | YES (Canonical) | Already canonical; migrate callers |
| AttachAttestation | #414 (delivery) | NO | Delivery attestation (canonical) |
| InterpretDispatch / ResolveDecision | #414 (delivery) | NO | Dispatch interpretation/decision (canonical) |
| PrepareDelivery / CompleteDelivery / RecordMergeAttempt / RecordExternalMerge | #414 (delivery) | NO | Delivery prepare/complete/merge (canonical) |
| AuthorizeMerge / AuthorizeGitMutation / ClearGitMutationAuthorization / SetGitAuthContext / SetGitCapabilityTier | #414 (delivery/git auth) | NO | Delivery/git authorization (canonical) |
| ReconcileIssueLinks | #414 (delivery) | NO | Issue-link reconciliation (canonical) |
| Retire | #412 (Soldier retirement) | NO | Retirement lifecycle (canonical) |
| ReceiveTransfer / Promote / Supersede | #413 (Task Transfer) | NO | Transfer/activate primitives at Task Authority owner boundary (canonical) |

## Production callers → true owner

| Caller | True owner | Legacy op used | Notes |
|---|---|---|---|
| internal/fleet/task_handoff_transaction.go | #413 (Transfer) | ReceiveTransfer, taskauthorityfs.NewStore | Needs Task Authority transfer primitives; migration via #413 |
| internal/fleet/taskauthority_reads.go | #416 (CLI) / #407 | Get | Reads; migrate to Canonical.Get |
| internal/cli/ctx.go | #416 (CLI) | NewStore | Composition root; migrate to Canonical |
| internal/cli/task_cmd.go, task_authority_reads.go, report_cmd.go, spawn_cmd.go, git_worktree_safety.go, backlog_cmd.go, decisionhold_cmd.go, session_cmd.go, watch_cmd.go, retirement_port.go, migrate_cmd.go, task_authority_ops.go, backlog_readiness.go, lifecycle_partial.go | #416 (CLI) | NewStore + Authority ops | CLI composition; migrate to Canonical |
| internal/fleet/delivery_*.go, merge_authorization.go, git_authorization.go, retirement_poll.go, retirement_task.go, spawn_runner.go, spawn_spawn.go | #412/#414 (delivery/retirement/launch) | Delivery/retirement/launch ops | Migrate after canonical feature parity |

## Dependency-cycle resolution

- #412/#413/#414 are blocked by #406. They cannot be prerequisites for #406's completion.
- Break the cycle by separating OWNER CAPABILITIES from DOWNSTREAM WORKFLOWS:
  - Task Authority OWNER PRIMITIVES required to make the concrete implementation self-sufficient
    belong to #406 (e.g. delivery/transfer/retirement primitives THAT EXIST AT THE TASK
    AUTHORITY OWNER BOUNDARY without preserving Store).
  - Fleet-owned launch/retirement orchestration belongs to #412.
  - Fleet-owned transfer journal/orchestration belongs to #413; typed Task Authority
    reservation/receive/activate primitives needed by that workflow must exist at the Task
    Authority owner boundary without Store.
  - Task Authority Delivery Authorization belongs to #414 by explicit issue contract; do not
    duplicate its semantics in #406. Determine whether its absence actually keeps Store required,
    or whether legacy delivery callers can remain unlaunched until #414 without blocking Store
    removal.
  - CLI composition/rendering belongs to #416; mechanical migration away from legacy Authority
    may be performed earlier as bounded program integration if it does not pre-implement
    AXI/rendering semantics.

## Proposed dependency-safe sequence (per Supervisor)

a. Finish #406-owned canonical primitives and concrete filesystem absorption (incl. any
   Task-Authority-owner delivery/transfer/retirement primitives needed to be self-sufficient).
b. Migrate or temporarily remove from build/runtime any legacy callers whose behavior is
   already covered by accepted canonical primitives; use separate non-overlapping caller-
   migration Peers after #407 Fleet edits stop.
c. Remove Store/memStore/legacy Authority when nothing current requires them. If a feature
   assigned to a later issue is not yet implemented, its old implementation must NOT keep a
   second live path; the feature may be unavailable/fail closed until its owning issue lands,
   provided current issue contracts and tests are updated truthfully and no required current
   behavior is silently lost.
d. Re-verify #406 criterion 4 and only then ACCEPT #406.
e. Launch downstream issues that declare #406 as a blocker.

## Immediate next actions (authorized)

1. Re-review/integrate `885cb837` as provisional #406 groundwork if PASS (keep #406 OPEN).
2. Produce the bounded #406 completion proposal; do NOT record #406 as ACCEPTED.
3. Coordinate with #407: no Fleet caller migration while #407 Engineer edits Fleet.