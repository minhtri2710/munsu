# Task Authority Deep-Module Implementation Plan

* **Date:** 2026-08-01
* **Status:** Implementation in progress; Checkpoint 3 complete
* **Source:** [ADR-0007](../adr/0007-task-authority-deep-module-and-transactional-store.md)
* **Related:** [ADR-0002](../adr/0002-deep-module-clean-break-and-durable-lifecycle.md), [ADR-0004](../adr/0004-authoritative-task-lifecycle-delivery-and-projections.md), [ADR-0005](../adr/0005-runtime-bindings-supervision-recovery-and-mutation-fencing.md), [ADR-0006](../adr/0006-state-migration-build-provenance-and-compatibility-gates.md), [incident remediation master plan](2026-07-30-incident-remediation-master-plan.md)
* **Delivery mode:** no-mistakes; test-first; vertical slices; no dual mutation authority

## Implementation progress

Checkpoint 1 is complete through commit `fca1b38`. Task 2.1 is complete through commits `802ab768` and `6df038ed`. Task 2.2 is complete through commits `69b5a5fc` and `e894f2cf`. Task 2.3 is complete through commits `9dfd902a` and `7fe3d3f9`. Task 2.4 is complete through commit `e172c595`. Task 2.5 is complete through commits `47eaf25a` and `75355dcb`; Checkpoint 2 is complete through `8d07b12`. Task 3.1 is complete through commit `db5c1445`. Task 3.2 is complete through commit `345338b5`: `task add`, `task show`, `task list`, and `backlog add` create and query canonical Task Authority records through the composed Authority; `.meta` and the backlog file are post-commit projections (a projection failure returns a typed `LifecyclePartialError` and never rolls back the authoritative Task Generation); duplicate creation returns a typed conflict with no projection duplicate; the CLI migration allowlist drops `CreateTaskAggregate` for `task_cmd.go`/`backlog_cmd.go`. Fleet `spawn_runner.go` keeps the allowlisted `home.CreateTaskAggregate` call until the Phase 4 spawn cutover, so that function remains exported. Task 3.3 is complete through commit `1c262402`: `backlog start|done|block|unblock|reopen` drive the named Authority `Start`/`Complete`/`Block`/`Unblock`/`Reopen` operations with Expected Generation and a stable invocation Operation ID; invalid transitions fail inside the Authority before the backlog projection is touched; a projection failure returns a typed `LifecyclePartialError` that is retryable without replaying the authoritative operation; `Reopen` creates the next queued Generation at Revision one and leaves the prior Generation immutable historical state; `home.StartTask`, `home.UnblockTask`, and `home.ReopenTask` are deleted in the same slice (their last callers moved), and no production caller of a generic backlog state update remains. `home.SupersedeTask` stays for `backlog retry`, which is outside Task 3.3's named scope. The pre-existing `TestAgentSkillMirrorsMatchCanonical` failure reproduces on the clean base and is unrelated to this slice.

Task 3.4 is complete: `task status` is audit-only. Appending a status line never mutates the Authoritative Task Aggregate phase (proven against a seeded legacy v1 aggregate and against a canonical Authority record, whose Phase and Revision stay untouched); the existing typed event translation is preserved and pinned by an event-log assertion; help and typed output state the audit-only contract; `home.UpdateCurrentTaskAggregateState` has no CLI caller and its entry left the CLI allowlist (only `spawn_cmd.go` -> `UpdateCurrentTaskAggregateKind` remains for Phase 4). The CLI allowlist gate, `Test.*TaskStatus`, and the fleet/orchestrator `Test.*(Status|Report|Reconcile)` suites are green, confirming material Soldier reports still flow through the existing parent reconciliation path.

Task 3.5 is complete: `.meta` and `.status` are reconciled as one-directional projections in `internal/taskauthorityfs` (`ReconcileTaskProjections` for one task, `ReconcileProjections` for all). `.meta` authoritative fields (owner, description, kind, project, generation, state) are derived from the canonical current Task Aggregate; runtime-only projection fields are preserved and empty canonical values drop stale keys. `.status` lines are derived from the typed lifecycle audit history ("<after>: <reason>" in committed audit order); a deleted or corrupt projection is rebuilt, an existing file stays append-only with legacy runtime lines preserved and missing derived lines appended. Repair never touches the authority: aggregate documents, current pointer, and audit events are byte-identical after a pass, and Revision/Generation are pinned in the typed outcome. Reconciliation is idempotent (a second pass reports `unchanged` and rewrites nothing), fails closed on legacy v1 state (`ErrMigrationRequired`, never lazy), and returns a typed partial outcome (`ProjectionFailed` per projection) when a repair cannot complete, leaving the authority intact. `home.WriteStatus` (atomic, locked) is the rebuild primitive beside `AppendStatus`. The CLI surface is `munsu task reconcile [id]` with a typed retryable `ProjectionPartialError` (`projection_failed`) when any projection cannot be repaired.

Verified on 2026-08-01 (Task 3.5):

```sh
go build ./...
go vet ./...
go test -race ./internal/taskauthority ./internal/taskauthorityfs
go test ./internal/home ./internal/fleet ./internal/cli ./internal/orchestrator
go test ./internal/taskauthorityfs -run 'Test.*Projection'
go test ./internal/cli -run 'Test.*(Projection|TaskShow|TaskList)'
go test ./... -skip TestAgentSkillMirrorsMatchCanonical
go test -tags=integration ./... -skip TestAgentSkillMirrorsMatchCanonical
```

All commands passed; the integration-tagged run is green across all 13 packages (the previously recorded pre-existing CLI/fleet config-migration and Captain config-push fixture failures did not reproduce). The only skipped test is the documented pre-existing `TestAgentSkillMirrorsMatchCanonical` failure, reproduced on the clean base and unrelated to this slice.

Checkpoint 3 is complete. Independent Reviewer acceptance (read-only audit role) verified all five checkpoint criteria at commit `83214861`: task create/show/list and backlog lifecycle route through the composed Authority (`Ctx.TaskAuthority()`); `task status` is audit-only and cannot mutate authoritative phase; `.meta` and `.status` rebuild from canonical state/audit (idempotent, fail-closed `ErrMigrationRequired` on legacy v1, typed partial outcomes); the only remaining old-mutation production callers are the documented Phase 4/6 allowlist entries (`spawn_runner.go` create/working-update/dispatch-check and handoff dispatch-check, plus `migrate_cmd.go` migration tooling); allowlist gate tests (`TestNoNewTaskAuthorityReachThrough`) pass in CLI and fleet; `home.StartTask`, `home.UnblockTask`, and `home.ReopenTask` are deleted. The worktree-sanctioned skip `TestAgentSkillMirrorsMatchCanonical` is the only failing test and is confirmed branch-untouched.

Verified on 2026-08-02 (Checkpoint 3):

```sh
go build ./...
go vet ./...
go test ./internal/taskauthority ./internal/taskauthorityfs
go test -race ./internal/taskauthority ./internal/taskauthorityfs
go test ./internal/taskauthorityfs -run 'Test.*Projection'
go test ./internal/cli -run 'Test.*(Projection|TaskShow|TaskList|TaskStatus)'
go test ./internal/cli -run 'Test.*(TaskAdd|TaskShow|TaskList|BacklogAdd|Authoritative)'
go test ./internal/cli -run 'Test.*Backlog.*(Start|Block|Unblock|Done|Reopen|Partial)'
go test ./internal/fleet ./internal/orchestrator -run 'Test.*(Status|Report|Reconcile)'
go test ./internal/home ./internal/fleet ./internal/cli ./internal/orchestrator -skip TestAgentSkillMirrorsMatchCanonical
go test ./... -count=1 -skip TestAgentSkillMirrorsMatchCanonical
go test -tags=integration ./... -count=1 -skip TestAgentSkillMirrorsMatchCanonical
```

All commands passed; the only skipped test is the documented pre-existing `TestAgentSkillMirrorsMatchCanonical` (branch-untouched, contract-sanctioned worktree skip).

Task 4.1 is complete through commit `bc731f8e`. Independent Reviewer acceptance verified all four criteria: `Authority.BindWorktree` is a named generation-bound operation validating the full payload (repository identity, path, Git/Common dirs, head, lease, fence token, bound timestamp) with a stable Task Operation ID; the lease marker and aggregate binding commit/recover atomically through the `taskauthorityfs` journal (crash matrix at all 10 journal stages, canonical `View` never exposes a partial binding); same-op replay is idempotent, a duplicate Operation ID with changed intent conflicts non-retryably, and any rebind of an already-bound generation fails closed; `home.BindTaskWorktree` is deleted with zero production callers and the allowlist shrunk in the same slice (`TestNoNewTaskAuthorityReachThrough` green in fleet and cli). Spawn wiring injects the composed Authority via `fleet.Args.Authority` from the CLI composition root with `HomeDir: ctx.Home`; `home.TaskWorktreeLeaseActive` read path preserved against the v2 marker namespace; `internal/taskauthority` retains zero filesystem imports. Confirmed transitions: v1 lease path remains a v1-detection location, migration does not yet emit v2 markers (pre-existing), and spawn on v1-state homes fails closed at bind with `ErrMigrationRequired` until Task 4.2 completes the spawn cutover.

