# 0002. Complete the Deep-Module Clean Break and Durable Fleet Lifecycle

* **Status:** Accepted and authorized for continuous implementation and full migration; no formal architecture gates
* **Date:** 2026-07-26
* **Supersedes:** ADR-0001 implementation details
* **Reaffirms:** Four core modules plus five infrastructure leaf modules

## Context & Problem Statement

ADR-0001 established four core modules, but the repository still contains 48 `internal` packages, 35 direct `internal/cli` imports, 151 test files, and 68,491 test lines. The new `domain`, `backend`, and `orchestrator` packages are partly shallow forwarding facades. One measured wall-clock `go test ./... -count=1` run took 65.25 seconds; the agreed warm three-run protocol has not yet been applied.

The hot path for an Uplink Report crosses CLI routing, mailbox persistence, notification, watcher hooks, Captain activation, Processing Ack, retries, and retirement. Callers must know ordering, identities, file layouts, and failure semantics across many seams. The current shape reduces locality, gives little leverage, and makes implementation packages—not module interfaces—the test surface.

## Decision Drivers

* Enforce locality, test speed, and package count as equal success criteria throughout implementation.
* Complete the accepted four-module direction without compatibility facades.
* Make durable lifecycle state recoverable after process crashes.
* Preserve fail-closed identity, delivery, resource, and migration behavior.
* Support production behavior on macOS, Linux, and Windows.
* Keep command shapes stable where they are not deprecated while standardizing AXI output.

## Decision

### 1. Exact package topology

The final topology is nine domain/infrastructure packages plus one CLI adapter:

1. `internal/domain`
2. `internal/backend`
3. `internal/orchestrator`
4. `internal/fleet`
5. `internal/config`
6. `internal/home`
7. `internal/harness`
8. `internal/bootstrap`
9. `internal/testutil`
10. `internal/cli` — excluded from the nine-package invariant because it is the executable input/output adapter

The migration is a clean break. Old internal packages, forwarding aliases, dual runtime paths, and compatibility facades are removed in the same activation. Intermediate commits may temporarily exceed the final package count, but they must build, pass focused tests, and may not add compatibility paths.

### 2. Composition and dependency direction

`internal/cli` is the single composition root. `cmd/munsu` is a minimal executable bootstrap that delegates to it.

* `domain` contains only pure invariants, transition rules, and typed outcomes.
* `orchestrator` and `fleet` do not import each other.
* A calling module defines the port it needs; the composition root injects an adapter.
* `orchestrator` and `fleet` may import `domain`.
* `config` and `home` may be used through narrow interfaces.
* Only `fleet` uses `harness`.
* Only `cli` uses `bootstrap`.
* Only tests use `testutil`.

Durable schemas live with the module that owns their lifecycle and invariants. CLI response schemas live in `cli`; there is no generic contract package.

### 3. Module ownership

#### Domain

`domain` owns Actor Rank and Task Kind rules, allowed combinations, task/Captain/Uplink/decision transition rules, delivery eligibility, typed error categories, retry dispositions, and typed outcomes. It performs no filesystem, environment, process, serialization, or adapter work.

#### Fleet

`fleet` owns authoritative versioned Soldier and Captain records, task lifecycle, Captain lifecycle, backlog/work intake, project registry, scope, decision/hold obligations, delivery evidence, and task operations.

Task operations are:

* `StartTask`
* `PrepareDelivery`
* `RetireTask`

`StartTask` and `RetireTask` are journaled sagas. Acquired endpoints and worktrees have durable lease IDs, task-generation bindings, and fencing tokens. Stale generations cannot mutate or release newer resources.

#### Orchestrator

`orchestrator` owns the observation stream, Uplink Report workflow, watcher, Wake Lease, retries, recovery scheduling, audit events, and AFK policy. The watcher calls typed operations and does not inspect another module's durable file layout.

#### Backend

