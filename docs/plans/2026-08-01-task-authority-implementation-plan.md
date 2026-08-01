# Task Authority Deep-Module Implementation Plan

* **Date:** 2026-08-01
* **Status:** Implementation in progress; Checkpoint 1 complete
* **Source:** [ADR-0007](../adr/0007-task-authority-deep-module-and-transactional-store.md)
* **Related:** [ADR-0002](../adr/0002-deep-module-clean-break-and-durable-lifecycle.md), [ADR-0004](../adr/0004-authoritative-task-lifecycle-delivery-and-projections.md), [ADR-0005](../adr/0005-runtime-bindings-supervision-recovery-and-mutation-fencing.md), [ADR-0006](../adr/0006-state-migration-build-provenance-and-compatibility-gates.md), [incident remediation master plan](2026-07-30-incident-remediation-master-plan.md)
* **Delivery mode:** no-mistakes; test-first; vertical slices; no dual mutation authority

## Implementation progress

Checkpoint 1 is complete through commit `fca1b38`. Task 2.1 is complete through commits `802ab768` and `6df038ed`. Task 2.2 is complete through commits `69b5a5fc` and `e894f2cf`. Task 2.3 is complete through commits `9dfd902a` and `7fe3d3f9`; Task 2.4 is next.

Verified on 2026-08-01:

```sh
go build ./...
go vet ./...
go test -race ./internal/taskauthority
go test ./internal/home ./internal/fleet ./internal/cli
go test ./...
```

All commands passed. The previously recorded `internal/bootstrap` timeout did not reproduce, and the full untagged suite is green. `go test -tags=integration ./...` compiles the previously failing tagged paths but remains red in pre-existing CLI/fleet config-migration and Captain config-push fixtures; `internal/taskauthority` passes within that run.

## Goal

Move Authoritative Task Aggregate lifecycle, readiness, and durable dispatch control from `internal/home`, direct CLI mutations, and fleet reach-through into one deep `internal/taskauthority` module. Persist it through a crash-recoverable `internal/taskauthorityfs` adapter composed at the CLI, while preserving current on-disk identities and removing each old mutation path as soon as its final caller migrates.

This plan refines the task-authority implementation details of the incident remediation master plan. ADR-0007 governs where the older plan mentions a generic Task repository, expected revision on every caller, or durable transaction/evidence primitives for task authority. Success requires high depth and leverage: one semantic operation must replace each caller-owned read/check/write sequence while preserving locality behind one Store seam.

## Completion criteria

The cutover is complete when:

1. `internal/taskauthority` owns lifecycle, readiness, Dispatch Hold, Dispatch Interpretation, Dispatch Decision, Task Revision, Task Operation, and typed authoritative audit rules.
2. Callers use named semantic operations; no public generic state setter, patch, save, or mutation callback exists.
3. `internal/taskauthorityfs` and an in-memory adapter pass the same Store contract suite.
4. Filesystem transactions recover deterministically after failure at every journal stage.
5. Task Generation remains the caller-visible incarnation fence; Task Revision advances on every committed mutation without becoming a mandatory caller CAS token.
6. `Start` and concurrent Dispatch Hold creation have no check-commit race.
7. Typed audit events commit with authoritative state; `.meta` and `.status` are rebuildable projections.
8. Watcher/degraded supervision is checked outside Task Authority.
9. Task, backlog, spawn, dispatch, handoff, and delivery authoritative mutations no longer call `internal/home` task mutation functions.
10. Old task aggregate/lifecycle/dispatch mutation implementations are removed from `internal/home`.
11. `go build ./...`, `go vet ./...`, `go test ./...`, focused race/crash tests, and the no-mistakes gate pass, excluding only explicitly recorded pre-existing environment failures.

## Program rules

* Write a failing behavior test before each new rule or migration behavior.
* Each task below is one focused change set; split it further if implementation exceeds one reviewable session.
* Every slice must leave the repository buildable and focused tests green.
* When the last caller of an old authoritative mutation moves, delete or unexport that mutation in the same slice.
* Projection compatibility may remain temporarily; authoritative mutation compatibility may not.
* Do not add `SetState`, `Patch`, `Save`, `UpdateAggregate`, public `Mutate`, or an interface for the whole Authority.
* Do not move authoritative types into `internal/domain` merely to avoid imports.
* Do not make runtime `View` or `Update` silently migrate schema. Unsupported schema returns an exact explicit migration action.
* Preserve lock order `.dispatch.lock → per-task lock` until measured contention justifies a separate decision.
* Do not mix watcher health into Dispatch Hold records or task phases.
* Do not update `docs/architecture.md`, `docs/port-mapping.md`, or `AGENTS.md` ahead of the code slice they describe.

## Dependency graph

```text
Task domain rules and Store contract
    ├── in-memory adapter and concurrency tests
    └── filesystem schemas and journal
            ├── explicit v1 → v2 migration
            └── CLI composition
                    ├── task/backlog lifecycle cutover
                    ├── spawn binding cutover
                    ├── dispatch-control cutover
                    ├── handoff saga cutover
                    └── delivery mutation cutover
                            └── projection/read-model cutover
                                    └── home authority deletion
```

## Phase 0 — Baseline and guardrails

### Task 0.1 — Freeze the pre-change verification baseline

**Description:** Record current focused/full test behavior before Task Authority implementation so environmental or pre-existing failures are not attributed to the refactor.

**Acceptance criteria:**

- [x] `go build ./...` and `go vet ./...` results are recorded.
- [x] Focused task/fleet/CLI tests are recorded separately from the full suite.
- [x] Known `internal/bootstrap` Pi version timeout and integration-tag compile failures are reproduced or explicitly shown resolved before implementation begins.

**Verification:**

```sh
go build ./...
go vet ./...
go test ./internal/home ./internal/fleet ./internal/cli
go test ./...
```

**Dependencies:** None.

**Files likely touched:** None; retain command output in the implementation session or delivery evidence.

**Estimated scope:** XS.

### Task 0.2 — Add architecture enforcement tests

