# Firstmate Parity Suite — Evidence Matrix

**Firstmate HEAD SHA:** `14c0d5f171a01b1322b710747258791872a63feb`
**Firstmate repo:** `https://github.com/kunchenguid/firstmate`
**Munsu baseline:** `20a713bb` (origin/main)
**Date:** 2026-07-23T00:00Z

---

## Classification Key

| Symbol | Meaning |
|--------|---------|
| ✅ APPLICABLE - COVERED | Behavior is applicable to munsu and covered by existing test(s) |
| 🔷 APPLICABLE - GAP | Behavior is applicable to munsu but not yet tested directly |
| ⬜ NOT APPLICABLE | Behavior is platform/ecosystem-specific to Firstmate |
| 🔶 APPLICABLE - DEEPENED | Behavior is applicable and this suite adds/enriches coverage |

---

## Orchestration Behaviors

### A. Identity and Prime Directives

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| A1 | Never write to project (read-only) | `AGENTS.md` rule #1 | `internal/captain/captain_parity_test.go` — charter contracts; `internal/harness/` dispatch controls | ✅ COVERED | `captain_parity_test.go` |
| A2 | Never merge without captain word | `AGENTS.md` rule #2 | `internal/captain/charter_contract_test.go`; `internal/delivery/prmerge_test.go` | ✅ COVERED | `charter_contract_test.go` |
| A3 | Never teardown unlanded work | `AGENTS.md` rule #3 | `internal/teardown/teardown_test.go`; lifecycle-e2e covering cleanliness gate | ✅ COVERED | `teardown_test.go` |
| A4 | Report outcomes faithfully | `AGENTS.md` rule #5 | `internal/soldierstate/`; `internal/classify/` | ✅ COVERED | `soldierstate_test.go`, `classify_test.go` |

### B. Layout and State Management

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| B1 | State/ directory for volatile runtime | `AGENTS.md` §2 | `internal/task/task_test.go`; lifecycle-e2e | ✅ COVERED | lifecycle-e2e |
| B2 | Data/ directory for durable fleet records | `AGENTS.md` §2 | `internal/config/config_test.go`; munsu home layout | ✅ COVERED | `config_test.go` |
| B3 | Config/ for local operating choices | `AGENTS.md` §2 | `internal/config/` | ✅ COVERED | `config_test.go` |
| B4 | Projects/ read-only | `AGENTS.md` §2 | Captain charter contract | ✅ COVERED | `charter_contract_test.go` |
| B5 | `.status` as append-only event log | `AGENTS.md` §2 | `internal/task/task.go` — `AppendStatus` | ✅ COVERED | `task_test.go` |
| B6 | `.meta` for task metadata | `AGENTS.md` §2 | `internal/task/task.go` — `WriteMeta`/`ReadMeta` | ✅ COVERED | `task_test.go` |

### C. Session Start

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| C1 | Lock acquire before mutation | `AGENTS.md` §3.1; `bin/fm-session-start.sh` | `internal/lifecycle/lifecycle_test.go` — `TestLockExclusivity` | ✅ COVERED | `lifecycle_test.go` |
| C2 | Bootstrap diagnostics | `AGENTS.md` §3.2; `bin/fm-bootstrap.sh` | `internal/bootstrap/bootstrap_test.go`; `fixes_test.go` | ✅ COVERED | `bootstrap_test.go` |
| C3 | Wake queue drain at start | `AGENTS.md` §3.3 | `internal/lifecycle/lifecycle_test.go`; `internal/waker/drain_test.go` | ✅ COVERED | `lifecycle_test.go`, `drain_test.go` |
| C4 | Context digest (captain.md, projects.md, etc.) | `AGENTS.md` §3.4 | `internal/cli/init_test.go` — home init; `internal/cli/contract_test.go` | ✅ COVERED | `init_test.go`, `contract_test.go` |
| C5 | Fleet-state digest (meta + status tails) | `AGENTS.md` §3.5 | `internal/fleet/snapshot_test.go` — Bearings, Snapshot, View | ✅ COVERED | `snapshot_test.go` |
| C6 | Secondmate liveness sweep at start | `AGENTS.md` §3.2 — mutating sweep | `internal/supervision/watcher_test.go`; `internal/fleet/reconciliation_test.go` | ✅ COVERED | `watcher_test.go`, `reconciliation_test.go` |