Task 4.2 is complete through commit `b2c2cbeb`. Independent Reviewer acceptance verified all four criteria: `Authority.ConfirmSpawn` commits Endpoint Binding + queued→working (reason `spawned`) + exactly-one Revision advance + typed audit event + durable idempotency receipt in one Store update envelope, revalidating Expected Generation, owner presence, worktree binding, and applicable durable Dispatch Holds inside the transaction (Start/Hold atomicity pattern, no check-commit race); failed persistence leaves the task non-working (crash matrix: before-manifest → queued/unbound, later stages converge to working/bound); `home.BindTaskEndpoint` and `home.UpdateCurrentTaskAggregateState(..., "working", ...)` are deleted with zero production callers and the allowlist/gate symbol list shrunk in the same slice. Fleet cutover: `bindEndpoint` + `markWorkingAfterBinding` collapsed into one `confirmSpawn` call (stable Operation ID `spawn-confirm-<id>-<gen>`, `spawnActor`, fail-closed without a composed Authority); the `.meta` projection remains a non-authoritative side file written before the authoritative transition. The `chore(paseo)` optimization commit `b23388c4` (peer handoffs and verification tiers) is cherry-picked onto the branch above this slice.

Task 4.3 is complete through commit `062a154f`. Independent Reviewer acceptance verified all four criteria: `internal/taskauthority`/`internal/taskauthorityfs` have zero watcher references (deliberately absent and documented in `readiness.go`); start, spawn, and handoff fail closed on degraded supervision via explicit fleet/CLI gates (`fleet.CheckSupervisionForDispatch` wrapping `home.CheckWatcherHealthForDispatch`, run before any Authority/Store call — start at `backlog_cmd.go` before `auth.Start`, spawn pre-flight sites in `spawn_runner.go`, handoff gating both homes before the saga); watcher failure returns typed `ErrUnhealthyWatcher` and never creates/mutates a Dispatch Hold or task phase (asserted by fresh tests); `home.CheckDispatchHold` is holds-only and no longer calls watcher health. The spawn durable-hold pre-flight is gone — durable holds are evaluated atomically inside `ConfirmSpawn`; the start path gained the plan-required watcher gate (pre-cutover `home.StartTask` never had one); handoff keeps holds-only `home.CheckDispatchHold` through Phase 6. Lock order preserved and the architecture allowlist verified (spawn_runner caller entry removed; handoff keeps `CheckDispatchHold`); no docs/plan changes in the slice.

Checkpoint 4 is complete. Independent Reviewer acceptance (full verification tier) verified all four criteria at `fabdf8e8`: Worktree and Endpoint Bindings are authoritative generation-bound aggregate records inside the `taskauthorityfs` Store (lease marker committed atomically with the binding; `home.BindTaskWorktree`/`BindTaskEndpoint`/`UpdateCurrentTaskAggregateState` zero production hits); `ConfirmSpawn` is a single-envelope named semantic operation (fence + owner + worktree + queued + unbound + applicable holds revalidated inside the transaction, one Revision advance, typed audit, durable receipt, replay/conflict semantics, crash recovery leaves task queued or working); watcher health is deliberately absent from Authority (sole `readiness.go` comment; gates live in fleet/CLI orchestration and never touch holds or phases); full unit suite (13/13 packages), full integration suite, `-race` taskauthority/taskauthorityfs, spawn-focused tests (12), and the crash/migration/transaction matrix are all green with only the worktree-sanctioned skip `TestAgentSkillMirrorsMatchCanonical`. Architecture grep gates: only the documented allowlisted callers remain (`spawn_runner.go` CreateTaskAggregate for Phase 4 transition, handoff `CheckDispatchHold` through Phase 6); `TestNoNewTaskAuthorityReachThrough` passes in fleet and cli.

Verified on 2026-08-02 (Checkpoint 4):

```sh
go build ./...
go vet ./...
go test -race ./internal/taskauthority ./internal/taskauthorityfs
go test ./internal/fleet -run 'Test.*Spawn'
go test ./... -count=1 -skip TestAgentSkillMirrorsMatchCanonical
go test -tags=integration ./... -count=1 -skip TestAgentSkillMirrorsMatchCanonical
```

All commands passed; the only skipped test is the documented pre-existing `TestAgentSkillMirrorsMatchCanonical` (worktree-sanctioned skip). Orchestration policy was centralized globally during this phase: the three primary commits `7f3bf7a` (acknowledged direct Peer reports), `79a968b` (JSON validity), and `3e0e44a` (delete repo-local workflow policy) are cherry-picked onto the branch (`6e755322`, `58e1cccc`, `eae89601`); policy now lives only in `~/.paseo/orchestration-preferences.json`.

Task 5.1 is complete through commits `930c4410` (initial) and `8c72f55b` (repair after REOPEN). Independent Reviewer acceptance verified all four criteria after one reopen cycle: the first review REOPENed on criterion 2 — a dependency edge whose prerequisite exists in canonical state but outside the requested set was wrongly classified decision-required (both adapters populated the canonical read set with requested tasks only, whereas pre-move home re-read prerequisites from disk, so existence in canonical state — not requested-set membership — was the ambiguity criterion). The bounded repair (`8c72f55b`) expanded the canonical read set in both the home adapter (`gatherInterpretationSnapshot`) and `Authority.interpretInTx` (parity) to load every snapshot-referenced task, restoring existence-based ambiguity semantics byte-for-byte; six regression tests (home A/B/D, Authority parity A/B/D, fleet probe E handoff of a child alone with an in-source parent) failed before the fix and pass after. Re-review confirmed probes A/B/E match pre-move outcomes with C/D controls unchanged and interpretation IDs/digests byte-identical (handoff journal compatibility holds). Final state: `Authority.InterpretDispatch` is a named semantic operation reading fresh canonical state in one Store transaction, deriving the dependency snapshot when not supplied, computing the deterministic interpretation identity and dependency-snapshot digest, classifying safe reinterpretation vs material ambiguity per ADR-0004, persisting the interpretation record, and atomically staging Decision + Hold + typed dispatch audit when decision-required (barrier race test plus the `taskauthorityfs` five-document journal test); interpretation rules live only in `internal/taskauthority/interpretation.go` (grep-proven) with `internal/home` reduced to a serialization-only adapter; the sole handoff caller (`task_handoff_transaction.go:188`) stays byte-compatible. Seam adjudication: the Lead's mechanism ruling (home adapter composing `taskauthorityfs.Store`) was amended to pure-engine delegation — `taskauthorityfs` imports `internal/home` (migration/projection), so home→taskauthorityfs would cycle, and the handoff path operates on legacy v1 homes where the fs Store fails closed with `ErrMigrationRequired`; accepted as a local patch with the fs→home import cycle tracked for the Phase 6/8 resolution.

Task 5.2 is complete through commits `2f7a74cb` (initial) and `63d7ec84` (repair after REOPEN). Independent Reviewer acceptance verified all four criteria after one reopen cycle: the first review REOPENed because the CLI cutover of `decision-hold complete` dropped the `resolved:` status projection append (old `fleet.Complete` had it), so complete→verify failed with "unresolved decisions remain" and scout retirement was blocked on the stale `needs-decision` line. The bounded repair (`63d7ec84`) re-appends `resolved: recorded (decision noted) [key=<key>]` in `newDecisionHoldCompleteCmd` mirroring the resolve path (ADR-0007 §7 projection semantics — a projection failure never rolls back the authoritative release), with a regression test (`TestDecisionHoldCompleteAppendsResolvedProjection`) that provably fails on the pre-fix parent and passes after; the Reviewer independently reproduced the complete→verify and complete→retire chains as restored. Final state: `Authority.ResolveDecision` is a named operation resolving the Dispatch Decision and releasing its matching hold in one Store transaction without auto-starting queued work (idempotent replay, `ErrOperationConflict` on changed digest, typed audit); the CLI `decision-hold` commands route through the Authority via the composition root (legacy `<home>/holds/*.hold` path gets no new authoritative writes; v1 homes fail closed with typed migration-required); `internal/fleet/decisionhold.go` is deleted; `home.CreateDispatchHold`/`ReleaseDispatchHold`/`ResolveDispatchDecision` are deleted with the gate allowlist shrunk; `home.CheckDispatchHold` remains solely for the Phase 6 handoff saga. Adjudicated caveats: the CLI ResolveDecision branch is organically unreachable until decision-backed CLI holds exist (tracked forward-compat bridge); scout retirement safety reads the status projection (teardown is not a held action; Authority threading tracked as optional follow-up); the fleet.snapshot unresolved-hold count under-reports on legacy v1 homes (read-only metric; enforcement paths fail closed).

Task 5.3 is complete through commit `4e50da6f`. Independent Reviewer acceptance verified all four criteria: `internal/home` exports no lifecycle/readiness/dispatch mutation operation — deleted `SupersedeTask`, `ListReadyTaskAggregates`, `mutateCurrentTaskAggregate(ed/Locked)`, `supersedeGenerations`, `lifecyclePrecondition`/`ErrTaskLifecyclePrecondition`, unlocked read/write helpers, and the zero-caller `EvaluateDispatch` wrapper, with remaining exports being reads or the Lead-adjudicated Phase 6 handoff adapter (`QueryTaskReadiness` read, `CheckDispatchHold` holds-only read, `EvaluateDispatchWithDependencies`/`PersistDispatchInterpretation`/`LoadDispatchInterpretation`/`LoadDispatchDecision` v1 serialization, sole caller `task_handoff_transaction.go`); authoritative-v2 dispatch serialization lives only in `taskauthorityfs` (the v1 `state/.dispatch` adapter path is the documented Phase 6 exception — fs Store fails closed on v1 homes and `taskauthorityfs`→`home` imports forbid the cycle); zero production callers of removed symbols (grep gates + `TestNoNewTaskAuthorityReachThrough` green); behavior tests re-expressed through the Authority (`home/retry_supersede_test.go` deleted; its four contracts moved to `internal/taskauthority/supersede_test.go` plus typed audit; CLI retry/ready tests seed canonical records). `Authority.Supersede` (new named operation, generation fence, stable Operation ID, terminal-only eligibility, fresh queued Generation at Revision one with prior Generation immutable and no stale bindings, typed audit, durable idempotent receipt) backs `backlog retry`; `backlog ready` uses `auth.List`+`auth.Readiness`. The gate allowlist shrank to its defensible minimum. Adjudicated caveats: the Checkpoint 5 "empty allowlist for old local mutations" expectation still has the two documented Phase 4 spawn transition entries (`spawn_runner.go`→`CreateTaskAggregate`, `spawn_cmd.go`→`UpdateCurrentTaskAggregateKind`), whose removal the plan itself schedules at Task 7.8, and the Phase 6 handoff read (`CheckDispatchHold`); v2 `Supersede`/`Reopen` do not reset the `.status` projection generation attribution (consistent with the Task 3.3 v2 model; tracked for Phase 7 projection work); CLI retry preserves the run-after behavior.