`backend` is one deep module with internal terminal-endpoint and worktree subsystems. Its external interface consists of typed capability operations rather than adapter objects. Terminal and worktree adapters remain internal seams with shared adapter contract tests.

Resilient Backend Fallback occurs only while resolving a new endpoint. Resolution returns a typed result including the resolved name and `IsFallback`. A task already bound to a backend fails closed if that backend cannot be resolved. Runtime operations never switch adapters implicitly.

### 4. Uplink Report lifecycle

The canonical Uplink journal lives in the receiver's durable home. An obligation is identified by:

`SenderIdentity + TaskID + key`

Each report has a globally unique Message ID and a monotonically increasing generation acquired with compare-and-swap under a per-obligation lock.

The authoritative lifecycle is:

`Intent → Durable → Acked → Retired`

Notification and receiver observation are orthogonal facts, not required lifecycle predecessors:

* Notification: `Queued | Attempted | Accepted`
* Receiver observation: `Unseen | Received`

A receiver may process or acknowledge a durable report while notification remains `Queued`, and Processing Ack does not require an earlier Receive operation. `Stalled` is an operational outcome. Corrupt state fails the affected operation with `CorruptState` and places the obligation in Quarantine. A new generation supersedes an active previous generation. Reopen always creates a new generation. A Processing Ack binds the exact Message ID and generation and closes only the delivery obligation; it does not mean follow-up work is complete.

The receiver-local versioned journal is the source of truth. Receiver envelopes, wake records, sender pending/open evidence, and audit projections are repairable projections. The receiver journal is updated before sender projections are retired.

Durability determines report success. A failed Delivery Notification returns a successful `Queued` outcome and is retried. Recovery uses exponential backoff with deterministic jitter, a cap, per-receiver rate limiting, and journaled retry deadlines. Pending obligations never expire automatically; they may become `Stalled/NeedsOperator`.

The journal uses canonical versioned JSON with a checksum. Writes use the strongest documented platform durability behavior:

* Unix: sync temporary file, atomic rename, sync parent directory.
* Windows: flush temporary file, write-through replacement, verify resulting handle and identity.

Only capability-verified local filesystems are supported. Unsupported filesystems and network shares fail preflight. Corrupt records quarantine only the affected obligation and fail closed.

### 5. State machines

All transitions are compare-and-swap operations on the current generation. An edge not listed below returns typed `Conflict` with `RetryNever`.

Lifecycle phase and operator attention are separate durable dimensions. Attention is `None | NeedsOperator | Quarantined`. Failure is recorded as a typed cause/outcome; it is not encoded by combining phase and attention into one state name.

#### Uplink Obligation

| From | To | Authority | Guard |
|---|---|---|---|
| absent | Intent | sender | capacity available; identity valid |
| Intent | Durable | orchestrator | canonical journal write and checksum durable |
| Durable | Acked | receiver | exact Message ID and generation; Ack validates |
| Acked | Retired | orchestrator | sender projections reconciled |
| any non-Retired phase | same phase + attention `Quarantined` | migration/orchestrator | corrupt or contradictory canonical state |
| quarantined phase | Retired | General | explicit abandon writes a durable disposition/tombstone outside the corrupt record; corrupt generation remains immutable |
| quarantined or active generation | Intent (new generation) | sender + General | explicit supersession creates a clean generation; old generation remains immutable |
| active non-quarantined generation | Intent (new generation) | sender | explicit supersession/reopen |

Notification and receiver observation are orthogonal facts. They do not gate Ack.

| Notification fact | Next fact | Authority | Guard |
|---|---|---|---|
| absent or Queued | Attempted | orchestrator | retry deadline due; receiver rate limit available |
| Attempted | Accepted | backend notification adapter | submission acknowledged for exact Message ID/generation |
| Attempted | Queued | orchestrator | typed retry disposition schedules next attempt |