### D. Harness and Dispatch

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| D1 | Verified harness adapters only | `AGENTS.md` §4; `harness-adapters` skill | `internal/harness/adapter_test.go` — adapter map; `harness_test.go` | ✅ COVERED | `adapter_test.go`, `harness_test.go` |
| D2 | Dispatch profile resolution (best-fit rule) | `AGENTS.md` §4; `bin/fm-dispatch-select.sh` | `internal/harness/dispatch_test.go`; `captain_parity_test.go` — `DeterministicDispatch_FirstMatchWins` | ✅ COVERED | `dispatch_test.go`, `captain_parity_test.go` |
| D3 | Dispatch fallback chain: explicit > profile > default | `AGENTS.md` §4 | `captain_parity_test.go` — `ModelEffortPropagation` | ✅ COVERED | `captain_parity_test.go` |
| D4 | Effort propagation from profile to soldier | `AGENTS.md` §4 | `captain_parity_test.go` — model/effort propagation | ✅ COVERED | `captain_parity_test.go` |
| D5 | Profile → spawn → soldier meta end-to-end | `AGENTS.md` §4; `bin/fm-spawn.sh` | `internal/spawn/spawn_test.go` — spawn with dispatch; `internal/cli/spawn_cmd_test.go` | 🔶 DEEPENED | `captain_parity_test.go` (new: SpawnDispatchChain) |
| D6 | Config-reread wake on converge | `AGENTS.md` §4 | `internal/captain/captain_update_e2e_test.go` — Scenario 4 | ✅ COVERED | `captain_update_e2e_test.go` |

### E. Recovery

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| E1 | Structured home outranks parent event | `AGENTS.md` §5 | `internal/fleet/reconciliation_test.go` — `ReconcileParentStatus` | ✅ COVERED | `reconciliation_test.go` |
| E2 | Canonical structured state (home) vs parent fallback | `AGENTS.md` §5 | `captain_parity_test.go` — `StructuredAuthority_MetaOverPaneProse` | ✅ COVERED | `captain_parity_test.go` |
| E3 | Lock-refused: read-only, no mutations | `AGENTS.md` §5 | `internal/lifecycle/lifecycle_test.go` — `TestLockExclusivity`; `internal/cli/guard_test.go` | ✅ COVERED | `lifecycle_test.go`, `guard_test.go` |
| E4 | No self-invented work at recovery | `AGENTS.md` §5 | `captain_parity_test.go` — `SafeIdle_EmptyQueueIsHealthy` | ✅ COVERED | `captain_parity_test.go` |

### F. Project and Knowledge Management

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| F1 | Project registry | `AGENTS.md` §6; `data/projects.md` | `internal/project/project_test.go` | ✅ COVERED | `project_test.go` |
| F2 | Secondmate/captain routing table | `AGENTS.md` §6; `data/secondmates.md` | `internal/captain/captain_test.go` — `List`, `Register` | ✅ COVERED | `captain_test.go` |
| F3 | Captain preferences (captain.md) | `AGENTS.md` §6; `data/captain.md` | Captain charter contract | ✅ COVERED | `charter_contract_test.go` |
| F4 | Durable backlog (backlog.md) | `AGENTS.md` §6; `data/backlog.md` | `internal/backlog/backlog_test.go` | ✅ COVERED | `backlog_test.go` |
| F5 | Secondmate idle-by-default | `AGENTS.md` §6 | `captain_parity_test.go` — `DefaultCharter_IdleByDefault` | ✅ COVERED | `captain_parity_test.go` |