Checkpoint 5 is complete. Independent Reviewer acceptance (full verification tier, after one reopen + repair) verified all four criteria at `862d8719`: Task Authority owns all local lifecycle (`Create`/`Start`/`Block`/`Unblock`/`Complete`/`Reopen`/`Supersede`), readiness, and durable dispatch rules (`CreateHold`/`ReleaseHold`/`ResolveDecision`/`InterpretDispatch`/`ConfirmSpawn`/`BindWorktree`); home owns none (serialization-only adapter, holds-only `CheckDispatchHold` read with sole Phase 6 handoff caller); Start/Hold barrier race, interpretation atomicity (13 tests), and the crash-stage matrix pass; the architecture allowlist holds only documented plan-scheduled transition shims (`CreateTaskAggregate`/`UpdateCurrentTaskAggregateKind` → Task 7.8; `CheckDispatchHold` → Phase 6) — adjudicated as transition shims, not competing rule ownership, with the plan's own Task 7.8 grep naming the spawn symbols. The gate REOPENed once on a bisect-proven regression from Task 5.2: the `fleet snapshot` read (`auth.ListHolds()` → `Store.View()` → `withDispatchLock` → `stateDirSafe`) created `state/` + `state/.dispatch.lock` on a fresh home, violating the read-only contract (`TestContractCLIReadsOnlyFreshTempHome`). The bounded repair (`862d8719`) added `withDispatchLockRead`/`stateDirExists` — homes with no authority state (every authority record lives under `state/`) return the empty committed view without creating `state/`, while existing homes keep the canonical `.dispatch.lock → per-task lock` order and all fail-closed behavior; verified by fresh reproduction, a real-binary scratch run, and the full integration suite (13/13 green). Full unit (13/13), full integration (13/13), race, crash/lock matrices, grep gates, build, vet, gofmt, and diff-check are green with only the worktree-sanctioned skip. Note: two Checkpoint 5 gate review attempts died on DeepSeek transport failures before verdicts (no code ruling); the third completed the gate.

Verified on 2026-08-02 (Checkpoint 5):

```sh
go build ./...
go vet ./...
go test -race ./internal/taskauthority ./internal/taskauthorityfs
go test ./... -count=1 -skip TestAgentSkillMirrorsMatchCanonical
go test -tags=integration ./... -count=1 -skip TestAgentSkillMirrorsMatchCanonical
```

All commands passed; the only skipped test is the documented pre-existing `TestAgentSkillMirrorsMatchCanonical` (worktree-sanctioned skip).

Task 6.1 is complete through commit `1ad4e347`. Independent Reviewer acceptance verified all four criteria: `TransferRequest`/`TransferIntent` (pure model in `internal/taskauthority/transfer.go`, zero filesystem imports) bind source/destination home identity, Task ID, exact Generation, a deterministic request digest, and both stable Task Operation IDs, with `Validate()` rejecting empty/mismatched/same-home identities, unsafe Task IDs, invalid Generation, malformed digests, and unsafe Operation IDs; the v1 saga writes a journal v2 with validated per-task intents (durable before staging) and commits fleet-durable destination receipts (`state/.task-handoff-receipts/<op-id>.json`, atomic+fsync) with semantics identical to the Store receipt (same Operation ID + digest → idempotent no-op returning the original; changed digest → non-retryable typed conflict, never overwriting the original); ordering invariant proven by crash tests — all destination receipts commit before any source authority mutation, and failure before the destination receipt leaves source ownership current (recovery re-commits the receipt idempotently and converges to a single destination owner; the 8-boundary crash matrix now includes the `destination-receipt` boundary); a conflicting destination owner (same or newer Generation) fails closed with a typed `domain.ErrorConflict` before preflight, source untouched and destination truth never overwritten. Seam adjudication (Lead ruling, confirmed by the Reviewer): the fleet-durable receipt for the v1 saga path mirrors Store receipt semantics exactly (the fs Store fails closed on v1 homes; same pattern as Task 5.1); Task 6.2 reuses `TransferIntent` for the destination receive operation and retires the v1 file receipt in-slice. Journal v1→v2 bump fails closed on recovery of old journals; no distributed filesystem transaction; lock order preserved.

Task 6.2 is complete through commit `1ecd7af7`. Independent Reviewer acceptance verified all four criteria: `Authority.ReceiveTransfer` (new named operation) commits the complete same Task Generation (aggregate with bindings, interpretation record, decision/hold, task-bound generation audit history, one typed receive audit) at the destination in ONE Store transaction keyed by the intent's destination Operation ID — idempotent replay (no duplicate creation), changed-digest non-retryable conflict, destination-owns-same/newer-generation fails closed with typed `domain.ErrorConflict` (destination truth never overwritten), Generation preserved exactly with Revision restarted at FirstRevision (ADR-0007 §3 — per-authority mutation ordering); the saga reads the source via the source Authority and retires source ownership ONLY after every durable destination receipt (the `destination-receipt` crash boundary sits between receive and retire); retry/recovery replays the Store receipt idempotently (8-boundary crash matrix converges to exactly one destination owner, zero duplicates); projection copies run post-receive with a typed `HandoffPartialError{Transferred, ProjectionErr}` that never rolls back or re-transfers ownership; cross-home resolution stays fleet-owned (`handoffCandidateOwners` collects candidates from canonical v2 state, ambiguity fails closed with typed correction commands); v1 homes fail closed with typed migration-required on both source and destination. The Task 5.3 removal path executed in-slice: the home handoff-compat surface is deleted (`EvaluateDispatchWithDependencies`/`EvaluateDispatch`, `PersistDispatchInterpretation`/records, `LoadDispatchInterpretation`/`LoadDispatchDecision`, `toHome*` converters, v1 dispatch path helpers, `QueryTaskReadiness`/`TaskReadiness`/`ReadinessReason`, `CheckDispatchHold`, and the unused dispatch types), retaining only the v1 record shapes needed by migration decoding; the Phase 6 allowlist entries are empty (`LegacyTaskAuthoritySymbols` = only the Phase 4 spawn entries); `TestNoNewTaskAuthorityReachThrough` green in fleet and cli. Adjudicated caveats (Lead ruling, confirmed by the Reviewer): fleet-owned source retirement (v2 transactions do not represent deletion; `internal/taskauthority` is filesystem-free) is saga-side bookkeeping with digest-verified, idempotent, fail-closed removal strictly after the durable receipt — no foundation debt; Revision restart at FirstRevision is coherent per ADR-0007 §3; only task-bound audit events transfer (dispatch-control audit is structurally non-task-bound and stays at the source); v1 fail-closed is the deliberate posture; the dead `resolveHandoffKeys` helper is a minor cleanup note.

Checkpoint 6 is complete. Independent Reviewer acceptance (full verification tier) verified all three criteria at `32dac0f7`: handoff preserves one current owner and one Task Generation — `Authority.ReceiveTransfer` commits the complete generation in one Store transaction (Generation preserved exactly, `Current=true` at destination, conflicting destination owner fails closed typed `domain.ErrorConflict`, destination truth never overwritten), and source retirement is digest-verified/idempotent/all-or-nothing, strictly after the durable receipt; failed or interrupted handoff resumes without dual ownership — Store idempotency (same Operation ID + digest replays, changed digest conflicts non-retryably), journal v3 recovery (preparing/prepared discard, commit-decided replays idempotently, completed verifies), and the 8-boundary crash matrix converge to exactly one destination owner with zero duplicates (unit + integration); the full integration suite passes with exit 0 using only the sanctioned skip, so there is nothing to misattribute to a compile baseline. Full unit and integration suites green (with only the worktree-sanctioned skip), race and handoff suites green, architecture grep gates show only the two Phase 4 spawn entries (`CreateTaskAggregate`, `UpdateCurrentTaskAggregateKind` — plan-scheduled for Task 7.8), `internal/taskauthority` retains zero filesystem imports, and `TestNoNewTaskAuthorityReachThrough` passes in fleet and cli. Cosmetic note recorded: the plan's Task 6.2 record cites ADR-0007 §5 where revision ordering lives in §3.