**Description:** Add fast tests that prevent new direct task-authority reach-through while migration proceeds.

**Acceptance criteria:**

- [x] A repository test enumerates the temporary allowlist of production files permitted to call old `home` task aggregate/lifecycle/dispatch mutations.
- [x] The test fails if a new production caller is added.
- [x] The allowlist only shrinks in later slices and reaches zero before cleanup.

**Verification:**

```sh
go test ./internal/fleet ./internal/cli
```

**Dependencies:** Task 0.1.

**Files likely touched:**

- `internal/fleet/task_aggregate_authority_test.go`
- `internal/cli/task_aggregate_authority_test.go`

**Estimated scope:** S.

## Phase 1 — Deep module and in-memory implementation

### Task 1.1 — Define authoritative records and value semantics

**Description:** Create the Task Authority vocabulary without persistence: Aggregate, Definition, Lifecycle, Generation, Revision, bindings, Dispatch Hold/Interpretation/Decision, Task Operation, receipts, and typed audit events.

**Acceptance criteria:**

- [x] Task Generation validation preserves existing positive monotonic identity semantics.
- [x] Task Revision starts at one, advances within a Generation, and resets for a new Generation.
- [x] Aggregate validation rejects mismatched generation-bound bindings, invalid current identities, and malformed dispatch scope.
- [x] JSON representations are deterministic and versioned.

**Verification:**

```sh
go test ./internal/taskauthority -run 'Test.*(Generation|Revision|Aggregate|Binding|Dispatch)'
```

**Dependencies:** Task 0.2.

**Files likely touched:**

- `internal/taskauthority/model.go`
- `internal/taskauthority/dispatch.go`
- `internal/taskauthority/operation.go`
- `internal/taskauthority/model_test.go`

**Estimated scope:** M.

### Task 1.2 — Define the transactional Store interface and contract suite

**Description:** Define the narrow implementation seam below Authority and a reusable adapter contract suite. The interface exposes transactional records and staged changes, not lifecycle verbs.

**Acceptance criteria:**

- [x] `Store.View` exposes one consistent committed view.
- [x] `Store.Update` serializes a typed Task Operation and returns a durable receipt.
- [x] Transaction records cover aggregate, dispatch records, audit event, and idempotency receipt.
- [x] The contract suite specifies rollback-on-callback-error, duplicate Operation ID behavior, Revision advancement, and Generation replacement.

**Verification:**

```sh
go test ./internal/taskauthority -run 'TestStoreContract'
```

**Dependencies:** Task 1.1.

**Files likely touched:**

- `internal/taskauthority/store.go`
- `internal/taskauthority/store_contract_test.go`
- `internal/taskauthority/errors.go`

**Estimated scope:** M.

### Task 1.3 — Implement the in-memory Store adapter

**Description:** Add the first real adapter with one transaction mutex and deterministic failure hooks. It contains no lifecycle rules.

**Acceptance criteria:**

- [x] The adapter passes the full Store contract suite.
- [x] Failed callbacks leave all records and Revision unchanged.
- [x] Concurrent updates serialize without partial visibility.
- [x] Same Operation ID and digest returns the original receipt; a changed digest conflicts.

**Verification:**

```sh
go test -race ./internal/taskauthority -run 'Test.*(MemoryStore|StoreContract|Idempotency)'
```

**Dependencies:** Task 1.2.

**Files likely touched:**

- `internal/taskauthority/memstore_test.go`
- `internal/taskauthority/store_contract_test.go`

**Estimated scope:** S.

### Task 1.4 — Implement canonical queries

**Description:** Implement concrete `Authority` construction plus `Get`, `List`, and `Readiness` over the Store interface.

**Acceptance criteria:**

- [x] `Get` and `List` return only canonical authoritative records.
- [x] `Readiness` returns typed reasons for missing owner, blocked phase, in-flight phase, terminal phase, and applicable Dispatch Hold.
- [x] Watcher health is absent from Authority readiness.
- [x] Tests run the real Authority over the in-memory adapter.

**Verification:**

```sh
go test ./internal/taskauthority -run 'TestAuthority.*(Get|List|Readiness)'
```

**Dependencies:** Task 1.3.

**Files likely touched:**

- `internal/taskauthority/authority.go`
- `internal/taskauthority/readiness.go`
- `internal/taskauthority/authority_query_test.go`

**Estimated scope:** M.

### Task 1.5 — Implement core lifecycle operations

**Description:** Implement `Create`, `Start`, `Block`, `Unblock`, `Complete`, and `Reopen` as named semantic operations evaluated against fresh state inside Store transactions.

**Acceptance criteria:**

- [x] Every mutation requires a stable Task Operation ID and Expected Generation where an existing Generation is targeted.
- [x] Invalid phase transitions return typed non-retryable conflict without mutation.
- [x] Every committed mutation advances Revision and emits a typed audit event.
- [x] `Reopen` creates the next Generation at Revision one and preserves prior history.

**Verification:**

```sh
go test ./internal/taskauthority -run 'TestAuthority.*(Create|Start|Block|Unblock|Complete|Reopen)'
```

**Dependencies:** Task 1.4.

**Files likely touched:**

- `internal/taskauthority/lifecycle.go`
- `internal/taskauthority/lifecycle_test.go`
- `internal/taskauthority/audit.go`

**Estimated scope:** M.

### Task 1.6 — Prove Start/Hold atomicity

**Description:** Implement durable Dispatch Hold creation/release and prove that `Start` cannot race a concurrent matching hold.

**Acceptance criteria:**

- [x] `CreateHold` and `ReleaseHold` are idempotent named operations.
- [x] `Start` checks holds and commits lifecycle state inside one Store update envelope.
- [x] A deterministic barrier test proves the hold writer cannot commit inside the Start check-commit span.
- [x] The only valid race outcomes are Start first then hold, or hold first then blocked Start.

**Verification:**

```sh
go test -race ./internal/taskauthority -run 'Test.*(DispatchHold|StartCannotRaceHold)'
```

**Dependencies:** Task 1.5.

**Files likely touched:**