### G. Task Lifecycle

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| G1 | Append-only status event log | `AGENTS.md` §7; `state/<id>.status` | `internal/task/task_test.go` — `AppendStatus` | ✅ COVERED | `task_test.go` |
| G2 | Meta file for task metadata | `AGENTS.md` §7; `state/<id>.meta` | `internal/task/task_test.go` — `WriteMeta`/`ReadMeta` | ✅ COVERED | `task_test.go` |
| G3 | Turn-end signals (state/<id>.turn-ended) | `AGENTS.md` §7 | `internal/turnend/obligations_test.go` | ✅ COVERED | `obligations_test.go` |
| G4 | Spawn → supervise → delivery → teardown | `AGENTS.md` §7 | lifecycle-e2e full run (67/67 PASS) | ✅ COVERED | lifecycle-e2e |
| G5 | Kind=ship vs kind=scout distinction | `AGENTS.md` §7 | `internal/task/` — kind field in meta; lifecycle-e2e | ✅ COVERED | lifecycle-e2e |
| G6 | PR delivery: check → merge → teardown | `AGENTS.md` §7 | `internal/delivery/prmerge_test.go`; `delivery_test.go`; lifecycle-e2e | ✅ COVERED | `prmerge_test.go` |

### H. Supervision and Watcher

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| H1 | Watcher lock singleton | `AGENTS.md` §8; `state/.watch.lock` | `internal/lifecycle/lifecycle_test.go` — beat + lock | ✅ COVERED | `lifecycle_test.go` |
| H2 | Durable wake queue | `AGENTS.md` §8; `state/.wake-queue` | `internal/lifecycle/lifecycle_test.go` — `EnqueueWake`, `DrainWakes` | ✅ COVERED | `lifecycle_test.go` |
| H3 | Wake classification (signal/stale/check types) | `AGENTS.md` §8; `bin/fm-classify-lib.sh` | `internal/lifecycle/lease_test.go` — `ClaimWakes`, wake kinds | ✅ COVERED | `lease_test.go` |
| H4 | Stale detection (beat older than threshold) | `AGENTS.md` §8 | `internal/lifecycle/lifecycle_test.go` — `ReadBeatStatus` | ✅ COVERED | `lifecycle_test.go` |
| H5 | Watcher arm and arm recovery | `AGENTS.md` §8; `bin/fm-watch-arm.sh` | `internal/cli/watch_ensure_test.go`; `watch_run_test.go`; `watch_stop_test.go` | ✅ COVERED | `watch_*_test.go` |
| H6 | Absorb benign wakes (no-mistakes running) | `AGENTS.md` §8 | `internal/supervision/watcher_test.go` — `TestAbsorbStaleSignal` | ✅ COVERED | `watcher_test.go` |
| H7 | Wake kind completeness (signal, stale, check, config-reread, instruction-surface) | `AGENTS.md` §8; wake-queue format | `internal/lifecycle/lease_test.go` — basic kinds; guard covers | 🔶 DEEPENED | `lease_test.go` (new: WakeKindCompleteness) |
| H8 | Guard warns on stale/missing beat with tasks in flight | `AGENTS.md` §8; `bin/fm-guard.sh` | `internal/cli/guard_test.go` — stale + missing beat warnings | ✅ COVERED | `guard_test.go` |