`Accepted` is terminal for the generation's notification fact. Any later operator-triggered delivery is appended as attempt history and does not regress `Accepted`.

| Receiver observation | Next fact | Authority | Guard |
|---|---|---|---|
| Unseen | Received | receiver | exact durable envelope validated |
| Received | Received | receiver | idempotent repeat |

Processing Ack may transition `Durable → Acked` from either receiver-observation fact and from any notification fact.

#### Task shared core

| From | To | Authority | Guard |
|---|---|---|---|
| absent | Planned | General `PlanTask`, or assigned Captain `PlanTask` within project scope | valid Actor Rank/Task Kind/project scope |
| Planned | Starting | General or assigned Captain `StartTask` | backlog claim and start intent journaled |
| Starting | Running | fleet `AdvanceStartTask` | endpoint, worktree, and harness launch verified |
| Starting or Running | Recovering | fleet `RecoverTask` | retryable lifecycle/resource failure |
| Starting, Running, or Recovering | Blocked | General `HoldTask`, or assigned Captain `HoldTask` within scope | explicit external dependency/hold; prior resumable phase recorded |
| Blocked | prior resumable phase | General `ResumeTask`, or assigned Captain `ResumeTask` within scope | blocking obligation resolved; stored resume phase remains valid |
| Starting, Running, Recovering, or Blocked | same phase + attention `NeedsOperator` | fleet `EscalateTask` | bounded retry exhausted or unsafe compensation |
| Recovering | prior resumable phase | fleet `CompleteTaskRecovery` | recovery guards restored; stored resume phase remains valid |
| any phase with attention `NeedsOperator` | Starting (new generation) + attention `None` | General `ReopenTask` | explicit reason and new generation |
| any non-Retired phase with attention `NeedsOperator` | Retiring | General `AbandonTask` | abandon disposition journaled; resource cleanup remains mandatory |

Task Kind guards define the completion path:

| Task Kind | From | To | Authority | Guard |
|---|---|---|---|---|
| `ship` | Running | DeliveryReady | General or assigned Captain `PrepareDelivery` within scope | immutable delivery identity/evidence captured |
| `scout` | Running | ReportReady | Soldier `SubmitScoutReport` for its exact task generation | required report evidence durable |
| `ship` | DeliveryReady | Blocked | General `HoldTask`, or assigned Captain `HoldTask` within scope | delivery dependency or decision obligation open; resume phase recorded |
| `scout` | ReportReady | Blocked | General `HoldTask`, or assigned Captain `HoldTask` within scope | report acceptance or decision obligation open; resume phase recorded |
| `ship` | Blocked | DeliveryReady | General `ResumeTask`, or assigned Captain `ResumeTask` within scope | ship-ready guards restored and stored resume phase matches |
| `scout` | Blocked | ReportReady | General `ResumeTask`, or assigned Captain `ResumeTask` within scope | scout-ready guards restored and stored resume phase matches |
| `ship` | DeliveryReady | Recovering | fleet `RecoverTask` | delivery evidence refresh or resource failure is retryable; resume phase recorded |
| `scout` | ReportReady | Recovering | fleet `RecoverTask` | report projection or resource failure is retryable; resume phase recorded |
| `ship` | Recovering | DeliveryReady | fleet `CompleteTaskRecovery` | delivery-ready guards restored and stored resume phase matches |
| `scout` | Recovering | ReportReady | fleet `CompleteTaskRecovery` | report-ready guards restored and stored resume phase matches |
| `ship` | DeliveryReady | same phase + attention `NeedsOperator` | fleet `EscalateTask` | bounded ready-state recovery exhausted |
| `scout` | ReportReady | same phase + attention `NeedsOperator` | fleet `EscalateTask` | bounded ready-state recovery exhausted |
| `ship` | DeliveryReady | Retiring | General or assigned Captain `RetireTask` within scope | delivery eligible; obligations closed |
| `scout` | ReportReady | Retiring | General or assigned Captain `RetireTask` within scope | report accepted; obligations closed |
| `ship` or `scout` | Retiring | Retired | fleet `CompleteRetirement` | endpoint/worktree release verified; backlog projection complete |
| `ship` or `scout` | Retiring | same phase + attention `NeedsOperator` | fleet `EscalateRetirement` | bounded cleanup retries exhausted; resources remain owned or quarantined |