- `internal/taskauthority/dispatch.go`
- `internal/taskauthority/dispatch_test.go`
- `internal/taskauthority/memstore_test.go`

**Estimated scope:** M.

### Checkpoint 1 — Domain depth

- [x] `internal/taskauthority` tests are green under `-race`.
- [x] No test fakes the whole Authority.
- [x] The public interface contains only named semantic operations and queries.
- [x] The Store interface has at least two intended implementations: in-memory and filesystem.
- [x] No production caller has switched yet; repository behavior is unchanged.

## Phase 2 — Filesystem adapter, journal, and explicit migration

### Task 2.1 — Define the on-disk v2 authority schema

**Description:** Define versioned v2 paths/documents for aggregate Revision, dispatch records, typed audit events, Task Operation receipts, and transaction manifests while preserving Task ID and Generation identities.

**Acceptance criteria:**

- [x] Schema validation fails closed for unknown versions and corrupt required fields.
- [x] Existing v1 records are detectable but not silently mutated.
- [x] Paths remain private to `taskauthorityfs`; `taskauthority` has no filesystem imports.
- [x] File permissions and path validation match or strengthen current behavior.

**Verification:**

```sh
go test ./internal/taskauthorityfs -run 'Test.*(Schema|Path|Permissions|UnsupportedVersion)'
```

**Dependencies:** Checkpoint 1.

**Files likely touched:**

- `internal/taskauthorityfs/schema.go`
- `internal/taskauthorityfs/paths.go`
- `internal/taskauthorityfs/schema_test.go`

**Estimated scope:** M.

### Task 2.2 — Implement read-only committed views and lock order

**Description:** Implement filesystem `Store.View`, canonical record loading, and the lock primitives needed by `Update` without lifecycle rules.

**Acceptance criteria:**

- [x] Canonical reads load one current Generation and its Revision, dispatch records, and receipts.
- [x] Corrupt or contradictory current records quarantine/fail closed instead of choosing the first record.
- [x] Lock acquisition order is `.dispatch.lock → per-task lock`.
- [x] Focused tests prove no reverse lock order is introduced.

**Verification:**

```sh
go test ./internal/taskauthorityfs -run 'Test.*(View|LockOrder|Corrupt|Current)'
```

**Dependencies:** Task 2.1.

**Files likely touched:**

- `internal/taskauthorityfs/store.go`
- `internal/taskauthorityfs/lock.go`
- `internal/taskauthorityfs/store_test.go`

**Estimated scope:** M.

### Task 2.3 — Implement recoverable write-ahead transactions

**Description:** Implement pending manifests, idempotent file application, commit markers, and recovery before canonical reads or writes.

**Acceptance criteria:**

- [x] `Store.Update` writes a pending manifest before authoritative file replacement.
- [x] Recovery is idempotent after failure before manifest, after manifest, after each data write, and before/after commit marker.
- [x] Canonical `View` never returns a partially applied transaction.
- [x] Callback errors produce no pending manifest and no mutation.

**Verification:**

```sh
go test ./internal/taskauthorityfs -run 'Test.*(Transaction|Recovery|Crash)'
go test -race ./internal/taskauthorityfs
```

**Dependencies:** Task 2.2.

**Files likely touched:**

- `internal/taskauthorityfs/transaction.go`
- `internal/taskauthorityfs/recovery.go`
- `internal/taskauthorityfs/transaction_test.go`
- `internal/taskauthorityfs/recovery_test.go`

**Estimated scope:** M.

### Task 2.4 — Pass the shared Store contract with the filesystem adapter

**Description:** Run the same Store contract suite against a temporary on-disk home and close semantic differences between adapters.

**Acceptance criteria:**

- [ ] Both adapters have identical receipts, Revision behavior, idempotency, and rollback semantics.
- [ ] Filesystem tests assert durable results after closing and reopening the Store.
- [ ] Adapter-specific behavior remains behind the Store seam.

**Verification:**

```sh
go test ./internal/taskauthority ./internal/taskauthorityfs -run 'TestStoreContract'
```

**Dependencies:** Task 2.3.

**Files likely touched:**

- `internal/taskauthority/store_contract_test.go`
- `internal/taskauthorityfs/contract_test.go`

**Estimated scope:** S.

### Task 2.5 — Add explicit v1-to-v2 Task Authority migration

**Description:** Extend the explicit plan/apply migration framework to convert current task aggregates and dispatch records into the v2 authority schema with Revision, initial typed audit evidence, and receipts. Runtime loads remain detect-only.

**Acceptance criteria:**

- [ ] Plan records exact home identity, source digest, source schema, target schema, records, and quarantine outcomes.
- [ ] Apply revalidates source digest, stages all target records, verifies them, installs, and writes a durable receipt.
- [ ] Corrupt or conflicting sources remain untouched and fail with exact evidence.
- [ ] Re-running a completed migration verifies the receipt and returns `already_migrated`.
- [ ] Normal `View`/`Update` returns an exact migration-required error for v1 state and never invokes migration.

**Verification:**

```sh
go test ./internal/taskauthorityfs -run 'Test.*Migration'
go test ./internal/cli -run 'TestMigrateTaskAuthority'
```

**Dependencies:** Task 2.4.

**Files likely touched:**

- `internal/taskauthorityfs/migration.go`
- `internal/taskauthorityfs/migration_test.go`
- `internal/cli/migrate_cmd.go`
- `internal/cli/task_authority_migrate_test.go`

**Estimated scope:** M.

### Checkpoint 2 — Durable adapter

- [ ] In-memory and filesystem adapters pass one Store contract suite.
- [ ] Crash injection passes at every journal stage.
- [ ] v1 state requires explicit migration; no lazy migration path exists.
- [ ] `go build ./...` and `go vet ./...` pass.
- [ ] No existing production mutation has switched yet.

## Phase 3 — CLI composition and first vertical cutover

### Task 3.1 — Compose Authority in CLI context

**Description:** Construct `taskauthorityfs.Store` and concrete `taskauthority.Authority` once after resolving the exact home.

**Acceptance criteria:**