Task 7.1 is complete (classification slice, no code changes; recorded at commit `95c1fafe`). Independent Reviewer acceptance verified the classification of all 12 production `home.WriteMeta`/`mhome.WriteMeta` call sites (count corrected from the briefed 13 — the sibling `home.CompareAndSwapMeta` primitive has 13 production sites and was the miscount): 8 authoritative sites mapping to named Authority operations across Tasks 7.2–7.7 (`delivery_issuelinks.go` ×2 → 7.2 ReconcileIssueLinks incl. the dormant RepairIssueLinks; `delivery_attestation.go` → 7.3 AttachAttestation (dormant, spawn writes attestation as runtime observation); `git_authorization.go` ×2 → 7.4 (both dormant; live path is CAS); `delivery_mrcheck.go`/`delivery_prcheck.go` → 7.5 PrepareDelivery identity + `delivery_state=review-ready` init; `delivery_terminal.go` → 7.5 CompleteDelivery terminal identity), 2 Captain supervisor metadata sites (`captain_captain.go` ×2, `captain:` namespace — explicitly excluded from Task Authority and from the Task 7.8 gate), and 2 runtime projection sites (`cli/task_cmd.go` task add = post-`auth.Create` projection with typed `LifecyclePartialError`; `spawn_runner.go` writeTaskMeta = deliberately pre-transition side file per Task 4.2 — neither can influence lifecycle because the Authority commits and revalidates first). Verified cross-cutting findings: zero of the 12 sites bind Task Generation; `WriteMeta` is a full-file replace (projection-loss/race surface eliminated by the Authority moves); Authority access is missing in fleet delivery functions (only the CLI ctx and spawn Args reach it — Tasks 7.2–7.7 thread the composed Authority while preserving cross-home `ResolveTaskHome`); the 13-site CAS family drives `delivery_state` and merge/Git authorization records and MUST be scoped into Tasks 7.4–7.6 or it becomes a competing delivery mutation authority (git/merge auth CAS → 7.4, terminal CAS → 7.5, mergeops CAS → 7.6; delivery_amend spans 7.5/7.6); the dormant helpers (`RepairIssueLinks`, `PersistAttestationToMeta`, `SetGitCapabilityTier`, `SetGitAuthContext`) are zero-caller and each slice folds or deletes them with Authority-interface test replacements; retirement has no WriteMeta site (7.7 is a new named `Retire` operation); the Task 7.8 gate must exempt the `captain:` namespace. Planning notes recorded: Tasks 7.5/7.6 should explicitly assign `delivery_prcheck.go`/`delivery_mrcheck.go`/`delivery_amend.go` and extend the verification greps; `MetaGitAuthContext` must converge both writers (dormant SetGitAuthContext + delivery_amend CAS) onto one Authority operation.

Task 7.2 is complete through commit `6bf682ee`. Independent Reviewer acceptance verified all four criteria: every Issue Link mutation binds exact Task Generation and Operation ID — `Authority.ReconcileIssueLinks` revalidates the Expected Generation fence inside one Store transaction (stale → `ErrConflict`), commits the generation-bound definition record + provider evidence, advances Revision exactly one, and emits one typed `AuditIssueLinks` event (TaskID + Generation + OperationID + Actor + timestamp + reason) with a durable receipt; the production op ID is per generation (`issue-links-reconcile-<taskID>-<gen>`), so a reopen mints a fresh ID; parent and related links cannot be promoted to automatic closure — auto-close policy is allowed only for `IssueLinkImplementation` and rejected (typed `ErrInvalidInput`) at both request validation and the aggregate-level `validateIssueLinkDefinition` invariant (wired into `validateAggregate`), tested at both levels with the aggregate left unmutated; reconciliation retries are idempotent and preserve provider evidence — both Store adapters implement ADR-0007 §6 (same op ID + digest → original receipt with `Replayed=true`, no second audit, no double Revision; changed digest → typed non-retryable `ErrOperationConflict`, fs `RetryNever`), and `requestDigest` excludes the Operation ID so a fresh-ID repair can succeed (tested at authority and fleet level, revision stays 2 on retry); `delivery_issuelinks.go` no longer writes task `.meta` directly — zero `WriteMeta`/`CompareAndSwapMeta` matches, the file dropped `encoding/json`/`time` imports, the projection write moved to the caller (`projectIssueLinkReconciliation` in `delivery_prmerge.go`) with typed `IssueLinkProjectionError` that never rolls back the authoritative commit (ADR-0007 §7). Program rules verified: named operation only (no generic `Set*`), one Store transaction, exactly-one Revision advance, `internal/taskauthority` retains zero filesystem imports, definition records live in `definition.go` inside the Aggregate (cloned and validated), fleet cutover composes the Authority via `Ctx.TaskAuthorityFor(homeDir)` over the home resolved by `RequireShipMeta` → `ResolveTaskHome` (primary then captain homes — cross-home delivery preserved), production always supplies the Authority and `ReconcileAndStoreIssueLinks` fails closed on nil, the dormant `RepairIssueLinks`/`IssueLinkRepairRecord`/`MetaIssueLinkRepairHistory`/`appendRepairHistory` are deleted with zero references and no behavior test lost (all four old repair tests map to new/retained coverage; manual-policy remains a valid committed status; repair-history audit superseded by the typed audit event). 15 files touched, all Task 7.2; the CAS `delivery_state` family is untouched (plan-scheduled for 7.5/7.6); pre-existing gofmt drift in `internal/domain/domain.go` and `delivery_attestation.go` reproduced at baseline and left untouched (housekeeping suggestion recorded, out of scope).

Task 7.3 is complete through commit `7993c743`. Independent Reviewer acceptance verified all four criteria: the Delivery Plan is revisioned within the Task Generation — `DeliveryPlan{RequestedMode, EffectiveMode, FallbackReason}` lives in `internal/taskauthority/definition.go` inside the Aggregate (validated by `validateAggregate`, deep-copied in `clone()`), one acceptance per generation is bounded (second attachment under a fresh Operation ID fails closed typed `ErrConflict` even with identical content), `Reopen` starts a generation without a plan and the prior generation's plan stays immutable, and a fallback reason is mandatory whenever modes differ (a silent mode change is never accepted); capability attestation references bind exact project, home, config digest, and Task Generation — `CapabilityAttestation{Project, Home, ConfigDigest}` commits fenced to the Expected Generation supplied by the ConfirmSpawn receipt, project+home are required non-empty, the digest is optional and safe when present (omitted only when no typed config exists), and cross-home is covered; runtime observation data cannot silently become authoritative task definition — only the accepted reference commits (probes/harness/gate-agent/executable identity stay in the fleet runtime record outside the Aggregate), rejected acceptance leaves the observation runtime-only with the task still working (acceptance is evidence, not a lifecycle gate), projection failure returns typed `AttestationProjectionError` and never rolls back the authoritative commit, and `writeTaskMeta` (pre-transition side file) writes no attestation fields (runtime-observation-only); `delivery_attestation.go` no longer writes task `.meta` directly — grep gate clean, dormant `PersistAttestationToMeta` deleted with all four prior tests re-expressed 1:1 through `AttachAttestation` + `projectAttestationEvidence` (plus new idempotent-retry, cross-home, fails-closed, typed-partial-error tests; no behavior test lost), and the remaining meta interaction is read-only (`ReadAttestationFromMeta`, `ModeVisibilityFields`). Program rules verified: `AttachAttestation` is a named semantic operation (generation fence revalidated inside the Store transaction, stable Operation ID, delivery-plan + attestation-reference + exactly-one Revision advance + typed `AuditAttestation` event + durable receipt in ONE transaction, same-op+digest replays idempotently preserving provider evidence, changed digest → typed non-retryable `ErrOperationConflict`, stale `ErrConflict`/missing `ErrNotFound` mutating nothing); `internal/taskauthority` retains zero filesystem imports; spawn threads the composed Authority (`Args.Authority` from `Ctx.TaskAuthority()`) and routes acceptance through `StoreAttestationEvidence` (op ID `spawn-attest-<id>-<gen>`); the projection-layer read surface is retained with snapshot continuing to read the runtime-observation `meta["mode"]` — canonical-read preference is explicitly scheduled at Task 7.8; no competing `.meta` writer of record for attestation fields outside the projection. Adjudicated: the timing shift is behavior-neutral (no production reader of the old side-file attestation fields; the post-confirm `attachAttestation` mirrors the `appendSpawnedStatus`/`armWatcher` best-effort pattern with warning-only failure); bounded per-generation transitions break no legitimate same-generation path (mode resolution and late-capability-loss fallback complete before the acceptance commits; mode change remains possible across generations via `Reopen`/`Supersede`, matching ADR-0004 §6); gofmt normalization confined to in-slice files. Risks recorded: post-spawn mode transitions required by ADR-0004 §6 are a forward-looking gap for Task 7.5/7.8, not a regression.

