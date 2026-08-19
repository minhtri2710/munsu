# Workflow topology matrix

This is a **documentation-only** artifact for [issue #546](https://github.com/minhtri2710/munsu/issues/546).
It records the current workflow topology and its invariants as they exist in the tree
today. It states no new topology or delivery policy, proposes no behavior change, and
is not authoritative over the documents or code it maps — it is a navigation aid that
points each trace and invariant to its owner.

Every row cites the owning Go symbol and the document/ADR that grounds it. Nothing here
should be trusted over the cited authority; where they disagree, the cited code or ADR
wins and this matrix should be corrected.

## Grounding

| Reference | Role in this matrix |
|---|---|
| `docs/adr/0008-owner-clean-architecture-and-pre-public-v1-reset.md` | Topology and ownership rules, one canonical path, task/config/snapshot/assignment boundaries, delivery authorization, config assignment to captains |
| `docs/adr/0015-the-soldier-closes-its-own-terminal-handoff.md` | Terminal receipt = notification artifact, not a transport; the writer closes its own ack |
| `docs/architecture.md` | Module map, home data model, task state, configuration, captain lifecycle, supervision/wake delivery |
| `docs/port-mapping.md` | Command ↔ Go package capability mapping |
| `COMMANDS.md` | Command surface grouped by lifecycle phase |
| `internal/**` | Concrete owners and symbols |

## 1. Rank traces

### 1.1 General → Soldier (direct)

The General (`MUNSU_ROLE=general`) is the fleet orchestrator. It drives a soldier task
through creation, brief, spawn, supervision/steering, delivery, teardown, and closure.
"Supervision" here means observation and downlink steering, not supervision-metric
generation; metrics are out of scope for this matrix.

| Step | Command (`COMMANDS.md`) | Owner module | Authoritative symbol |
|---|---|---|---|
| Create task | `munsu task add` | `internal/taskauthority` | `Canonical.Create` (`internal/taskauthority/canonical_ops.go`) |
| Start task | `munsu task start` | `internal/taskauthority` | `Canonical.Start` (`canonical_ops.go`) |
| Scaffold brief | `munsu brief` | `internal/fleet` | `fleet.Scaffold` (`internal/fleet/brief.go`; CLI wiring `internal/cli/session_cmd.go`) |
| Spawn soldier | `munsu spawn` | `internal/fleet` + `internal/taskauthority` | `fleet.Spawn` / `Runner.Run` (`internal/fleet/spawn_spawn.go`, `internal/fleet/spawn_runner.go`); `Canonical.BindWorktree`/`BindEndpoint` (`internal/taskauthority/canonical_binding.go`); `Canonical.BeginSpawn`/`RecordLaunch` (`internal/taskauthority/canonical_launch.go`) |
| Downlink steering | `munsu send` | `internal/cli` + `internal/fleet` + `internal/home` | `fleet.SendToSoldier` (`internal/fleet/captain_soldier_queue.go`); durable mailbox (`internal/home/mailbox_envelope.go`, `mailbox_store.go`, `mailbox_receiver.go`) |
| Current-state read | `munsu soldier-state`, `munsu fleet snapshot` | `internal/fleet` | `fleet.ReadWithProbe` (`internal/fleet/soldierstate_soldierstate.go`); `fleet.NewCanonicalCurrentState` (`internal/fleet/taskauthority_reads.go`); `fleet.PhaseFromProjection` (`internal/fleet/snapshot.go`) |
| Delivery | `munsu delivery ...` | `internal/fleet` + `internal/taskauthority` + `internal/domain` | `fleet.Deliver` (`internal/fleet/delivery_deliver.go`); `Canonical.AuthorizeDelivery`/`RevokeDeliveryAuthorization`/`CommitDeliveryOutcome` (`internal/taskauthority/canonical_delivery.go`); `PR.CanMerge`/`Review.IsApproving` (`internal/domain/domain.go`) |
| Teardown | `munsu teardown` | `internal/cli` + `internal/fleet` + `internal/taskauthority` + `internal/orchestrator` | `fleet.RetireTask` (`internal/fleet/retirement_task.go`); `Canonical.Retire` (`internal/taskauthority/canonical_retirement.go`); `Canonical.BeginCleanup`/`CompleteCleanup`/`AbortCleanup` (`internal/taskauthority/canonical_cleanup.go`); `orchestrator.VerifyRetirementContinuity` (`internal/orchestrator/retirement_journal.go`); CLI `munsu teardown` (`internal/cli/spawn_cmd.go`) |
| Close task | `munsu task done` | `internal/taskauthority` | `Canonical.Complete` (`canonical_ops.go`) |

### 1.2 Captain → Soldier

A Captain (`MUNSU_ROLE=captain`) is a persistent domain supervisor, seeded from a
General home and owning its own captain home (`internal/fleet/captain_captain.go`).
Tasks enter the captain's flock through a durable handoff, and inheritable config
reaches it through a config assignment; the captain then spawns and oversees soldiers
on the same spawn path used by a General (the runner runs inside the captain home).

| Step | Command (`COMMANDS.md`) | Owner module | Authoritative symbol |
|---|---|---|---|
| Seed captain | `munsu captain seed` | `internal/fleet` | `fleet.SeedCaptain` (`internal/fleet/captain_captain.go`) |
| Launch captain | `munsu captain launch` | `internal/fleet` | `fleet.Launch`, resolves via `internal/harness` and fails closed for unknown harnesses (`internal/fleet/captain_captain.go`) |
| Assign tasks | `munsu captain handoff` | `internal/fleet` + `internal/taskauthority` | `fleet.Handoff` (durable Fleet-owned Task Transfer journal, `internal/fleet/task_handoff_transaction.go`); `Canonical.ReserveTransfer`/`CommitTransfer`/`ReceiveTransfer`/`ActivateTransfer` (`internal/taskauthority/canonical_transfer.go`) |
| Config assignment | `munsu captain config-push` | `internal/fleet` + `internal/config` | `configPushWithResult` / `publishResolvedSnapshot` (`internal/fleet/captain_captain.go`); `config.StorePublishedSnapshot` / `LoadPublishedSnapshot` (`internal/config/published_snapshot.go`) |
| Spawn / steer / deliver / tear down soldiers | (same `munsu spawn`/`send`/`delivery`/`teardown` path, run inside the captain home) | `internal/fleet` + `internal/taskauthority` | `Runner.Run` incl. `checkCaptainBacklogAuthority` / `resolveParentCaptainID` (`internal/fleet/spawn_runner.go`); soldier queue/downlink `fleet.SendToSoldier` (`internal/fleet/captain_soldier_queue.go`) |
| Captain uplink | `munsu report` (captain role) | `internal/orchestrator` | `orchestrator.Report` / `Recover` / `NotifyParentWithTransport` (`internal/orchestrator/uplink_uplink.go`) |

## 2. Boundaries: task / config / snapshot / assignment

| Boundary | Rule | Owner | Authority |
|---|---|---|---|
| Task | The canonical Task Aggregate (generation/revision, lifecycle phase, Dispatch Holds, delivery authorization/outcomes, transfer reservations, launch/retirement evidence) is the only task truth. `.meta`/`.status` are post-commit projections written by fleet/CLI projection writers; they are never authoritative and never decide lifecycle. Current-state reads are canonical-first. | `internal/taskauthority` (truth), `internal/home` (durable mechanics + generic `.meta`/`.status` primitives) | `docs/architecture.md` "Task state"; `docs/port-mapping.md` "Task meta + status records"; ADR-0008 §2; ADR-0007 §7 |
| Config | Typed settings, defaults, validation, Project Overlays and Captains live in `internal/config`; flat key files under `config/`. Core modules never read process environment or infer authority from the working directory; `internal/cli` translates env/flags into typed overrides. | `internal/config`, `internal/cli` | `docs/architecture.md` "Configuration"; ADR-0008 §6 |
| Snapshot | A Config Snapshot is resolved once at an operation boundary and stays immutable for that operation (`config.ResolvedSnapshot`). It is published as a signed document a captain can `StorePublishedSnapshot`/`LoadPublishedSnapshot`. | `internal/config` (`resolved_snapshot.go`, `published_snapshot.go`) | ADR-0008 §6 |
| Assignment | The General is the single resolution authority. Fleet distributes the resolved snapshot to a captain through a journaled Config Assignment over the canonical Uplink path (`config-push`); a captain verifies and durably accepts the assigned identity, generation and digest. A captain never re-resolves, never reads the General's files, never falls back to local configuration, and never uses terminal nudges as authority. | `internal/fleet` (assignment), `internal/config` (snapshot), `internal/orchestrator` (Uplink) | ADR-0008 §3, §6; `internal/fleet/captain_captain.go` (`configPushWithResult`, `publishResolvedSnapshot`) |

## 3. Invariants: report, deliver, ack, teardown

| Invariant | Rule | Owner | Authority |
|---|---|---|---|
| Report (uplink) | A soldier's **terminal scout `done`** routes through `orchestrator.DeliverWake` (`internal/orchestrator/wakedelivery_deliver.go`); **material** soldier/captain states route through the canonical mailbox uplink `orchestrator.Report`; all other states use a local `DeliverWake`. **Delivery truth is never captured or committed through the terminal report path** — terminal reports and retirement only consume canonical delivery authorization/outcome truth, and delivery execution runs exclusively through the Fleet journaled `Deliver` operation. | `internal/cli/report_cmd.go` (`newReportCmd`), `internal/orchestrator` | `docs/architecture.md` "Supervision and wake delivery"; `internal/cli/report_cmd.go` (comment above the terminal-report routing) |
| Deliver | `fleet.Deliver` is the **sole** delivery executor (`internal/fleet/delivery_deliver.go`). Task Authority issues an immutable `DeliveryAuthorization` bound to the exact Task Generation and Operation ID (`AuthorizeDelivery`/`RevokeDeliveryAuthorization`/`CommitDeliveryOutcome`, `internal/taskauthority/canonical_delivery.go`); acceptance rules (`PR.CanMerge`, `Review.IsApproving`) live solely in `internal/domain`. CLI helpers and the terminal report path are not parallel delivery implementations. | `internal/fleet`, `internal/taskauthority`, `internal/domain` | ADR-0008 §2, §3; `docs/architecture.md` "Delivery acceptance"; `docs/port-mapping.md` "Delivery invariants" |
| Ack (terminal handoff) | The **writer closes its own terminal handoff** (ADR-0015). `DeliverWake` writes the captain receipt + `InitTaskObligations` and then `WriteAck` + `CompleteTaskObligation` **unconditionally**, whether or not the parent is a captain home. A pending receipt can then only exist if the process died between write and ack — a genuine fail-closed condition. | `internal/orchestrator/wakedelivery_deliver.go` (`DeliverWake`), `internal/orchestrator/turnend_obligations.go` (`WriteReceipt`/`WriteAck`/`InitTaskObligations`/`CompleteTaskObligation`) | ADR-0015, full |
| Terminal receipt = notification, not transport | The terminal receipt file survives **only** as a notification artifact: `CaptainActivationHook` (`internal/orchestrator/captain_relay.go`) invokes `ActivateOnReceiptWithTransport` (`internal/orchestrator/wakedelivery_deliver.go`) on the captain watcher's activation hook, scanning receipts regardless of ack state to nudge the captain's pane. Nothing relays a receipt into a parent home; the mailbox uplink (`orchestrator.Report` / `Recover`) is the only uplink. A terminal signal may wake an implementation loop, but terminal delivery is not semantic acknowledgment. | `internal/orchestrator/captain_relay.go`, `internal/orchestrator/uplink_uplink.go` | ADR-0015; ADR-0008 §4 |
| Teardown | `fleet.RetireTask` (`internal/fleet/retirement_task.go`) transitions the canonical task with `Canonical.Retire` and closes the probe→dispose window with a durable `CleanupClaim` committed atomically with the retired phase, reconciled by `BeginCleanup`/`CompleteCleanup`/`AbortCleanup` (`internal/taskauthority/canonical_cleanup.go`). `orchestrator.VerifyRetirementContinuity` (`internal/orchestrator/retirement_journal.go`) refuses teardown without `--force` while a `ReportRelay` obligation is open — now satisfiable because `DeliverWake` closes its own ack (ADR-0015). This matrix records the invariant only; it makes no claim about teardown outcomes. | `internal/fleet`, `internal/taskauthority`, `internal/orchestrator` | ADR-0008 §2, §5; ADR-0015; `docs/architecture.md` "Task Authority" / "Supervision and wake delivery" |

## 4. Relationship to other traces

- The **General → Captain** trace (seed/launch/update/retire/config-push, `internal/fleet/captain_captain.go`) is the source of the captain home this matrix's Captain → Soldier trace runs inside. The General observes only typed captain health and never inspects or mutates captain filesystem state (ADR-0008 §4).
- **Supervision** (`munsu watch`, `munsu guard`, `munsu afk`, `internal/orchestrator`) observes endpoints and interprets observations, but never mutates lifecycle directly; lifecycle, delivery, binding and handoff mutations go through `internal/taskauthority`. Supervision-metric generation is out of scope for this matrix.

This matrix is intentionally narrow: it covers the two named rank traces, the
task/config/snapshot/assignment boundaries, and the report/deliver/ack/teardown
invariants. It does not add runtime behavior, config-handoff changes, supervision
metrics, topology policy, teardown outcomes, or compatibility/migration/fallback paths,
and it does not restate unrelated historical documentation.