- [ ] Command context exposes the concrete Authority without package globals.
- [ ] Commands that do not need Task Authority do not receive pass-through parameters.
- [ ] Store construction performs no migration or mutation.
- [ ] Tests can inject an Authority backed by an in-memory Store.

**Verification:**

```sh
go test ./internal/cli -run 'Test.*(Context|TaskAuthorityComposition)'
```

**Dependencies:** Checkpoint 2.

**Files likely touched:**

- `internal/cli/ctx.go`
- `internal/cli/root.go`
- `internal/cli/ctx_test.go`
- `internal/cli/task_authority_adapter_test.go`

**Estimated scope:** M.

### Task 3.2 — Cut over task creation and canonical queries

**Description:** Migrate `task add`, task show/list, and backlog add to Authority `Create`, `Get`, and `List`. Backlog and `.meta` become post-commit projections.

**Acceptance criteria:**

- [ ] One create operation produces one queued Task Generation with owner, definition, kind, and project.
- [ ] Duplicate creation returns typed conflict and creates no projection duplicate.
- [ ] Task show/list read canonical Authority records; `.meta` cannot override them.
- [ ] Projection failure returns a typed partial result while preserving the authoritative receipt for reconciliation.
- [ ] Production callers no longer invoke `home.CreateTaskAggregate`.

**Verification:**

```sh
go test ./internal/cli -run 'Test.*(TaskAdd|TaskShow|TaskList|BacklogAdd|Authoritative)'
go test ./internal/fleet -run 'Test.*TaskAggregateAuthority'
```

**Dependencies:** Task 3.1.

**Files likely touched:**

- `internal/cli/task_cmd.go`
- `internal/cli/backlog_cmd.go`
- `internal/cli/task_aggregate_authority_test.go`
- `internal/cli/lifecycle_partial.go`
- `internal/fleet/backlog_backlog.go`

**Estimated scope:** M.

### Task 3.3 — Cut over Start, Block, Unblock, Complete, and Reopen

**Description:** Replace direct home lifecycle mutations and generic backlog aggregate updates with named Authority operations.

**Acceptance criteria:**

- [ ] Backlog commands call semantic Authority methods with Expected Generation and stable Operation ID.
- [ ] Invalid transitions fail before backlog projection mutation.
- [ ] Projection failure is retryable without replaying the authoritative operation.
- [ ] `Reopen` creates a new Generation and leaves prior Generation immutable.
- [ ] `home.StartTask`, `home.UnblockTask`, `home.ReopenTask`, and generic backlog state updates have no production callers.

**Verification:**

```sh
go test ./internal/cli -run 'Test.*Backlog.*(Start|Block|Unblock|Done|Reopen|Partial)'
go test ./internal/taskauthority -run 'TestAuthority.*(Start|Block|Unblock|Complete|Reopen)'
```

**Dependencies:** Task 3.2.

**Files likely touched:**

- `internal/cli/backlog_cmd.go`
- `internal/cli/backlog_lifecycle_test.go`
- `internal/cli/lifecycle_partial.go`
- `internal/home/task_lifecycle.go`
- `internal/home/task_lifecycle_test.go`

**Estimated scope:** M.

### Task 3.4 — Make `task status` audit-only

**Description:** Stop `munsu task status` from acting as a generic authoritative state setter. It records typed activity/audit input and updates projections; authoritative transitions occur only through named operations owned by the parent rank.

**Acceptance criteria:**

- [ ] Appending a status line cannot change the Authoritative Task Aggregate phase.
- [ ] Existing typed event translation remains idempotent.
- [ ] Material Soldier reports continue through the existing parent reconciliation path.
- [ ] CLI help and typed output no longer imply that arbitrary status text mutates authoritative lifecycle.
- [ ] `home.UpdateCurrentTaskAggregateState` has no CLI caller.

**Verification:**

```sh
go test ./internal/cli -run 'Test.*TaskStatus'
go test ./internal/fleet ./internal/orchestrator -run 'Test.*(Status|Report|Reconcile)'
```

**Dependencies:** Task 3.3.

**Files likely touched:**

- `internal/cli/task_cmd.go`
- `internal/cli/task_cmd_test.go`
- `internal/orchestrator/lifecycle_lifecycle.go`
- `internal/cli/contract_fixtures/`

**Estimated scope:** M.

### Task 3.5 — Add authoritative `.meta` and `.status` reconciliation

**Description:** Materialize compatibility projections from committed aggregate and typed audit records, with explicit retry and rebuild behavior.

**Acceptance criteria:**

- [ ] `.meta` is derived from canonical aggregate/bindings and cannot write back into authority.
- [ ] `.status` is derived from typed audit/activity history and remains append-only where compatibility requires it.
- [ ] Deleting or corrupting a projection can be repaired without changing Revision or Generation.
- [ ] Projection reconciliation is idempotent and has a typed partial outcome.

**Verification:**

```sh
go test ./internal/taskauthorityfs -run 'Test.*Projection'
go test ./internal/cli -run 'Test.*(Projection|TaskShow|TaskList)'
```

**Dependencies:** Task 3.4.

**Files likely touched:**

- `internal/taskauthorityfs/projection.go`
- `internal/taskauthorityfs/projection_test.go`
- `internal/home/taskmeta.go`
- `internal/cli/task_cmd.go`

**Estimated scope:** M.

### Checkpoint 3 — First working vertical slice

- [ ] Task create/show/list and backlog lifecycle work end-to-end through Authority.
- [ ] `task status` cannot mutate authoritative phase.
- [ ] `.meta` and `.status` rebuild from canonical state/audit.
- [ ] Old create/start/unblock/reopen/generic-state production callers are gone.
- [ ] Focused CLI, taskauthority, taskauthorityfs, fleet, and orchestrator tests pass.

## Phase 4 — Spawn and generation-bound bindings

### Task 4.1 — Move Worktree Binding into Authority

**Description:** Replace the current marker-plus-aggregate two-write sequence with a named generation-bound `BindWorktree` operation inside the Authority transaction.

**Acceptance criteria:**