Task 7.4 is complete through commit `650e1fd8`. Independent Reviewer acceptance verified all four criteria: merge authorization binds Task Generation, provider identity, PR identity, and immutable head SHA — `Authority.AuthorizeMerge` commits `MergeAuthorization{HeadSHA, ProviderIdentitySnapshot, AuthorizedAt, Authorizer}` inside the Aggregate (generation binding structural; Expected Generation fence revalidated in one Store transaction; validation requires identity-snapshot head to equal the authorized head); changed head invalidates stale authorization — the prior authorized head is revalidated in-tx (expected prior head must equal the committed head else typed `ErrConflict`, never silent reuse; same op ID + changed head is non-retryable `ErrOperationConflict` at the store layer; a fresh op acknowledging the prior head may re-authorize explicitly), and the fleet read path `CheckMergeAuthorization` returns typed `ErrStaleAuthorization` when the current head ≠ authorized head — force-with-lease semantics at both layers; git authorization binds the exact generation, worktree binding, capability tier, and expected pre-state — `SetGitCapabilityTier` (fence + worktree `ErrPrecondition` + `ExpectedPriorTier` + tier immutable per generation), `SetGitAuthContext` (fence + worktree + `ExpectedPriorContext` + value-level no-op), `AuthorizeGitMutation` (fence + worktree + `ExpectedPriorContext` + elevated-op-only + expected-state validation), `ClearGitMutationAuthorization` (fence + worktree + `ExpectedPriorOp`), each with one Revision advance + typed `AuditGitAuthorization`; authorization writes are idempotent and directly auditable — Store-level same-op+digest replay (`Replayed=true`, no second audit, no double revision), changed digest → non-retryable `ErrOperationConflict`, value-level same-value no-ops not advancing Revision, `RecordExternalMerge` bounded (one per generation), typed `AuditMergeAuthorization`/`AuditGitAuthorization` events committing in the same transaction as the record with durable receipts. Program rules verified: named operations only; `internal/taskauthority` retains zero filesystem imports (only stdlib `path`); records inside the Aggregate (validated by `validateAuthorizationDefinition` via `validateAggregate`, deep-copied in `clone()`); fleet cutover via `internal/fleet/delivery_authorization.go` wrappers (nil fails closed, cross-home preserved, `.meta` projections caller-owned with typed `AuthorizationProjectionError` — ADR-0007 §7, projection failure never rolls back); `git_authorization.go`/`merge_authorization.go` rewritten to a zero-write read/policy surface. Convergence achieved: both `git_auth_context` writers (dormant `SetGitAuthContext` + three delivery_amend CAS context writes) converge on the single `SetGitAuthContext` op (`MetaGitAuthContext` zero in `delivery_amend.go`); the `git_capability_tier`/`MetaGitMutationAuth`/merge-authorization field families have zero raw writes outside the projection layer. The CAS-site classification was verified coherent by field family: delivery_amend context writes moved in-slice while `delivery_state` CAS stays (7.5/7.6); merge_authorization `AuthorizeMerge` + `RecordExternalMerge` evidence records moved; the `delivery_state=merged` transition was deliberately deferred to Task 7.6 (both the op and the old function are dormant — zero production callers — and tests assert the evidence record commits without touching state); delivery_terminal (2) + delivery_mergeops (4) CAS untouched. v1 fail-closed confirmed: pr-amend/reconcile compose the Authority, `auth.Get` on a non-migrated home returns typed `ErrMigrationRequired`, and the context op runs BEFORE the `delivery_state` CAS so failure leaves the amendment unstarted. Tier-immutability is a documented behavior change (old WriteMeta silently overwrote; the op enforces one tier per generation — same-value re-set no-op, different tier conflicts) with no legitimate launch-time path broken (zero production callers pre-task). All removed fleet behavior tests have Authority-interface replacements (traced one-by-one); type moves (`GitCapabilityTier`/`GitOperation`/`GitExpectedState`/`GitMutationAuthorization`/`MergeAuthorization`/`ProviderIdentitySnapshot`/`ExternalMergeRecord`) compile-clean. 15 Go files, all Task 7.4; the two `SetGitCapabilityTier|SetGitAuthContext` grep matches are the wrapper op invocations (folding), zero dormant helpers remain. Risks recorded: wrapper-level retries converge by value-level idempotency (fresh random op ID per invocation) with receipt-level replay guaranteed at the Authority layer; `CheckMergeAuthorization` and the write wrapper remain without production callers (wiring into the merge flow is outside scope); legacy `.meta` merge_authorization JSON is no longer consumed by the canonical read path — migration of legacy authorizations to be considered in the Task 7.8 gate.

Task 7.5 is complete through commit `60f1d7ad`. Independent Reviewer acceptance verified all four criteria: ship completion cannot use `resolved` as delivery completion — enforced at four layers (CLI `report_cmd.go` pre-check, `validateCompleteDeliveryRequest` rejecting terminals other than delivered/done, the `CompleteDelivery` phase gate failing closed on resolved/retired generations, and `validateDeliveryTerminalRecord`), and the terminal record never mutates the lifecycle Phase (tests assert the phase stays queued; delivery evidence is bound inside the generation, not a competing phase mutation); provider evidence binds exact Task Generation, provider identity, and head SHA — `CompleteDelivery`/`PrepareDelivery` revalidate the Expected Generation fence inside the Store transaction, the identity snapshot head must equal the record head, the terminal head must equal the prepare-time head (changed head → typed `ErrConflict`, no mutation), and the terminal identity URL must equal the prepared URL (same PR); failed verification leaves the prior authoritative phase unchanged — validation failures commit nothing (aggregate phase, revision, and record absence asserted unchanged) and commit failures fail closed with no partial state; delivered/done transitions emit typed audit events and idempotency receipts — one `AuditDeliveryPrepare`/`AuditDeliveryTerminal` event commits in the same transaction as the record with exactly-one Revision advance and a durable receipt, same-op+digest replay returns the original receipt (`Replayed=true`, no second audit, no double revision), changed digest → non-retryable `ErrOperationConflict`, same-value re-prepare/re-complete is an in-value no-op, and delivered and done are distinct mutually-exclusive transitions per generation. Program rules verified: named operations only (fence revalidated in-tx, stable Task Operation ID, records inside the Aggregate validated by `validateAggregate` and deep-copied in `clone()`), `internal/taskauthority` retains zero filesystem imports; fleet cutover — `RoutePRCheck`/`PRCheck`/`MRLiveCheck` → `StoreDeliveryPrepare`, `CaptureTerminalIdentity` (done) and fleet `PrepareDelivery` (delivered) → `StoreDeliveryCompletion`, cross-home preserved via `ResolveTaskHome`/`RequireShipMeta`, production always supplies the Authority (nil fails closed), remaining `.meta` writes for these families are projection-layer only with typed non-rollback `DeliveryProjectionError`; `delivery_terminal.go` CAS count is now zero while `delivery_amend.go` (3) + `delivery_mergeops.go` (4) `delivery_state` CAS sites are untouched (empty diff) for Task 7.6. Adjudicated: the resolved exclusion is enforced at three real layers (CLI pre-check verified pre-existing in the parent; phase never mutated by terminal ops — asserted); the legacy fail-closed migration step is the intended posture — a `.meta`-only ship fails `report done` at `CaptureTerminalIdentity`→`StoreDeliveryCompletion` before any status/receipt/wake is produced, and re-running pr-check commits the prepare record and unblocks (end-to-end report flow checked, no unintended production break; legacy migration scheduled for the Task 7.8 gate); the terminal record is bounded to one per generation with same-terminal+head in-value no-op; wrapper-level retries converge by value-level idempotency (fresh random op ID per invocation) with receipt-level replay at the Authority layer; `CaptureTerminalIdentity`'s fresh-capture fallback must bind the prepared head/PR inside `CompleteDelivery` or fails closed. 16 files, all Task 7.5 scope. Minor cosmetic note (non-blocking): `internal/fleet/delivery_terminal_test.go` introduced a missing EOF newline (gofmt-flagged; recommend a follow-up sweep with Task 7.8).