`captain-supervisor` uses the Captain graph rather than the ship/scout completion path.

#### Captain

| From | To | Authority | Guard |
|---|---|---|---|
| absent | Provisioning | General `ProvisionCaptain` | captain identity and project scope valid |
| Provisioning | Idle | fleet `CompleteCaptainProvisioning` | home, record, harness, and endpoint ready |
| Idle | Activating | fleet `ActivateCaptain` from an orchestrator activation request | request binds assigned Captain and exact Uplink generation; CAS succeeds |
| Idle | Recovering | fleet `RecoverCaptain` | authoritative endpoint is absent/dead or durable recovery work is pending |
| Activating | Active | fleet `CompleteCaptainActivation` | recognized-agent submission acknowledged |
| Active | Idle | assigned Captain `AcceptActivation`, or fleet `SetCaptainIdle` | current activation work accepted or no pending activation |
| Activating or Active | Recovering | fleet `RecoverCaptain` | endpoint or activation failure is retryable |
| Recovering | Idle or Activating | fleet `CompleteCaptainRecovery` | endpoint restored; stored resume phase and retry request remain valid |
| Provisioning, Idle, Activating, Active, or Recovering | same phase + attention `NeedsOperator` | fleet `EscalateCaptain` | bounded attempts exhausted |
| any phase with attention `NeedsOperator` | Provisioning (new generation) + attention `None` | General `ReopenCaptain` | explicit recover action and reason |
| Idle, Active, or any phase with attention `NeedsOperator` | Retiring | General `RetireCaptain` or `AbandonCaptain` | scoped obligations and resource disposition recorded; cleanup remains mandatory |
| Retiring | Retired | fleet `CompleteCaptainRetirement` | endpoint/resources released and registry finalized |
| Retiring | same phase + attention `NeedsOperator` | fleet `EscalateCaptainRetirement` | bounded cleanup retries exhausted |

#### Decision/Hold

| From | To | Authority | Guard |
|---|---|---|---|
| absent | Open | Soldier or assigned Captain `OpenDecision` within task scope | required resolving authority and dependency recorded |
| Open | Held | assigned Captain `HoldDecision` within scope, or General globally | explicit hold reason/evidence |
| Held | Open | assigned Captain `ReleaseDecisionHold` within scope, or General globally | hold released but decision unresolved |
| Open or Held | Resolved | the declared Captain within scope, or General when declared/global | resolution reason/evidence valid |
| Resolved | Closed | fleet operation consuming the decision | exact decision generation is consumed by a guarded transition |
| Closed | Open (new generation) | original requester `ReopenDecision`, or General | explicit reason and new obligation generation |

### 6. Task delivery and retirement

Delivery adapters return normalized evidence rather than business verdicts. Evidence binds task generation, provider, repository, PR/MR number, base ref, head ref, immutable head SHA, and capture time. `domain` computes eligibility; `fleet` performs explicit retirement.

`PrepareDelivery` validates the task/worktree generation, invokes the delivery adapter, captures immutable evidence, and records polling requirements. It does not merge or retire.

`RetireTask` requires valid delivery evidence, closed Uplink and decision obligations, matching resource leases, and a journaled retire intent. It fences new mutation, releases the endpoint, releases the worktree, verifies no owned resources remain, marks the task Retired, then completes the backlog projection. Cleanup failure leaves phase `Retiring` with attention `NeedsOperator`; `Retired` is impossible until resource disposal is verified.

### 7. Wake and watcher semantics