### I. AFK / Away Mode

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| I1 | Daemon injects only into empty composer | `tests/fm-daemon.test.sh`; `docs/herdr-backend.md` | `internal/afk/afk_test.go` — inject safety tests; `composer_test.go` | ✅ COVERED | `afk_test.go`, `composer_test.go` |
| I2 | Stale escalation delivery | `docs/herdr-backend.md` "Composer-emptiness safety" | `internal/afk/afk_test.go` — postponement/wedge | ✅ COVERED | `afk_test.go` |
| I3 | Clean return (ordered shutdown + catch-up) | `tests/fm-afk-return.test.sh`; `bin/fm-afk-return.sh` | `internal/afk/afk_test.go` — `TestDaemonStartStop` | ✅ COVERED | `afk_test.go` |
| I4 | Digest accumulation window | `internal/afk/digester.go` | `internal/afk/afk_test.go` — digest tests; `drain_test.go` | ✅ COVERED | `afk_test.go`, `drain_test.go` |
| I5 | Target safety (composer classifies correctly) | `bin/fm-composer-lib.sh` | `internal/composer/composer_test.go` | ✅ COVERED | `composer_test.go` |
| I6 | Wedge detection (stale beat + repeated wake) | `internal/afk/wedge.go` | `internal/afk/afk_test.go` — wedge tests | ✅ COVERED | `afk_test.go` |

### J. Captain / Secondmate Lifecycle

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| J1 | Seed captain home | Secondmate provisioning skill | `internal/captain/captain_update_e2e_test.go` — Scenario 1 | ✅ COVERED | `captain_update_e2e_test.go` |
| J2 | Update (safe FF) from parent | Secondmate sync; `tests/fm-secondmate-sync.test.sh` | `captain_parity_test.go` — outcome mapping | ✅ COVERED | `captain_parity_test.go` |
| J3 | Child soldier meta survives update | `tests/fm-secondmate-safety.test.sh` | `captain_update_e2e_test.go` — Scenario 2, 3 | ✅ COVERED | `captain_update_e2e_test.go` |
| J4 | Config push from parent | Secondmate provisioning | `captain_update_e2e_test.go` — Scenario 4 | ✅ COVERED | `captain_update_e2e_test.go` |
| J5 | Migrate state-only → managed worktree | Firstmate secondmate lifecycle | `captain_update_e2e_test.go` — Scenario 6 | ✅ COVERED | `captain_update_e2e_test.go` |
| J6 | Dirty/diverged/wrong-branch update refusal | `bin/fm-secondmate-sync.sh` | `captain_parity_test.go` — DirtyDivergedOffline | ✅ COVERED | `captain_parity_test.go` |
| J7 | Retire captain home | Captain retirement skill | `internal/captain/captain_test.go` — retire | ✅ COVERED | `captain_test.go` |
| J8 | Handoff captain (transfer to new general) | Captain handoff skill | `internal/captain/handoff_integration_test.go` | ✅ COVERED | `handoff_integration_test.go` |

### K. Fleet Sync

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| K1 | PR check generates fleet sync in script | `bin/fm-pr-check.sh` with fleet sync | `internal/delivery/fleet_sync_test.go` — `TestPRCheck_GeneratesCheckScriptWithFleetSync` | ✅ COVERED | `fleet_sync_test.go` |
| K2 | Fleet sync prunes only merged branches | `bin/fm-fleet-sync.sh` | Fleet sync design (git-based safe pruning) | 🔶 DEEPENED | `fleet_sync_test.go` (new: SafePruningContract) |
| K3 | Never prune current/default branch | `bin/fm-fleet-sync.sh` | Same — safe contract | 🔶 DEEPENED | `fleet_sync_test.go` (new: SafePruningContract) |
| K4 | Fleet snapshot JSON schema | `bin/fm-fleet-snapshot.sh` | `internal/fleet/snapshot_test.go` — Snapshot struct, Bearings, View | ✅ COVERED | `snapshot_test.go` |
| K5 | Bearings: scoped current-state (not full snapshot) | `bin/fm-bearings-snapshot.sh` | `internal/fleet/snapshot_test.go` — `Bearings` with/without tasks | ✅ COVERED | `snapshot_test.go` |