Task 7.6 is complete through commit `9bb059b9`. Independent Reviewer acceptance verified all four criteria: each merge attempt has a stable Operation ID and exact generation/head binding — `RecordMergeAttempt` mints a stable op ID, revalidates the Expected Generation fence inside the Store transaction (stale → `ErrConflict`), and binds `HeadSHA` + `ProviderIdentitySnapshot` (identity head must equal record head, enforced at request validation and by `validateMergeAttemptRecord` via `validateAggregate`); `remote_unknown` forbids repeated provider mutation and permits only read reconciliation — after a committed remote-unknown, every further attempt is refused with typed sentinel `ErrMergeMutationRefused` (errors.Is-able, `RetryNever`), only `Get` works, and fleet `ReconcileMergeDelivery` returns a classified read-only result (`Escalated=true`) even when the provider later confirms merged; already-merged and provider false-negative outcomes are idempotent — a committed merged-equivalent outcome makes any re-attempt an in-value no-op (revision/audit unchanged) and open/failed/remote-unknown reads never regress the verified merged truth; partial metadata failure does not erase verified remote truth — projection failure returns typed `MergeOutcomeProjectionError`, the authoritative commit stands, and retry heals idempotently (a state-dir-lock fleet test asserts the authoritative merged record + merged SHA survive with meta unmutated and the retry heals `delivery_state=merged`). Program rules verified: named operations only (no generic `Set*`), one Store transaction with exactly-one Revision advance, typed `AuditMergeOutcome` committing in-tx with durable receipt, same-op+digest replay (`Replayed=true`), changed digest → non-retryable `ErrOperationConflict`, stale/missing fails closed; `internal/taskauthority` retains zero filesystem imports; `MergeAttempt` lives inside the Aggregate (validated, deep-copied); the merged-state transition commits via the Authority (`RecordExternalMerge` deferred from Task 7.4 now drives `StoreMergeAttempt(Merged)` with verified evidence; `MarkMerged`/`MarkMergedFromRecord`/`ReconcileIdentity`'s merged branch route through it; zero raw CAS for the merged outcome); fleet cutover via the composed Authority (cross-home preserved, nil fails closed), remaining `.meta` fields projection-layer-only with typed non-rollback errors, and a new in-tree gate test pins the grep. The 13-site CAS family is now ZERO in fleet/cli production (verified). Adjudicated: the remote_unknown Escalated=true posture (refusing even a later provider-confirmed merged mutation) is the mandated fail-closed design — it preserves safety (no re-mutation against an uncertain provider state, no double-merge risk) with the wedge observable via sentinel + Escalated flag and resolved through read reconciliation; residual operational cost recorded — a wedged task has no in-band unblock (retirement poll fails closed, ReconcileIdentity refuses, RecordExternalMerge/MarkMerged route into the same refusal), recommending a runbook entry and a governed un-wedge consideration at the Task 7.8 gate; amendment identity changes rebind DeliveryPrepare (force-with-lease acknowledging the committed prior head) and fail closed without an authoritative prepare record — coherent with Task 7.5's migration posture; the minor divergences are behavior-safe (revision bumps tied to committed attempts, failed attempts now commit authoritative records — an auditable improvement; one wording note on the already-merged commit paths with no functional impact); the StoreMergeAttempt projection race yields at most one extra idempotent projection write healed on next reconcile, no worse than the old CAS; `ReconcileIdentity` fails closed on committed remote-unknown before any mutation; the CAS helpers (`identityChecks`/`identityUpdates`/`PrMetaFields`/`PrMetaChecks`/`persist*`) were zero-caller/orphaned and are gone with no behavior test lost; the boundary held — `delivery_terminal.go`/`retirement_task.go`/`delivery_mergeandretire.go` untouched with their `delivery_state` reads served by the maintained projection, and the retirement-poll merged transition is now Authority-routed via the unchanged `RetirementPort` interface composing the Authority over the serviced home (v1 fails closed). Risks recorded: legacy v2 homes with meta-only `delivery_state=merged` predating cutover carry no authoritative attempt until a mutation path runs — same class as the Task 7.4 legacy-authorization note, scheduled for the Task 7.8 gate.

Task 7.7 is complete through commit `bcb32096`. Independent Reviewer acceptance verified all four criteria: retirement requires exact Task Generation and verified prerequisites — `Authority.Retire` is a named semantic operation with the Expected Generation fence revalidated in-tx (stale → typed `ErrConflict`), `RequireVerifiedDelivery` enforced inside the Store transaction against the current Aggregate (committed merge attempt with merged-equivalent outcome OR delivered/done delivery terminal; not eligible → typed `ErrPrecondition`, nothing mutated, no audit), missing task → `ErrNotFound`; cleanup failure preserves merged truth and returns a resumable retirement receipt — the `Retire` op commits FIRST (durable receipt pinning retired phase + typed audit) and all post-receipt cleanup (endpoint Probe, Dispose, ReturnWorktree, FinalizeRetirementJournals) wraps failures in typed `RetirementCleanupPendingError{TaskID, Generation, Revision, CleanupErr}` with a partial `TeardownResult`, and the committed merged truth is never rolled back or mutated (aggregate retired at revision 3 with `MergeAttempt` retained; meta byte-identical, merged SHA intact); retry resumes retirement only — `MergeAndRetire` skips the merge phase on resume (alreadyMerged gates Phase 1, retry asserts `MergeOutcomeAlreadyMerged`) and replays the Store receipt under the stable per-generation op ID (`task-retire-<task>-<gen>`, same actor/digest) with revision unchanged (no double transition), and direct teardown retry completes the resume with revision stable; retired phase and audit event commit once — ONE Store transaction (aggregate Phase=retired + Revision+1 + typed `AuditRetirement` validated in `operation.go` + durable receipt; receipt count 2 = create+retire, audit count 1), an already-retired generation refuses a fresh op ID (`ErrConflict`, no second transition/audit), same-op replay returns the original receipt with no second audit/double revision, changed digest → non-retryable `ErrOperationConflict`, and reopened generations retire under their own per-generation op identity. Program rules verified: named operation only (no generic setters), `internal/taskauthority` retains zero filesystem imports (the op never touches the filesystem; cleanup stays fleet/saga-side post-receipt), records/phase inside the Aggregate retained post-retire; fleet cutover — `RetireTask` takes the composed Authority (nil fails closed, meta preserved), threads through `delivery_mergeandretire.go` and CLI `munsu teardown` (`fleet.ResolveTaskHome` primary + captain homes + `ctx.TaskAuthorityFor` — cross-home preserved), the retirement files show zero `WriteMeta`/`CompareAndSwapMeta`. Adjudicated: the `--force` fail-closed change is real and intended — the old path skipped the merged-state safety gate for ships under `--force`, the new op enforces the authoritative evidence prerequisite for identity-bearing tasks regardless of `--force` (the digest is stable because the prerequisite flag derives only from meta identity; a Force-dependent requirement would break receipt replay), with the heal path being merge/reconcile/poll committing evidence or operator escalation, and scouts/identity-less tasks unaffected; the resume ReadMeta-first dependency gap predates this slice (parent `RetireTask` and `MergeAndRetire` both began with `home.ReadMeta` — unchanged, out of slice scope, operator recovery possible via the durable aggregate + receipt); the legacy meta-only merged risk is exactly the class recorded in the Task 7.6 record (scheduled for the Task 7.8 gate); `AuditRetirement` renders no `.status` line (Task 7.8 owns that projection). 12 files, all Task 7.7 scope; the Task 7.8 boundary held (zero changes to snapshot/soldierstate/contract_commands/home_summary). Two minor non-blocking notes recommended for the Task 7.8 sweep: the `VerifyLaunchArtifacts` post-receipt failure returns a plain error instead of the typed partial (resumable and safe; wrap for consistency), and no dedicated fleet-level test asserts the --force + identity + no-evidence fail-closed derivation (Authority-level prerequisite fully tested).

Task 7.8 is complete through commit `fd6f2c20`. Independent Reviewer acceptance verified all four criteria: no production file in `internal/fleet`/`internal/cli` directly writes Authoritative Task Aggregate files or task `.meta` state — the plan's 7.8 grep is literally ZERO in production (the two Phase 4 allowlist entries removed: `ensureTaskAggregate` is now a pure Authority query failing closed without a canonical record with heal path `backlog add`, and the `promote` CLI routes through the new named `Authority.Promote` op replacing both `UpdateCurrentTaskAggregateKind` and `home.PromoteMeta` reach-throughs; `task add`'s projection routed through `taskauthorityfs.Store.ProjectTaskAdd`; the spawn side file is strictly projection-only), and the remaining 12 production `WriteMeta` sites are either one-directional delivery projection writers (verified field-by-field: only delivery projection fields), the spawn runtime-only side file + attestation projection, or the `captain:` exemption (2 sites) — zero `CompareAndSwapMeta`; snapshot and task observation prefer canonical Authority state — `snapshot.go` composes the fs Store per home with canonical kind/project/phase winning over `.meta`/`.status`, canonical tasks without `.meta` still appear, and v1 homes fail closed (`ErrMigrationRequired`); `.status` cannot override newer authoritative lifecycle — canonical phase is state truth in snapshot/ReadWithProbe (`StatusLogSuperseded=true`)/`SummarizeCaptainHome` (verb=phase), a stale `working` status cannot downgrade a canonical `done`, and a tampered meta cannot override kind/project; captain-specific metadata remains explicitly outside the gate — `captain_captain.go` untouched with a structural pin test. The new `Promote` op was adjudicated as the MINIMAL replacement (fence revalidated in-tx, one Revision advance, typed `AuditPromote`, durable receipt; semantics match legacy — kind=scout + done/resolved prerequisites re-expressed canonically with in-tx phase enforcement in the sanctioned fail-closed direction; no `.status` line matching legacy; CLI preflight reads canonical kind; projection reconciled post-receipt with typed `LifecyclePartialError`). Verified claims: the spawn side file never writes authoritative fields from spawn args and legacy meta-less tasks cannot spawn (fail-closed, heal `backlog add`); the read-lock extension (skip creating `state/.dispatch.lock` when `state/` exists but carries no authority records — extends the Task 5.2 repair, strictly read-only) preserves v1/corrupt/recovery fail-closed with no read-contract regression (plan command leaves target home byte-identical); the `WriteTaskAggregateMigrationPlan` → `WriteMigrationPlan` rename is behavior-neutral making the raw gate literally zero; the gate allowlists are EMPTIED — `TestNoNewTaskAuthorityReachThrough` passes `map[string][]string{}` in both fleet and cli; observation fixtures re-expressed v1→v2 with all four behavior tests preserved 1:1; the sweep items are done (EOF newline, `VerifyLaunchArtifacts` typed wrap, fleet-level `--force` fail-closed test with meta intact, `deriveStatusLines` renders `retired: <reason>` from `AuditRetirement`); `home.PromoteMeta` remains in `internal/home` with zero production callers (Task 8.1/8.2 deletion candidates). Legacy migration posture (a) verified fail-closed: meta-only `delivery_state=merged`/legacy `merge_authorization` claims raise typed `LegacyDeliveryEvidenceError` naming the heal path in snapshot + observation; `home_summary` fails closed home-level (canonicalAggregates error → Valid=false, State=unknown, "requires migration or repair") with per-child canonical phase/kind preference and no per-child delivery-claim guard (validity model flags inconsistency; minor doc note: the per-child exemption is not separately spelled out in code — recommended as a one-line note in a future sweep). Minor cosmetic note: `task observe` on a v1 home without meta reports not_found masking the migration reason (fail-closed either way). 27 files, all Task 7.8 scope; Task 8.1/8.2 boundaries held.

Checkpoint 7 is complete. Independent Reviewer acceptance (full verification tier) verified all four criteria at `734915a6`: delivery and task projections no longer form competing mutation authority — the plan grep gates (`CreateTaskAggregate`/`UpdateCurrentTaskAggregateKind`/`WriteTaskAggregate`/lifecycle/dispatch symbols and `home.(Create|Start|Block|Unblock|Complete|Reopen|Supersede|Bind|Confirm)`) have ZERO production matches (only `_test.go` fixtures + the gate tests), `TestNoNewTaskAuthorityReachThrough` passes with an EMPTY allowlist in both fleet and cli (the two Phase 4 spawn entries were removed at Task 7.8, replaced with `taskauthority_reads.go` queries and the named `Authority.Promote`), the captain exemption is pinned, and the cutover gate test pins zero direct authoritative meta writes outside `project*` helpers; snapshot and soldier state read canonical Task Authority records first with `.meta`/`.status` as display fallback only; any remaining `.meta` writes are classified as non-task runtime/Captain projection data — all 11 production `WriteMeta` sites classified: 8 projection-layer writes reconciling authoritative commits with typed non-rollback errors (delivery prepare/terminal, amendment begin/result, merge/git authorization, merge outcome, issue-link reconciliation, attestation evidence), 2 `captain:` namespace supervisor metadata sites (explicitly exempt), and 1 runtime-only observation (the spawn pre-transition side file, which cannot influence lifecycle because the Authority revalidates generation inside the transaction); task lifecycle, delivery, and bindings all bind exact Task Generation — every operation family verifies `cur.Generation != req.ExpectedGeneration` → typed `ErrConflict` INSIDE the `Store.Update(op, fn)` transaction with digest-verified idempotent Operation IDs (`phaseTransition` lifecycle, `Create` fail-closed on existing, `Supersede`/`Retire` incarnation fences, `PrepareDelivery`/`CompleteDelivery`/`RecordMergeAttempt`/attestation/issue links/authorization, `BindWorktree`/`ConfirmSpawn`), and CLI/fleet callers pass the exact current authoritative generation; focused delivery, snapshot, and observation tests pass (unit + integration). Full gate evidence: build/vet clean, `-race` on taskauthority/taskauthorityfs green, reach-through gate green uncached with empty allowlist, focused integration (Delivery|Snapshot|Observe|Projection|Authority|Spawn|Retire|Retirement|Handoff) green, full unit + full integration suites exit 0 with only the sanctioned skip (13 tested packages), `git diff --check` clean. Notes recorded: pre-existing gofmt-unclean `internal/configmigration/migration.go` (+test) predates the branch (recommend a `gofmt -w` tidy with Phase 8 cleanup); the legacy `internal/home` lifecycle/binding code (`task_lifecycle.go`, `dispatch_evaluator.go`, `dispatch_control.go`) remains present as the plan's Task 8.1 deletion scope.