Wake delivery is at least once. Each wake has a stable ID. Claiming creates a renewable Wake Lease with owner identity and a fencing token. Stale owners cannot acknowledge after takeover. Uplink wakes are projections rebuilt from the journal.

Watcher ownership uses a durable renewable lease with process/build identity, generation, and fencing token. A new watcher fences the old owner before attempting best-effort process termination. `EnsureWatcher` is called explicitly by session start, AFK activation, and Captain activation/recovery.

AFK is an orchestrator policy. AFK becomes active only after watcher readiness is verified. Prompt mutation is allowed only for backend-confirmed recognized agents; raw SendKeys fallback to an unknown target is forbidden.

### 8. Home, config, harness, and bootstrap

`home` is the cross-platform authority for canonical homes, provenance, layout/schema version, required directories, safe path construction, encoded path segments, root containment, no-follow/reparse-point checks, locks, durable replacement, backup paths, and permissions/ACL mapping. Durable directories are user-private (`0700` semantics) and files are user-private (`0600` semantics).

`config` owns typed settings, storage, parsing, precedence, defaults, and validation. Long-running modules receive immutable versioned snapshots. A valid generation is atomically published at operation boundaries; invalid generations are rejected while the prior snapshot remains active.

`harness` is a verified adapter registry containing detection evidence, launch templates, capability probe contracts, and turn-end hooks. Fleet owns launch and lifecycle.

`bootstrap` returns diagnostics and typed repair plans. It does not mutate automatically; CLI renders or explicitly executes actions.

### 9. Error, outcome, and audit contracts

Errors have typed categories and wrapped causes:

* `Validation`
* `Conflict`
* `Unavailable`
* `CorruptState`
* `Unsafe`
* `Internal`

Each failure carries `RetryNever`, `RetryLater`, or `RetryAfter(duration)`. Partial successes such as `Queued`, `Fallback`, and `AlreadyAcked` are typed outcomes, not warning errors. Aggregate migration may succeed with a typed quarantine summary, but an operation targeting a corrupt obligation returns `CorruptState` and fails closed.

Durable mutations emit typed audit events containing identifiers, generation, Actor Rank, task/key, phase, outcome, timestamps, payload hash, and typed error evidence. Raw prompts and report payloads are not copied into the audit stream. Completed history is compacted into versioned checkpoints plus a bounded append-only tail. Quarantine and migration evidence remain until explicit operator acknowledgement.

### 10. Operator authority

Safe operator actions are inspect, retry-now, acknowledge-quarantine, supersede with a new generation, and explicit abandon with reason. Quarantine acknowledgement is an orthogonal audit/activation fact and does not change lifecycle phase or attention. Direct journal editing or deletion is forbidden.

* General may mutate obligations.
* Captain may inspect and retry obligations within its scope.
* Soldier may inspect reports it sent.

Every mutation records actor identity/rank, target generation, reason, before/after phase, timestamp, and audit event.

### 11. Migration and activation

`munsu migrate` is explicit and supports inspect, dry-run, backup, apply, and status. General owns one fleet-wide migration lease and records the exact inventory of the General home plus every registered Captain home. Soldier execution homes are not independent migration roots.

Before schema mutation:

* Record a fleet-wide home, process, endpoint, and active-Soldier inventory.
* Acquire and fence a global migration lease for new binaries.
* Stop and drain all legacy watchers, Captains, and active Soldiers, including processes not represented by a migration-root home.
* Verify through durable records and OS process inspection that no legacy writer remains.
* Apply the verified legacy-writer isolation mechanism: generation-scoped new state roots, old-root relocation/isolation, disabled restart entry points, verified process termination, and proof that old writers cannot affect the new canonical state. New binaries resolve canonical state only through the generation-specific Activation Record and root. Old binaries may recreate legacy paths, but those paths can never become canonical for or affect the activated generation.
* Abort without schema mutation if termination, drain, verification, or guard installation fails.
* Create a backup with manifest, checksums, permissions metadata, and restore smoke test.
* Complete a dry-run import and capability preflight.