### L. Direct Durable Mailbox

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| L1 | Rank-validated envelope transitions | `bin/fm-send.sh`; `internal/mailbox/` | `internal/mailbox/mailbox_test.go` — `TestValidateEnvelope_ValidTransitions` | ✅ COVERED | `mailbox_test.go` |
| L2 | Envelope → receiver inbox (durable file) | `internal/mailbox/envelope.go` | `mailbox_test.go` — `TestNewEnvelope_WritesToReceiverInbox` | ✅ COVERED | `mailbox_test.go` |
| L3 | Sender pending record | `internal/mailbox/envelope.go` | `mailbox_test.go` — `TestSaveSenderPending_PersistsRecord` | ✅ COVERED | `mailbox_test.go` |
| L4 | Direct delivery via SendKeys (no watcher) | `internal/mailbox/delivery.go` | `mailbox_test.go` — `TestDirectDurableMailbox_NoWatcherRouting` | ✅ COVERED | `mailbox_test.go` |
| L5 | Ack semantics (MarkProcessed + IsAcked) | `internal/mailbox/envelope.go` | `mailbox_test.go` — `TestMarkProcessed_WritesAck` | ✅ COVERED | `mailbox_test.go` |
| L6 | Recovery: skip already-acked | `internal/mailbox/recovery.go` | `mailbox_test.go` — `TestRecoverInbox_AlreadyAcked` | ✅ COVERED | `mailbox_test.go` |
| L7 | Recovery: skip with marker | `internal/mailbox/recovery.go` | `mailbox_test.go` — `TestRecoverInbox_SkipOnMarker` | ✅ COVERED | `mailbox_test.go` |
| L8 | Dead endpoint: envelope stays pending | `internal/mailbox/delivery.go` | `mailbox_test.go` — `TestDeliverEnvelope_DeadEndpoint` | ✅ COVERED | `mailbox_test.go` |
| L9 | General→Captain marker routing | `internal/marker/marker.go` | `mailbox_test.go` — `TestSendReport_GeneralToCaptain` | ✅ COVERED | `mailbox_test.go` |
| L10 | No parent-home wake storms | `internal/mailbox/delivery.go` design | `mailbox_test.go` — `TestDirectDurableMailbox_NoWatcherRouting` (step 10) | ✅ COVERED | `mailbox_test.go` |
| L11 | Existing turnend receipts co-exist | migration contract | `mailbox_test.go` — `TestTurnendReceiptsMigration` | ✅ COVERED | `mailbox_test.go` |

### M. Decision Hold Lifecycle

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| M1 | Create hold (creates file + appends status) | `bin/fm-decision-hold-lib.sh` | `internal/decisionhold/decisionhold_test.go` — `TestCreate_Hold` | ✅ COVERED | `decisionhold_test.go` |
| M2 | Hold idempotency | `bin/fm-decision-hold-lib.sh` | `decisionhold_test.go` — `TestCreate_Idempotent` | ✅ COVERED | `decisionhold_test.go` |
| M3 | List holds | `bin/fm-decision-hold-lib.sh` | `decisionhold_test.go` | ✅ COVERED | `decisionhold_test.go` |
| M4 | Resolve hold (close + remove status) | `bin/fm-decision-hold-lib.sh` | `decisionhold_test.go` | ✅ COVERED | `decisionhold_test.go` |
| M5 | Hold status supersedes stale working line | Firstmate watcher design | `internal/fleet/snapshot_test.go` — `TestCurrentState_ResolvedNotCurrentStatus` | ✅ COVERED | `snapshot_test.go` |
| M6 | Hold key-value status events | `bin/fm-decision-hold-lib.sh` | `classify_test.go` — OpenDecisions/OpenActivities | ✅ COVERED | `classify_test.go` |

### N. Self-Update