Task 8.1 is complete through commit `3bc567fb`. Independent Reviewer acceptance verified all four criteria: `internal/home` retains no lifecycle/readiness mutation or generation-bound binding authority — the last legacy mutation (`home.PromoteMeta`, scout→ship `.meta` kind flip) was deleted (2 files, −122 lines, zero additions), all lifecycle/binding mutations having been removed in prior slices (verified by file history: `task_lifecycle.go`/`dispatch_evaluator.go` at Task 6.2, `retry_supersede_test.go` at Task 5.3, `StartTask`/`UnblockTask`/`ReopenTask` at Task 3.3, `BindTaskWorktree`/`BindTaskEndpoint`/`UpdateCurrentTaskAggregateState` at Tasks 4.1/4.2), and grep gate 2 matches only read/serialization helpers (`validateTaskEndpointBinding`/`validateTaskWorktreeBinding` v1 shape validation, `TaskWorktreeLeaseActive` v2 lease read) plus unrelated-domain functions (mailbox `Superseded*` message dedup, wake-resolution helpers) — the retained supervision/health functions (`CheckWatcherHealthForDispatch` etc.) are deliberately outside Task Authority per plan completion criterion 8 / Task 4.3 with a real production consumer; old behavior tests are replaced through the Authority interface, not simply removed — all 5 removed home tests map 1:1 to Authority-interface replacements (`Authority.Promote` fence/kind-flip tests, CLI `TestPromoteRoutesThroughTaskAuthority`/`TestPromoteRefusesNonTerminalScout` asserting the `.meta` projection reconcile with `meta[kind]=ship` + generation/state, and the meta-shape-specific preconditions superseded by the canonical preflight `auth.Get` fail-closed-on-absence + `deriveMetaProjection` field preservation); no orphaned helpers or imports remain — build/vet green and `PromoteMeta` grep shows only a documentation comment; focused home/taskauthority/fleet tests stay green (full suites exit 0 with only the sanctioned skip). Verified inventory coherence: the retained v1 shapes have real consumers (`home.TaskAggregate` binding fields decoded by `taskauthorityfs/migration.go convertV1Aggregate`; the `Dispatch*` types decoded by migration AND `DispatchAction` constants used by production fleet/CLI supervision gates; `ReadCurrentTaskAggregate`/`ListTaskAggregates` used by `cli/git_worktree_safety.go` + `taskauthorityfs/projection.go`; `TaskWorktreeLeaseActive` used by the git-safety gate). Adjudicated: `home.CompareAndSwapMeta` retention is defensible — it is a generic `.meta` CAS primitive with no lifecycle/binding semantics (cannot touch the aggregate, bindings, or authoritative state; `.meta` is a non-authoritative projection per Task 7.8) and zero production callers, and deleting it now would remove a behavior test without replacement (an explicit delete-or-justify decision is folded into the Task 8.2 sweep); the degraded-mode helpers are sanctioned Task 4.3 supervision tooling. Task 8.2 boundary verified: `task_aggregate*.go` + the `UpdateCurrentTaskAggregateKind` definition (zero production callers) remain as Task 8.2 deletion scope, with `internal/cli/git_worktree_safety.go` confirmed as the key production v1-aggregate consumer that 8.2 must migrate. Notes: Checkpoint 7 record's note listing `task_lifecycle.go`/`dispatch_evaluator.go` as "remains present" is stale doc drift (deleted at Task 6.2) — the Task 8.3 doc update corrects it.