- [ ] Binding validates repository identity, path, Git/Common directories, head, lease, fence token, and Expected Generation.
- [ ] Lease marker and aggregate binding commit or recover together.
- [ ] Rebinding the same identity is idempotent; conflicting binding fails closed.
- [ ] `home.BindTaskWorktree` has no production caller after cutover.

**Verification:**

```sh
go test ./internal/taskauthority -run 'TestAuthorityBindWorktree'
go test ./internal/taskauthorityfs -run 'Test.*WorktreeBinding'
go test ./internal/fleet -run 'Test.*Spawn.*Worktree'
```

**Dependencies:** Checkpoint 3.

**Files likely touched:**

- `internal/taskauthority/binding.go`
- `internal/taskauthority/binding_test.go`
- `internal/taskauthorityfs/transaction.go`
- `internal/home/task_worktree_binding.go`
- `internal/fleet/spawn_runner.go`

**Estimated scope:** M.

### Task 4.2 — Implement atomic `ConfirmSpawn`

**Description:** Persist Endpoint Binding and transition queued work to working as one semantic operation after harness readiness succeeds.

**Acceptance criteria:**

- [ ] Endpoint Binding and working transition commit together.
- [ ] Failed endpoint persistence leaves the task non-working.
- [ ] Expected Generation, owner, worktree binding, and applicable Dispatch Hold are revalidated inside the transaction.
- [ ] `home.BindTaskEndpoint` and `home.UpdateCurrentTaskAggregateState(..., "working", ...)` have no production caller.

**Verification:**

```sh
go test ./internal/taskauthority -run 'TestAuthorityConfirmSpawn'
go test ./internal/fleet -run 'TestEndpointBindingOrdering|TestEndpointBinding.*LeavesTaskNonWorking'
```

**Dependencies:** Task 4.1.

**Files likely touched:**

- `internal/taskauthority/spawn.go`
- `internal/taskauthority/spawn_test.go`
- `internal/fleet/spawn_runner.go`
- `internal/fleet/spawn_endpoint_binding_test.go`
- `internal/home/task_endpoint_binding.go`

**Estimated scope:** M.

### Task 4.3 — Separate supervision gating from durable authority

**Description:** Ensure watcher/degraded checks occur in fleet/CLI orchestration before handoff/start/spawn, while Authority only evaluates durable Dispatch Holds.

**Acceptance criteria:**

- [ ] Authority tests have no watcher dependency.
- [ ] Start/spawn commands still fail closed when supervision is degraded.
- [ ] No watcher failure creates or mutates a Dispatch Hold or task phase.
- [ ] Existing home dispatch check no longer calls watcher health.

**Verification:**

```sh
go test ./internal/taskauthority -run 'Test.*Readiness'
go test ./internal/fleet ./internal/cli -run 'Test.*(Watcher|Degraded|Dispatch|Start|Spawn)'
```

**Dependencies:** Task 4.2.

**Files likely touched:**

- `internal/fleet/spawn_runner.go`
- `internal/cli/backlog_cmd.go`
- `internal/home/dispatch_control.go`
- `internal/home/dispatch_degraded_test.go`
- `internal/fleet/dispatch_control_test.go`

**Estimated scope:** M.

### Checkpoint 4 — Spawn invariant cluster

- [ ] Worktree and Endpoint Bindings are authoritative generation-bound records.
- [ ] `ConfirmSpawn` is atomic and semantic.
- [ ] Watcher health remains outside Authority.
- [ ] Spawn-focused tests and race tests pass.

## Phase 5 — Durable dispatch-control cutover

### Task 5.1 — Move Dispatch Interpretation rules

**Description:** Move dependency digesting, divergence classification, autonomy rules, and interpretation outcomes into Task Authority.

**Acceptance criteria:**

- [ ] Interpretation identity/digest is deterministic.
- [ ] Safe reinterpretation and material ambiguity follow ADR-0004.
- [ ] Decision-required interpretation atomically stages its Decision, Hold, and audit event.
- [ ] Home adapter contains serialization only, not interpretation rules.

**Verification:**

```sh
go test ./internal/taskauthority -run 'Test.*InterpretDispatch'
```

**Dependencies:** Checkpoint 4.

**Files likely touched:**

- `internal/taskauthority/interpretation.go`
- `internal/taskauthority/interpretation_test.go`
- `internal/home/dispatch_evaluator.go`
- `internal/home/dispatch_evaluator_test.go`

**Estimated scope:** M.

### Task 5.2 — Move Decision and Hold lifecycle callers

**Description:** Cut CLI/fleet callers over to `CreateHold`, `ReleaseHold`, and `ResolveDecision`; remove direct home dispatch mutations.

**Acceptance criteria:**

- [ ] Hold scopes compose conservatively and release is idempotent.
- [ ] Resolving a Decision does not auto-start queued work.
- [ ] All human and automatic dispatch paths evaluate Authority holds.
- [ ] Direct production calls to `home.CreateDispatchHold`, `home.ReleaseDispatchHold`, `home.ResolveDispatchDecision`, and `home.CheckDispatchHold` are gone.

**Verification:**

```sh
go test ./internal/cli ./internal/fleet -run 'Test.*(DecisionHold|DispatchHold|Interpretation)'
go test -race ./internal/taskauthority -run 'Test.*Dispatch'
```

**Dependencies:** Task 5.1.

**Files likely touched:**

- `internal/cli/decisionhold_cmd.go`
- `internal/fleet/decisionhold.go`
- `internal/fleet/dispatch_control_test.go`
- `internal/home/dispatch_control.go`
- `internal/home/dispatch_control_test.go`

**Estimated scope:** M.

### Task 5.3 — Delete home dispatch authority

**Description:** Remove lifecycle and dispatch business rules from `internal/home`, retaining only state that still belongs to home mechanics during the remaining migration.

**Acceptance criteria:**

- [ ] `internal/home` exports no Task lifecycle, readiness, Dispatch Hold, Dispatch Interpretation, or Dispatch Decision mutation operation.
- [ ] Dispatch serialization and paths exist only in `taskauthorityfs`.
- [ ] Grep-based architecture tests show zero production callers of removed symbols.
- [ ] No behavior test is deleted without a replacement through the Authority interface.