Old pending artifacts are imported once into the current journal schema. Malformed or conflicting reports are quarantined independently. Migration is forward-only and does not dual-read or dual-write.

Activation writes the generation-specific Activation Record only after General verifies build/schema identity, backup and restore evidence, migration audit, canaries, writer quiescence, and explicit quarantine acknowledgements.

### 12. AXI and skill engine

CLI translates environment variables into typed context. Core modules do not read process environment. CLI owns text/JSON rendering and exit-code mapping.

All commands use one versioned response envelope with `schema_version`, `kind`, `status`, typed data, typed error, evidence, and retry disposition. The output flag is `--output text|json`. JSON mode writes exactly one envelope to stdout; human diagnostics go to stderr. Deprecated `--json` aliases are removed.

Skill metadata comes from a versioned typed embedded manifest. Each skill declares allowed Actor Ranks and fails closed for unknown ranks, skills, or malformed metadata. `munsu init` installs only `munsu-ops`; auxiliary skills remain embedded and are read through `munsu skill list/show`.

### 13. Verification and delivery

The default warm `go test ./... -count=1` suite contains hermetic critical-contract tests and must have a median under five seconds across three runs on macOS, Linux, and Windows, with no run above eight seconds. **Superseded for owner-clean v1:** ADR-0008 drops the numeric per-suite budget for v1; the registry lock-overlap guard (independent Project/Captain aggregate operations must complete within the 2s overlap budget) remains binding.

Required extended jobs cover race tests, state-machine properties, fuzzing, platform filesystem contracts, and real adapter integrations. A versioned test manifest defines all required commands per platform.

Activation canaries cover happy path, queued notification recovery, projection crashes, supersession/stale Ack, corruption quarantine, capacity backpressure, and backend fallback. They run against a synthetic home and a shadow copy restored from a verified backup.

Implementation proceeds continuously in small buildable slices under strict feature freeze. Focused verification runs after each slice; full cross-platform, migration, restore, topology, performance, and canary verification remains required before activation, but no formal review gate blocks coding progress.

## Alternatives Rejected

### Keep shallow facades during migration

Rejected because the old implementation remains the real interface, package count does not fall, and tests duplicate rather than replace coverage.

### Dual-read or dual-write for rollback

Rejected because it creates two runtime truths and prolongs migration complexity. Recovery is forward-only from a verified backup.

### Put all shared types in domain

Rejected because domain would become a shallow shared-types package rather than a deep pure-rules module.

### Let the watcher inspect and repair all files

Rejected because it leaks module storage layouts and destroys locality.

### Primitive-identical durability across platforms

Rejected because Windows does not expose a documented exact equivalent to Unix parent-directory fsync. Behavioral parity uses each platform's strongest documented primitives and capability-gated local filesystems.

## Consequences

### Positive

* Ten total internal packages, with nine domain/infrastructure modules and one CLI adapter.
* One authoritative lifecycle record for each task, Captain, and Uplink obligation.
* Recovery and retries share the same typed operations used by foreground commands.
* Tests exercise module interfaces and critical contracts rather than forwarding declarations.
* Platform limitations are explicit and fail closed.

### Negative / Trade-offs

* Large forward-only migration requiring full fleet quiescence.
* No binary rollback after schema migration begins; recovery requires restoring the verified backup.
* Windows storage behavior must be implemented and verified before activation; failures are implementation defects, not a separate authorization gate.
* Existing internal package consumers and deprecated output flags break in the clean cut.

## Explicitly Out of Scope

* New terminal or worktree adapters.
* Network or distributed durable storage.
* Cryptographic authentication between same-user local processes.
* TUI/UI redesign beyond AXI CLI output.
* Remote fleet protocol.
* Legacy compatibility layers beyond one-shot state import.