Task 8.2 is complete through commit `0edee482` (net −1752 lines: +488/−2240 across 22 files). Independent Reviewer acceptance verified all four criteria: `internal/home` retains no Authoritative Task Aggregate reader/writer or task migration implementation — `task_aggregate_store.go` (346), `task_aggregate_migration.go` (818), `task_aggregate_test.go` (318), the CLI `migrate task-aggregates` subcommand, `CompareAndSwapMeta` + `CASError`, the `ListMeta` v1-aggregate overlay, and the orphaned v1 validators/candidate-quarantine machinery are deleted, with every retained symbol carrying a v1 decode-only comment and a real consumer (`TaskAggregate`+`TaskAggregateEvidence` + binding structs consumed solely by `taskauthorityfs/migration.go` v1 JSON decode; `TaskWorktreeLeaseActive` v2-namespace lease read consumed by `cli/git_worktree_safety.go`; `AmbiguousTaskIDError`+`CorrectionCommands` shared typed error consumed by CLI task show + fleet handoff saga + captain), and deleted-symbol greps are zero in code; supported migration remains explicit through `taskauthorityfs` — `PlanMigration`/`ApplyMigration`/already-migrated (`ErrAlreadyMigrated`), `ErrMigrationRequired` v1 fail-closed, v2 `WriteMigrationPlan`, self-contained (imports `home` only for decode shapes + `ReadHomeIdentity`, no import of the deleted implementation); existing migration receipts and archives remain readable — the plan/apply/already-migrated test battery passes against the retained surface (plan read-only/deterministic, consumes v1 aggregate output, converts all record families, apply happy path, already-migrated no-rewrite, crash/partial-archive resume, source-changed and home-identity-changed fail-closed, quarantined-plan refusal, symlink/hardening) plus the CLI `TestMigrateTaskAuthorityPlanAndApplyCommands`; net code growth is reviewed — duplicate-store deletion (≈1480 lines) far outweighs the added decode-shape/re-expression surface (`cli/task_authority_reads.go` 105 lines + test re-expressions). Verified claims: `cli/git_worktree_safety.go` migrated to `Store.View().Current/Aggregates` with its semantics re-expressed (stale-generation via Authority Complete→Reopen, recycled-lease via lease-marker tamper, unexpected-head via binding-head rewrite) and v1 homes failing closed `ErrMigrationRequired`; `cli/task_authority_reads.go` re-expresses generation/ID/presence for ready/consume-ready/report/brief/task show; the `TestBeginAmendment_CASConflict` re-expression asserts the same fail-closed semantics through `SetGitAuthContext` (stale prior → typed `ErrConflict`; second amend fail-closed with both `.meta` projection and authoritative `GitAuthContext` untouched); `home.WriteMigrationPlan` (Task 7.8 rename) deleted with its only reader, the surviving `taskauthorityfs.WriteMigrationPlan` being the correct owner. Adjudicated: raw `.meta`/backlog-only homes that never produced v1 aggregates have no v2 migration path — verified they present an EMPTY authority view (not `ErrMigrationRequired`) so new tasks proceed through the Authority and only pre-authority tasks are unreachable, consistent with plan Task 8.2 wording; a one-line operator note (legacy raw-`.meta` tasks are not migrated; recreate via the Authority) is folded into the Task 8.3 doc update. Tech-debt carry-forward flagged for 8.3/8.4: the vestigial `LegacyTaskAuthoritySymbols` gate list (`CreateTaskAggregate`/`UpdateCurrentTaskAggregateKind` deleted), stale `CompareAndSwapMeta`-ban comments in the gate test (now vacuous), the stale `munsu migrate task-aggregates` provenance comment in `taskauthorityfs/migration.go:20`, and zero-caller `home.MigrateAndActivate` (a different legacy-state-import migration domain, ADR-0006, outside this plan's file list).

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

- [x] Both adapters have identical receipts, Revision behavior, idempotency, and rollback semantics.
- [x] Filesystem tests assert durable results after closing and reopening the Store.
- [x] Adapter-specific behavior remains behind the Store seam.

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

- [x] Plan records exact home identity, source digest, source schema, target schema, records, and quarantine outcomes.
- [x] Apply revalidates source digest, stages all target records, verifies them, installs, and writes a durable receipt.
- [x] Corrupt or conflicting sources remain untouched and fail with exact evidence.
- [x] Re-running a completed migration verifies the receipt and returns `already_migrated`.
- [x] Normal `View`/`Update` returns an exact migration-required error for v1 state and never invokes migration.

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

- [x] In-memory and filesystem adapters pass one Store contract suite.
- [x] Crash injection passes at every journal stage.
- [x] v1 state requires explicit migration; no lazy migration path exists.
- [x] `go build ./...` and `go vet ./...` pass.
- [x] No existing production mutation has switched yet.

## Phase 3 — CLI composition and first vertical cutover

### Task 3.1 — Compose Authority in CLI context

**Description:** Construct `taskauthorityfs.Store` and concrete `taskauthority.Authority` once after resolving the exact home.

**Acceptance criteria:**

- [x] Command context exposes the concrete Authority without package globals.
- [x] Commands that do not need Task Authority do not receive pass-through parameters.
- [x] Store construction performs no migration or mutation.
- [x] Tests can inject an Authority backed by an in-memory Store.

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

- [x] One create operation produces one queued Task Generation with owner, definition, kind, and project.
- [x] Duplicate creation returns typed conflict and creates no projection duplicate.
- [x] Task show/list read canonical Authority records; `.meta` cannot override them.
- [x] Projection failure returns a typed partial result while preserving the authoritative receipt for reconciliation.
- [x] Production callers no longer invoke `home.CreateTaskAggregate`.

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

- [x] Backlog commands call semantic Authority methods with Expected Generation and stable Operation ID.
- [x] Invalid transitions fail before backlog projection mutation.
- [x] Projection failure is retryable without replaying the authoritative operation.
- [x] `Reopen` creates a new Generation and leaves prior Generation immutable.
- [x] `home.StartTask`, `home.UnblockTask`, `home.ReopenTask`, and generic backlog state updates have no production callers.

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

- [x] Appending a status line cannot change the Authoritative Task Aggregate phase.
- [x] Existing typed event translation remains idempotent.
- [x] Material Soldier reports continue through the existing parent reconciliation path.
- [x] CLI help and typed output no longer imply that arbitrary status text mutates authoritative lifecycle.
- [x] `home.UpdateCurrentTaskAggregateState` has no CLI caller.

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

- [x] `.meta` is derived from canonical aggregate/bindings and cannot write back into authority.
- [x] `.status` is derived from typed audit/activity history and remains append-only where compatibility requires it.
- [x] Deleting or corrupting a projection can be repaired without changing Revision or Generation.
- [x] Projection reconciliation is idempotent and has a typed partial outcome.

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

- [x] Task create/show/list and backlog lifecycle work end-to-end through Authority.
- [x] `task status` cannot mutate authoritative phase.
- [x] `.meta` and `.status` rebuild from canonical state/audit.
- [x] Old create/start/unblock/reopen/generic-state production callers are gone.
- [x] Focused CLI, taskauthority, taskauthorityfs, fleet, and orchestrator tests pass.

## Phase 4 — Spawn and generation-bound bindings

### Task 4.1 — Move Worktree Binding into Authority

**Description:** Replace the current marker-plus-aggregate two-write sequence with a named generation-bound `BindWorktree` operation inside the Authority transaction.

**Acceptance criteria:**

- [x] Binding validates repository identity, path, Git/Common directories, head, lease, fence token, and Expected Generation.
- [x] Lease marker and aggregate binding commit or recover together.
- [x] Rebinding the same identity is idempotent; conflicting binding fails closed.
- [x] `home.BindTaskWorktree` has no production caller after cutover.

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

- [x] Endpoint Binding and working transition commit together.
- [x] Failed endpoint persistence leaves the task non-working.
- [x] Expected Generation, owner, worktree binding, and applicable Dispatch Hold are revalidated inside the transaction.
- [x] `home.BindTaskEndpoint` and `home.UpdateCurrentTaskAggregateState(..., "working", ...)` have no production caller.

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

- [x] Authority tests have no watcher dependency.
- [x] Start/spawn commands still fail closed when supervision is degraded.
- [x] No watcher failure creates or mutates a Dispatch Hold or task phase.
- [x] Existing home dispatch check no longer calls watcher health.

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

- [x] Worktree and Endpoint Bindings are authoritative generation-bound records.
- [x] `ConfirmSpawn` is atomic and semantic.
- [x] Watcher health remains outside Authority.
- [x] Spawn-focused tests and race tests pass.

## Phase 5 — Durable dispatch-control cutover

### Task 5.1 — Move Dispatch Interpretation rules

**Description:** Move dependency digesting, divergence classification, autonomy rules, and interpretation outcomes into Task Authority.

**Acceptance criteria:**

- [x] Interpretation identity/digest is deterministic.
- [x] Safe reinterpretation and material ambiguity follow ADR-0004.
- [x] Decision-required interpretation atomically stages its Decision, Hold, and audit event.
- [x] Home adapter contains serialization only, not interpretation rules.

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

- [x] Hold scopes compose conservatively and release is idempotent.
- [x] Resolving a Decision does not auto-start queued work.
- [x] All human and automatic dispatch paths evaluate Authority holds.
- [x] Direct production calls to `home.CreateDispatchHold`, `home.ReleaseDispatchHold`, `home.ResolveDispatchDecision`, and `home.CheckDispatchHold` are gone.

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

- [x] `internal/home` exports no Task lifecycle, readiness, Dispatch Hold, Dispatch Interpretation, or Dispatch Decision mutation operation.
- [x] Dispatch serialization and paths exist only in `taskauthorityfs`.
- [x] Grep-based architecture tests show zero production callers of removed symbols.
- [x] No behavior test is deleted without a replacement through the Authority interface.

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

- [x] Task Authority owns all local lifecycle, readiness, and durable dispatch rules.
- [x] Home owns none of those rules.
- [x] Start/Hold race, interpretation atomicity, and crash recovery tests pass.
- [x] Architecture allowlist for old local mutations is empty.

## Phase 6 — Cross-home handoff saga

### Task 6.1 — Define durable transfer intent and receipt

**Description:** Model handoff as a fleet-owned saga across source and destination Authorities without introducing a distributed filesystem transaction.

**Acceptance criteria:**

- [x] Transfer intent binds source/destination home identity, Task ID, exact Generation, request digest, and Operation IDs.
- [x] Destination receipt is durable and idempotent.
- [x] Failure before destination receipt leaves source ownership current.
- [x] A conflicting destination owner quarantines/fails closed.

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

- [x] Destination receives the complete same Task Generation before source retirement.
- [x] Retry resumes from durable receipts without duplicate task creation.
- [x] Projection copy failures do not change ownership truth.
- [x] Cross-home resolution remains in fleet and collects all candidate owners.

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

- [x] Handoff preserves one current owner and one Task Generation.
- [x] Failed or interrupted handoff resumes without dual ownership.
- [x] Integration failures, if any, are distinguished from the pre-existing tagged-test compile baseline.

## Phase 7 — Delivery and remaining projection writes

### Task 7.1 — Inventory and classify direct `.meta` writes

**Description:** Classify every production `fleet → home.WriteMeta` call as authoritative task definition/lifecycle/binding data, runtime projection data, or unrelated Captain metadata before changing it.

**Acceptance criteria:**

- [x] Every production write has one named owner and destination record.
- [x] Authoritative task fields map to a semantic Authority operation.
- [x] Runtime-only projection fields remain outside Aggregate and cannot influence lifecycle.
- [x] Captain supervisor metadata is not forced into Task Authority merely because it shares `.meta` format.

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

- [x] Every Issue Link mutation binds exact Task Generation and Operation ID.
- [x] Parent and related links cannot be promoted to automatic closure policy.
- [x] Reconciliation retries are idempotent and preserve provider evidence.
- [x] `delivery_issuelinks.go` no longer writes task `.meta` directly.

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

- [x] Requested/effective Delivery Plan and allowed transitions are revisioned within the Task Generation.
- [x] Capability attestation references bind exact project, home, config digest, and Task Generation.
- [x] Runtime observation data cannot silently become authoritative task definition.
- [x] `delivery_attestation.go` no longer writes task `.meta` directly.

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

- [x] Merge authorization binds Task Generation, provider identity, PR identity, and immutable head SHA.
- [x] Changed head invalidates stale authorization.
- [x] Git authorization binds the exact generation, worktree binding, capability tier, and expected pre-state.
- [x] Authorization writes are idempotent and directly auditable.

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

- [x] Ship completion cannot use `resolved` as delivery completion.
- [x] Provider evidence binds exact Task Generation, provider identity, and head SHA.
- [x] Failed verification leaves the prior authoritative phase unchanged.
- [x] Delivered/done transitions emit typed audit events and idempotency receipts.

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

- [x] Each merge attempt has a stable Operation ID and exact generation/head binding.
- [x] `remote_unknown` forbids repeated provider mutation and permits only read reconciliation.
- [x] Already-merged and provider false-negative outcomes are idempotent.
- [x] Partial metadata failure does not erase verified remote truth.

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

- [x] Retirement requires exact Task Generation and verified prerequisites.
- [x] Cleanup failure preserves merged truth and returns a resumable retirement receipt.
- [x] Retry resumes retirement only.
- [x] Retired phase and audit event commit once.

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

- [x] No production file in `internal/fleet` or `internal/cli` directly writes Authoritative Task Aggregate files or task `.meta` state.
- [x] Snapshot and task observation prefer canonical Authority state.
- [x] `.status` cannot override newer authoritative lifecycle.
- [x] Captain-specific metadata remains explicitly outside the task-authority grep gate.

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

- [x] Delivery and task projections no longer form competing mutation authority.
- [x] Any remaining `.meta` writes are classified as non-task runtime/Captain projection data.
- [x] Task lifecycle, delivery, and bindings all bind exact Task Generation.
- [x] Focused delivery, snapshot, and observation tests pass.

## Phase 8 — Final deletion, documentation, and gates

### Task 8.1 — Delete legacy home lifecycle and binding code

**Description:** Remove obsolete lifecycle, readiness, endpoint-binding, and worktree-binding implementations after all production callers and behavior tests use Authority.

**Acceptance criteria:**

- [x] `internal/home` retains no lifecycle/readiness mutation or generation-bound binding authority.
- [x] Old behavior tests are replaced through the Authority interface, not simply removed.
- [x] No orphaned helpers or imports remain.
- [x] Focused home/taskauthority/fleet tests stay green.

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

- [x] `internal/home` retains no Authoritative Task Aggregate reader/writer or task migration implementation.
- [x] Supported migration remains explicit through `taskauthorityfs`.
- [x] Existing migration receipts and archives remain readable where required for verification.
- [x] Net code growth is reviewed against deletion of duplicate stores and reach-through tests.

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