**Verification:**

```sh
go test ./internal/taskauthority ./internal/taskauthorityfs ./internal/home ./internal/fleet ./internal/cli
go build ./...
go vet ./...
```

**Dependencies:** Task 5.2.

**Files likely touched:**

- `internal/home/dispatch_control.go`
- `internal/home/dispatch_evaluator.go`
- `internal/home/task_lifecycle.go`
- corresponding tests

**Estimated scope:** M.

### Checkpoint 5 — Single lifecycle/dispatch authority

- [ ] Task Authority owns all local lifecycle, readiness, and durable dispatch rules.
- [ ] Home owns none of those rules.
- [ ] Start/Hold race, interpretation atomicity, and crash recovery tests pass.
- [ ] Architecture allowlist for old local mutations is empty.

## Phase 6 — Cross-home handoff saga

### Task 6.1 — Define durable transfer intent and receipt

**Description:** Model handoff as a fleet-owned saga across source and destination Authorities without introducing a distributed filesystem transaction.

**Acceptance criteria:**

- [ ] Transfer intent binds source/destination home identity, Task ID, exact Generation, request digest, and Operation IDs.
- [ ] Destination receipt is durable and idempotent.
- [ ] Failure before destination receipt leaves source ownership current.
- [ ] A conflicting destination owner quarantines/fails closed.

**Verification:**

```sh
go test ./internal/fleet -run 'Test.*Handoff.*(Intent|Receipt|Conflict|SourcePreserved)'
```

**Dependencies:** Checkpoint 5.

**Files likely touched:**

- `internal/fleet/task_handoff_transaction.go`
- `internal/fleet/task_handoff_test.go`
- `internal/taskauthority/transfer.go`
- `internal/taskauthority/transfer_test.go`

**Estimated scope:** M.

### Task 6.2 — Cut handoff over to two Authorities

**Description:** Replace direct aggregate/projection copying with source/destination Authority operations and durable saga recovery.

**Acceptance criteria:**

- [ ] Destination receives the complete same Task Generation before source retirement.
- [ ] Retry resumes from durable receipts without duplicate task creation.
- [ ] Projection copy failures do not change ownership truth.
- [ ] Cross-home resolution remains in fleet and collects all candidate owners.

**Verification:**

```sh
go test ./internal/fleet -run 'Test.*Handoff'
go test -tags=integration ./internal/fleet -run 'Test.*Handoff'
```

**Dependencies:** Task 6.1.

**Files likely touched:**

- `internal/fleet/task_handoff_transaction.go`
- `internal/fleet/backlog_backlog.go`
- `internal/fleet/task_handoff_test.go`
- `internal/fleet/captain_handoff_integration_test.go`
- `internal/taskauthority/transfer.go`

**Estimated scope:** M.

### Checkpoint 6 — Ownership transfer

- [ ] Handoff preserves one current owner and one Task Generation.
- [ ] Failed or interrupted handoff resumes without dual ownership.
- [ ] Integration failures, if any, are distinguished from the pre-existing tagged-test compile baseline.

## Phase 7 — Delivery and remaining projection writes

### Task 7.1 — Inventory and classify direct `.meta` writes

**Description:** Classify every production `fleet → home.WriteMeta` call as authoritative task definition/lifecycle/binding data, runtime projection data, or unrelated Captain metadata before changing it.

**Acceptance criteria:**

- [ ] Every production write has one named owner and destination record.
- [ ] Authoritative task fields map to a semantic Authority operation.
- [ ] Runtime-only projection fields remain outside Aggregate and cannot influence lifecycle.
- [ ] Captain supervisor metadata is not forced into Task Authority merely because it shares `.meta` format.

**Verification:**

```sh
rg 'home\.WriteMeta|mhome\.WriteMeta' internal/fleet internal/cli
```

**Dependencies:** Checkpoint 6.

**Files likely touched:** None initially; classification belongs in implementation notes or task evidence.

**Estimated scope:** S.

### Task 7.2 — Move Issue Link mutations

**Description:** Replace direct `.meta` writes for generation-bound Issue links and reconciliation outcomes with named Authority operations.

**Acceptance criteria:**

- [ ] Every Issue Link mutation binds exact Task Generation and Operation ID.
- [ ] Parent and related links cannot be promoted to automatic closure policy.
- [ ] Reconciliation retries are idempotent and preserve provider evidence.
- [ ] `delivery_issuelinks.go` no longer writes task `.meta` directly.

**Verification:**

```sh
go test ./internal/fleet -run 'Test.*IssueLink'
```

**Dependencies:** Task 7.1.

**Files likely touched:**

- `internal/fleet/delivery_issuelinks.go`
- `internal/fleet/delivery_issuelinks_test.go`
- `internal/taskauthority/definition.go`
- `internal/taskauthority/definition_test.go`

**Estimated scope:** M.

### Task 7.3 — Move Delivery Plan and capability-attestation references

**Description:** Replace authoritative delivery mode and attestation `.meta` writes with generation-bound definition records and named Authority operations. Runtime capability observations remain outside the aggregate unless accepted as authoritative evidence.

**Acceptance criteria:**

- [ ] Requested/effective Delivery Plan and allowed transitions are revisioned within the Task Generation.
- [ ] Capability attestation references bind exact project, home, config digest, and Task Generation.
- [ ] Runtime observation data cannot silently become authoritative task definition.
- [ ] `delivery_attestation.go` no longer writes task `.meta` directly.

**Verification:**

```sh
go test ./internal/fleet -run 'Test.*(DeliveryPlan|Attestation)'
```

**Dependencies:** Task 7.2.

**Files likely touched:**

- `internal/fleet/delivery_attestation.go`
- `internal/fleet/delivery_attestation_test.go`
- `internal/taskauthority/definition.go`
- `internal/taskauthority/definition_test.go`

**Estimated scope:** M.

### Task 7.4 — Move merge and Git authorization records