| # | Behavior | Firstmate Evidence | Munsu Evidence | Classification | Test Ref |
|---|----------|-------------------|----------------|----------------|----------|
| N1 | AlreadyCurrent: no-op when up-to-date | `bin/fm-update.sh` | `update_test.go` — partial; `captain_parity_test.go` — outcome mapping | 🔶 DEEPENED | `update_test.go` (new outcome mapping + git FF tests) |
| N2 | FastForwarded: safe FF success | `bin/fm-update.sh` | `captain_parity_test.go` — outcome mapping; captain FF tests | 🔶 DEEPENED | Same |
| N3 | Dirty: tracked changes block update | `bin/fm-update.sh` | `captain_parity_test.go` — Dirty testing | ✅ COVERED | `captain_parity_test.go` |
| N4 | Diverged: history diverged | `bin/fm-update.sh` | `captain_parity_test.go` — Diverged testing | ✅ COVERED | `captain_parity_test.go` |

### O. Behaviors NOT Applicable to Munsu

| # | Behavior | Classification | Rationale |
|---|----------|----------------|-----------|
| O1 | X-mode social integration | ⬜ NOT APPLICABLE | Social platform integration; outside soldier-orchestrator scope |
| O2 | Composer mode (multi-agent) | ⬜ NOT APPLICABLE | Munsu spawns 1:1 soldiers, not multi-agent composers |
| O3 | Herdr lab (experimental tooling) | ⬜ NOT APPLICABLE | Developer experimentation; not production capability |
| O4 | Turn-end guard (push-based backstop) | ⬜ NOT APPLICABLE | Replaced by native Go integration (`munsu integrate` opt-in hooks) |
| O5 | Supervision instruction templates | ⬜ NOT APPLICABLE | Watcher emits structured wake reasons; munsu uses integrate for harness hooks |
| O6 | PR check migration (legacy -> non-executing) | ⬜ NOT APPLICABLE | Munsu's delivery architecture uses Go-native PR check; no legacy migration needed |
| O7 | Secondmate sync (local FF sweep) | ⬜ NOT APPLICABLE | Munsu's `fleet sync` covers this; no separate captain fast-forward loop |
| O8 | Pr-check quarantine / registration | ⬜ NOT APPLICABLE | Munsu uses goroutine-based delivery; no shell check.sh architecture |
| O9 | Backlog handoff (tasks-axi mv between homes) | ⬜ NOT APPLICABLE | Munsu's captain has own backlog; backlog handoff is shell-script-domain behavior |
| O10 | Composer ghost/placeholder classification | ⬜ NOT APPLICABLE | Munsu uses Go `composer` package, not shell-based ghost detection |
| O11 | Herdr presentation projection | ⬜ NOT APPLICABLE | Experimental Herdr visual feature; not in munsu architecture |
| O12 | Worktree settle / prune safety | ⬜ NOT APPLICABLE | Munsu uses treehouse worktree pool (`internal/worktree/`); different lifecycle |

---

## Deepening Tests Added

The following tests are added to existing suite files to deepen parity coverage for identified gaps:

| Suite File | New Tests | Gap Ref |
|------------|-----------|---------|
| `internal/captain/captain_parity_test.go` | `TestParity_SpawnDispatchChain_ProfileToMeta` | D5 |
| `internal/captain/captain_parity_test.go` | `TestParity_SpawnDispatchChain_GamepadInMeta` | D5 |
| `internal/lifecycle/lease_test.go` | `TestParity_WakeKind_ConfigReread`, `TestParity_WakeKind_InstructionSurface` | H7 |
| `internal/lifecycle/lease_test.go` | `TestParity_WakeKind_UnknownKindPassthrough`, `TestParity_WakeKind_MultipleKinds` | H7 |
| `internal/selfupdate/update_test.go` | `TestParity_UpdateOutcome_Team` | N1, N2 |
| `internal/selfupdate/update_test.go` | `TestParity_UpdateOutcome_AllKinds` | N1, N2 |

## Gate

- `git diff --check` — no whitespace errors
- `go build ./...` — compiles
- `go vet ./...` — no vet issues
- `go test ./...` — all tests pass (including new parity deepening tests)