**Description:** Replace direct task `.meta` mutation for merge and Git authorization with explicit generation/head-bound records and named Authority operations.

**Acceptance criteria:**

- [ ] Merge authorization binds Task Generation, provider identity, PR identity, and immutable head SHA.
- [ ] Changed head invalidates stale authorization.
- [ ] Git authorization binds the exact generation, worktree binding, capability tier, and expected pre-state.
- [ ] Authorization writes are idempotent and directly auditable.

**Verification:**

```sh
go test ./internal/fleet -run 'Test.*(MergeAuthorization|GitAuthorization)'
```

**Dependencies:** Task 7.3.

**Files likely touched:**

- `internal/fleet/merge_authorization.go`
- `internal/fleet/merge_authorization_test.go`
- `internal/fleet/git_authorization.go`
- `internal/fleet/git_authorization_test.go`
- `internal/taskauthority/authorization.go`
- `internal/taskauthority/authorization_test.go`

**Estimated scope:** M.

### Task 7.5 — Move delivery preparation and terminal transitions

**Description:** Route PrepareDelivery and delivered/done terminal transitions through named Authority operations rather than generic state or metadata writes.

**Acceptance criteria:**

- [ ] Ship completion cannot use `resolved` as delivery completion.
- [ ] Provider evidence binds exact Task Generation, provider identity, and head SHA.
- [ ] Failed verification leaves the prior authoritative phase unchanged.
- [ ] Delivered/done transitions emit typed audit events and idempotency receipts.

**Verification:**

```sh
go test ./internal/fleet -run 'Test.*(PrepareDelivery|Delivered|Terminal)'
```

**Dependencies:** Task 7.4.

**Files likely touched:**

- `internal/fleet/delivery_terminal.go`
- `internal/fleet/delivery_terminal_test.go`
- `internal/taskauthority/delivery.go`
- `internal/taskauthority/delivery_test.go`

**Estimated scope:** M.

### Task 7.6 — Move merge outcomes into Authority

**Description:** Persist Merge Attempt and reconciled merge outcomes through named Authority operations while preserving remote-unknown safety.

**Acceptance criteria:**

- [ ] Each merge attempt has a stable Operation ID and exact generation/head binding.
- [ ] `remote_unknown` forbids repeated provider mutation and permits only read reconciliation.
- [ ] Already-merged and provider false-negative outcomes are idempotent.
- [ ] Partial metadata failure does not erase verified remote truth.

**Verification:**

```sh
go test ./internal/fleet -run 'Test.*(MergeAttempt|MergeOps|PRMerge)'
```

**Dependencies:** Task 7.5.

**Files likely touched:**

- `internal/fleet/delivery_mergeops.go`
- `internal/fleet/delivery_mergeops_test.go`
- `internal/fleet/delivery_prmerge.go`
- `internal/fleet/delivery_prmerge_test.go`
- `internal/taskauthority/delivery.go`

**Estimated scope:** M.

### Task 7.7 — Move retirement transition into Authority

**Description:** Make retirement a distinct named Authority operation after external merge/reconciliation, so cleanup retry never reruns merge.

**Acceptance criteria:**

- [ ] Retirement requires exact Task Generation and verified prerequisites.
- [ ] Cleanup failure preserves merged truth and returns a resumable retirement receipt.
- [ ] Retry resumes retirement only.
- [ ] Retired phase and audit event commit once.

**Verification:**

```sh
go test ./internal/fleet -run 'Test.*(RetireTask|MergeAndRetire|Retirement)'
```

**Dependencies:** Task 7.6.

**Files likely touched:**

- `internal/fleet/retirement_task.go`
- `internal/fleet/retirement_task_test.go`
- `internal/fleet/delivery_mergeandretire.go`
- `internal/fleet/delivery_mergeandretire_test.go`
- `internal/taskauthority/retirement.go`
- `internal/taskauthority/retirement_test.go`

**Estimated scope:** M.

### Task 7.8 — Remove remaining task projection reach-through

**Description:** Replace remaining task-related direct `home.WriteMeta`, aggregate writes, and status-authority reads with Authority queries or projection reconciliation.

**Acceptance criteria:**

- [ ] No production file in `internal/fleet` or `internal/cli` directly writes Authoritative Task Aggregate files or task `.meta` state.
- [ ] Snapshot and task observation prefer canonical Authority state.
- [ ] `.status` cannot override newer authoritative lifecycle.
- [ ] Captain-specific metadata remains explicitly outside the task-authority grep gate.

**Verification:**

```sh
rg 'WriteTaskAggregate|UpdateCurrentTaskAggregate|CreateTaskAggregate|StartTask|UnblockTask|ReopenTask|CreateDispatchHold|ReleaseDispatchHold|ResolveDispatchDecision|CheckDispatchHold' internal/fleet internal/cli

go test ./internal/fleet ./internal/cli -run 'Test.*(Snapshot|Observe|Projection|Authority)'
```

**Dependencies:** Task 7.7.

**Files likely touched:**

- `internal/fleet/snapshot.go`
- `internal/fleet/soldierstate_soldierstate.go`
- `internal/cli/contract_commands.go`
- `internal/fleet/home_summary.go`
- corresponding tests

**Estimated scope:** M.

### Checkpoint 7 — Fleet mutation cutover

- [ ] Delivery and task projections no longer form competing mutation authority.
- [ ] Any remaining `.meta` writes are classified as non-task runtime/Captain projection data.
- [ ] Task lifecycle, delivery, and bindings all bind exact Task Generation.
- [ ] Focused delivery, snapshot, and observation tests pass.

## Phase 8 — Final deletion, documentation, and gates

### Task 8.1 — Delete legacy home lifecycle and binding code

**Description:** Remove obsolete lifecycle, readiness, endpoint-binding, and worktree-binding implementations after all production callers and behavior tests use Authority.

**Acceptance criteria:**

- [ ] `internal/home` retains no lifecycle/readiness mutation or generation-bound binding authority.
- [ ] Old behavior tests are replaced through the Authority interface, not simply removed.
- [ ] No orphaned helpers or imports remain.
- [ ] Focused home/taskauthority/fleet tests stay green.

**Verification:**

```sh
go test ./internal/home ./internal/taskauthority ./internal/taskauthorityfs ./internal/fleet
```

**Dependencies:** Checkpoint 7.

**Files likely touched:**

- `internal/home/task_lifecycle.go`
- `internal/home/task_endpoint_binding.go`
- `internal/home/task_worktree_binding.go`
- corresponding tests

**Estimated scope:** M.

### Task 8.2 — Delete legacy aggregate store and migration code

**Description:** Remove v1 aggregate storage/migration implementations once v2 migration and adapter own all supported state paths.

**Acceptance criteria:**

- [ ] `internal/home` retains no Authoritative Task Aggregate reader/writer or task migration implementation.
- [ ] Supported migration remains explicit through `taskauthorityfs`.
- [ ] Existing migration receipts and archives remain readable where required for verification.
- [ ] Net code growth is reviewed against deletion of duplicate stores and reach-through tests.

**Verification:**

```sh
go test ./internal/home ./internal/taskauthorityfs ./internal/cli -run 'Test.*Migration'
go build ./...
go vet ./...
```

**Dependencies:** Task 8.1.

**Files likely touched:**

- `internal/home/task_aggregate.go`
- `internal/home/task_aggregate_store.go`
- `internal/home/task_aggregate_migration.go`
- corresponding tests

**Estimated scope:** M.

### Task 8.3 — Update repository architecture documentation

**Description:** Update documentation only after the code paths are real.

**Acceptance criteria:**

- [ ] `docs/architecture.md` describes `taskauthority` and `taskauthorityfs` accurately.
- [ ] `docs/port-mapping.md` maps canonical task lifecycle and dispatch ownership to the new module.
- [ ] `AGENTS.md` stale task/delivery ownership notes are revised without overwriting unrelated pre-existing edits.
- [ ] ADR-0007 status changes only if the implementation is complete.

**Verification:**

```sh
git diff --check
```

**Dependencies:** Task 8.2.

**Files likely touched:**

- `docs/architecture.md`
- `docs/port-mapping.md`
- `AGENTS.md`
- `docs/adr/0007-task-authority-deep-module-and-transactional-store.md`

**Estimated scope:** S.

### Task 8.4 — Run final architecture and delivery gates

**Description:** Verify behavior, architecture, migration, crash recovery, race safety, and repository cleanliness before delivery.

**Acceptance criteria:**

- [ ] Architecture grep gates find no forbidden production call paths.
- [ ] Both Store adapters pass contract tests and `-race`.
- [ ] Crash tests pass at every journal stage.
- [ ] Migration plan/apply and already-migrated paths pass.
- [ ] Full build, vet, and tests pass or every remaining failure is proven identical to the Phase-0 baseline.
- [ ] No-mistakes passes before push/PR.

**Verification:**

```sh
gofmt -w <changed-go-files>
go build ./...
go vet ./...
go test -race ./internal/taskauthority ./internal/taskauthorityfs
go test ./internal/home ./internal/fleet ./internal/cli ./internal/orchestrator
go test ./...
```

Then run the no-mistakes pipeline.

**Dependencies:** Task 8.3.

**Files likely touched:** None beyond fixes required by failing gates.

**Estimated scope:** M.

## Parallelization policy

Most work is sequential because later slices delete or replace the same authority paths.

Safe parallel work after the relevant contract is fixed:

* Store contract adversarial review while the filesystem adapter is implemented.
* Independent crash-injection tests against an already-defined transaction manifest.
* Projection tests after canonical audit/event schemas are fixed.
* Documentation update only after code cutover is complete.

Not safe to parallelize:

* Two agents editing `taskauthority` lifecycle rules simultaneously.
* Filesystem migration and transaction schema changes in separate branches without one owner.
* Spawn, dispatch, handoff, or delivery cutovers before the preceding interface is merged.
* Any task where one agent retains an old mutation path while another activates the new one.

When delegation is used, create detached/top-level Paseo agents and give each agent one bounded task with exclusive file ownership.

## Risk register

| Risk | Impact | Mitigation |
|---|---|---|
| v1 state is mutated before explicit migration | High | Detect-only runtime; Task 2.5 precedes composition activation |
| Journal recovery exposes partial state | High | Fail injection at every stage; canonical reads recover first |
| Start races a concurrent Dispatch Hold | High | One Store update envelope and deterministic barrier test |
| Generic setters survive as compatibility | High | Architecture allowlist shrinks to zero; delete in same slice |
| Task Revision becomes leaked caller protocol | Medium | Expected Generation by default; revision precondition only for explicit exact-snapshot operations |
| Projection failure is reported as authority failure | Medium | Durable receipt distinguishes committed authority from retryable projection failure |
| `taskauthority` grows into a god module | Medium | Named method deletion test; cross-home orchestration and watcher supervision remain outside |
| `taskauthorityfs` duplicates generic home helpers | Low | Keep only adapter-specific filesystem mechanics; do not export shallow home pass-throughs |
| Delivery cutover becomes too large | High | Split Task 7.2 and 7.3 by invariant/transaction cluster |
| Pre-existing tests mask regressions | Medium | Phase-0 baseline and focused package gates |
| Existing dirty `AGENTS.md` is overwritten | Medium | Do not edit until Task 8.2; preserve unrelated hunks exactly |

## Explicit non-goals

* No database or full event-sourcing adoption.
* No scoped-lock optimization before contention is measured.
* No new watcher implementation or supervision protocol.
* No redesign of provider merge semantics beyond routing authoritative mutations through Task Authority.
* No migration of Captain supervisor metadata merely because it uses `.meta` files.
* No permanent dual-read or dual-write compatibility path.
* No broad five-package split of `internal/fleet`; reassess fleet depth after this cutover.

## Review checkpoints requiring explicit continuation

Implementation should pause for review after Checkpoints 1, 2, 3, 5, and 7. Passing a checkpoint authorizes planning of the next slice, not automatic implementation beyond the user's explicit instruction.
