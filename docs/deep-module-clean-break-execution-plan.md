# Deep-Module Clean Break Execution Plan

## Status

Authorized for continuous production implementation and full migration by explicit user instruction on 2026-07-27. Commit and push remain unauthorized.

## Goal

Replace the current 48-package internal architecture with four deep core modules, five infrastructure leaf modules, and one CLI adapter. Perform a forward-only durable-state migration, preserve critical fleet contracts, complete the original resilience/AXI/skill plan, and activate once through a General-authorized evidence verification.

## Continuous delivery model

The user explicitly authorized full production implementation and migration on 2026-07-27 and removed the formal staged-gate authorization model. Work proceeds continuously in small buildable slices. The following are required correctness properties, not permission gates:

1. **Locality:** workflow knowledge, durable schemas, bugs, and verification concentrate in the owning module.
2. **Package count:** final topology is exactly nine domain/infrastructure packages plus `internal/cli`.
3. **Test speed:** warmed `go test ./... -count=1` median is under five seconds on macOS, Linux, and Windows; no run exceeds eight seconds.
4. **Migration safety:** generation-specific canonical roots, verified writer quiescence, backup/restore smoke, one-shot import, quarantine, and Activation Record publication-last.
5. **Clean break:** no forwarding facade, compatibility alias, dual runtime path, dual-read, or dual-write survives cutover.

The incomplete migration ledger remains informational evidence and a source of known risks; it is not a completion certificate or implementation blocker.

## Target package map

```text
internal/
├── domain/
├── backend/
├── orchestrator/
├── fleet/
├── config/
├── home/
├── harness/
├── bootstrap/
├── testutil/
└── cli/
```

`internal/cli` is the single composition root. `cmd/munsu` is a minimal executable bootstrap. `orchestrator` and `fleet` do not import each other.

## Delivery constraints

* One integration branch, incremental buildable commits, single activation.
* Strict feature freeze; only required bug/security fixes may enter.
* Command names and required positional arguments remain stable unless already deprecated.
* Existing internal Go package interfaces receive no compatibility aliases.
* Scope includes consolidation, migration, resilience guards, AXI output, embedded skills, and RBAC.
* Out of scope: new adapters, distributed storage, local cryptographic auth, TUI redesign, remote fleet protocol, and compatibility layers.

---

## Workstream A — Inventory evidence and continuous task slicing

### Task 0.1 — Inventory the entire build and runtime surface

**Scope:** Inventory the entire build and runtime surface: Go source under `cmd/` and `internal/`, tests, embedded assets/skills/templates, scripts, configuration, fixtures, generated runtime contracts, and every file that participates in build or runtime behavior.

**Acceptance criteria:**
- [ ] Every current package, source file, test, asset, script, configuration, fixture, and generated runtime contract has exactly one destination or deletion rationale.
- [ ] Hidden initialization, package globals, process spawning, environment reads, filesystem mutations, embed directives, and generation inputs are recorded.
- [ ] No build/runtime file remains unmapped.

**Verification:**
- [ ] Ledger counts match the complete repository build/runtime inventory.
- [ ] A machine check reports zero unmapped build/runtime files.

**Dependencies:** None.

### Task 0.2 — Inventory exported symbols and callers

**Scope:** Map every exported symbol and every direct caller/import before defining final ports.

**Acceptance criteria:**
- [ ] Every exported type, function, variable, and error has a destination/deletion rationale.
- [ ] Every CLI and internal caller is mapped to a target typed operation.
- [ ] Package-init hooks and overridable global function variables have explicit replacement plans.

**Verification:**
- [ ] Static inventory and `go list`/AST evidence are stored with the implementation evidence.
- [ ] Zero exported symbols or direct callers are unmapped.

**Dependencies:** Task 0.1.

### Task 0.3 — Inventory durable artifacts and tests

**Scope:** Map all files/directories written under homes and all existing tests.

**Acceptance criteria:**
- [ ] Every durable artifact has an owning target module, schema disposition, and migration rule.
- [ ] Every test maps to a surviving critical contract, adapter contract, replacement test, or deletion rationale.
- [ ] Packages including agentsmd, brief, capability, composer, ghurl, glurl, herdrprune, marker, nostatus, runner, selfupdate, soldier, and stow have explicit dispositions.

**Verification:**
- [ ] Zero durable artifacts and test files are unmapped.
- [ ] Ledger review identifies conflicts before port design begins.

**Dependencies:** Tasks 0.1–0.2.

### Task 0.4 — Re-slice implementation work from the ledger

**Scope:** Continuously convert relevant ledger evidence into session-sized implementation tasks as each subsystem is touched.

**Acceptance criteria:**
- [ ] Every task has one primary lifecycle/module concern, explicit dependencies, acceptance criteria, and verification.
- [ ] No task combines independent delivery, retirement, Captain, decision, watcher, or test-infrastructure migrations.
- [ ] Any task projected to touch more than five files is split or explicitly approved with a narrow mechanical rationale.

**Verification:**
- [ ] Architecture review confirms no XL multi-subsystem tasks remain in implementation workstreams.

**Dependencies:** Tasks 0.1–0.3.

### Continuous review A

Inventory evidence is updated opportunistically as affected surfaces are implemented. Incomplete inventory must be stated honestly and does not block unrelated implementation.

---

## Workstream B — Topology, ports, domain, and platform behavior

### Task 1.1 — Freeze the dependency contract

**Scope:** Define machine-checkable package and import rules before moving implementation.

**Acceptance criteria:**
- [ ] A package-policy test asserts the exact target package allowlist.
- [ ] It forbids `fleet ↔ orchestrator` imports and enforces `domain` as a core leaf.
- [ ] It enforces narrow infra use: harness→fleet, bootstrap→CLI, testutil→tests.

**Verification:**
- [ ] The policy test fails against the current tree for known violations.
- [ ] `go list` evidence is emitted in machine-readable form.

**Dependencies:** inventory workstream.

### Task 1.2 — Specify calling-module ports

**Scope:** Define the minimum typed ports needed by fleet and orchestrator without implementing moved behavior.

**Acceptance criteria:**
- [ ] Fleet ports cover backend capabilities, delivery evidence, clock, and audit emission.
- [ ] Orchestrator ports cover fleet task/Captain operations, home storage, clock, and backend notification capability.
- [ ] Ports return typed outcomes/errors and never expose adapter objects or filesystem paths as authority.

**Verification:**
- [ ] Compile-time adapter stubs demonstrate composition without core cross-imports.
- [ ] Interface review applies the deletion test and interface-as-test-surface rule.

**Dependencies:** Task 1.1.

### Task 1.3 — Prototype cross-platform home primitives

**Scope:** Prove secure and durable storage behavior before architectural approval.

**Acceptance criteria:**
- [ ] Unix prototype proves temp sync, atomic replacement, parent-directory sync, no-follow containment, per-obligation locks, and user-private permissions.
- [ ] Windows prototype proves FlushFileBuffers, write-through replacement, resulting-handle identity verification, reparse-point containment, LockFileEx, and user-only ACLs.
- [ ] Network shares and unsupported filesystems are detected and rejected.
- [ ] Legacy-writer isolation prototype uses generation-scoped new state roots, relocates/isolates old roots, disables legacy restart entry points, detects out-of-registry processes, verifies process termination, and proves old binaries cannot affect the new canonical state.
- [ ] New binaries resolve canonical state only through the generation-specific Activation Record/root; the prototype proves legacy paths recreated by old binaries cannot become canonical or affect that generation.
- [ ] The prototype is treated as an unproven hypothesis until all three platform jobs pass; same-user permissions alone are not accepted as sufficient fencing.
- [ ] Capability results are typed and can block migration preflight.

**Verification:**
- [ ] Platform contract tests pass on macOS, Linux, and Windows.
- [ ] Failure of any filesystem or legacy-writer-guard primitive blocks platform workstream and migration authorization.

**Dependencies:** Task 1.1.

### Task 1.4 — Establish typed domain contracts

**Scope:** Encode pure types, state machines, error categories, retry dispositions, and outcomes.

**Acceptance criteria:**
- [ ] Actor Rank and Task Kind are separate validated fields.
- [ ] Uplink, task, Captain, and decision transition graphs reject invalid transitions.
- [ ] Reopen/supersession creates a new generation.
- [ ] No domain code imports filesystem, environment, process, or other core modules.

**Verification:**
- [ ] Property tests prove monotonic generations and transition invariants.
- [ ] Domain package remains a leaf in the import graph.

**Dependencies:** Task 1.1.

### Continuous review B

Continuously verify topology, ports, state-machine contracts, and platform storage behavior. Any failure is an implementation defect that must be fixed before activation.

---

## Workstream C — Canonical journals, migration, and recovery

### Task 2.1 — Build the home storage authority

**Scope:** Implement versioned layouts, safe path encoding, durable replacement, locks, backup/restore, and capability preflight behind `home`.

**Acceptance criteria:**
- [ ] Callers cannot construct authoritative durable paths directly.
- [ ] All durable state defaults to user-private permissions/ACLs.
- [ ] Per-obligation and global migration leases expose fencing tokens.
- [ ] Backup includes manifest, checksums, permissions metadata, and restore verification.

**Verification:**
- [ ] Platform contract tests and path fuzz tests pass.
- [ ] Symlink/reparse-point and traversal attacks fail closed.

**Dependencies:** Tasks 1.2–1.4.

### Task 2.2 — Implement receiver-local Uplink journal

**Scope:** Implement canonical journal records and projection repair.

**Acceptance criteria:**
- [ ] Obligation identity is SenderIdentity + TaskID + key.
- [ ] Message ID and generation CAS bind every Ack and projection.
- [ ] Lifecycle implements Intent→Durable→Acked→Retired.
- [ ] Notification facts (`Queued|Attempted|Accepted`) and receiver-observation facts (`Unseen|Received`) are orthogonal and do not gate Ack.
- [ ] Envelope, sender pending/evidence, wake, and audit projections are rebuildable.
- [ ] Corruption at any non-retired phase sets attention `Quarantined` only on the affected obligation.
- [ ] Quarantine acknowledgement is an orthogonal audit/activation fact and does not mutate phase/attention.
- [ ] Explicit abandon writes a durable disposition/tombstone outside the corrupt record; supersession creates a clean generation; neither repairs the canonical record in place.

**Verification:**
- [ ] Failure injection before/after every durable phase proves forward recovery.
- [ ] Stale Ack and superseded generation tests fail closed.
- [ ] Capacity and retry scheduling tests use a deterministic Clock.

**Dependencies:** Task 2.1.

### Task 2.3 — Implement Wake Lease and watcher lease

**Scope:** Replace drain/delete semantics and PID authority with renewable leases.

**Acceptance criteria:**
- [ ] Wake delivery is at least once with stable Wake IDs.
- [ ] Lease renewal, expiry, redelivery, Ack, and fencing are idempotent.
- [ ] Watcher takeover fences the prior generation before process cleanup.
- [ ] EnsureWatcher is an explicit typed operation.

**Verification:**
- [ ] Race tests cover concurrent claim, renew, expiry, and stale Ack.
- [ ] Crash tests prove wake reconstruction from Uplink journals.

**Dependencies:** Tasks 2.1–2.2.

### Task 2.4 — Implement authoritative work-intake and project records

**Scope:** Build backlog dependencies/holds/ready projection plus project registry and scope records.

**Acceptance criteria:**
- [ ] Claim/start/complete transitions are versioned and CAS-protected.
- [ ] Project identity, canonical path, Captain assignment, and scope have one owner.
- [ ] Legacy backlog/project/scope artifacts have migration mappings from inventory workstream.

**Verification:**
- [ ] Focused state-transition and migration tests pass.

**Dependencies:** Tasks 1.2, 1.4, 2.1.

### Task 2.5 — Implement task record and StartTask saga

**Scope:** Build the shared task core and kind-guarded ship/scout paths through Running.

**Acceptance criteria:**
- [ ] Planned→Starting→Running transitions and Blocked/Recovering/NeedsOperator attention branches match the ADR table.
- [ ] StartTask uses bounded retry, fenced resource leases, and idempotent compensation.
- [ ] `captain-supervisor` is routed to the Captain graph rather than ship/scout completion states.

**Verification:**
- [ ] State-machine properties and resource-fencing race tests pass.
- [ ] Failure injection covers every acquisition, launch, retry, and compensation phase.

**Dependencies:** Tasks 2.1, 2.4.

### Task 2.6 — Implement delivery preparation

**Scope:** Implement PrepareDelivery, normalized evidence capture, and ship DeliveryReady guards.

**Acceptance criteria:**
- [ ] Delivery evidence binds task generation and immutable head identity.
- [ ] Ship DeliveryReady transitions, blocked/recovery resume, and NeedsOperator attention match ADR-0002.

**Verification:**
- [ ] Delivery identity and stale-evidence tests pass.

**Dependencies:** Task 2.5.

### Task 2.7 — Implement scout report preparation

**Scope:** Implement SubmitScoutReport, durable report evidence, and ReportReady guards.

**Acceptance criteria:**
- [ ] Scout ReportReady transitions, blocked/recovery resume phase, and NeedsOperator attention match ADR-0002.
- [ ] Report evidence binds the exact task generation.

**Verification:**
- [ ] Report durability, resume, and stale-generation tests pass.

**Dependencies:** Task 2.5.

### Task 2.8 — Implement task retirement and abandon

**Scope:** Implement RetireTask and General-authorized AbandonTask for ship/scout tasks.

**Acceptance criteria:**
- [ ] RetireTask enforces obligations/evidence and finalizes only after verified resource disposal.
- [ ] AbandonTask records a disposition and transitions to Retiring; it cannot transition directly to Retired.
- [ ] Failed/quarantined cleanup leaves phase Retiring with attention NeedsOperator.

**Verification:**
- [ ] Obligation guard, destructive cleanup, abandon, and resource-quarantine tests pass.

**Dependencies:** Tasks 2.5–2.7.

### Task 2.9 — Implement Captain lifecycle

**Scope:** Implement Captain provision, activation, idle, recovery, retirement, and abandon behavior.

**Acceptance criteria:**
- [ ] Captain transitions, including Idle→Recovering and NeedsOperator attention, match ADR-0002.
- [ ] Bounded recovery preserves lifecycle phase and sets attention `NeedsOperator`.
- [ ] General-authorized retire/abandon records scoped obligation and resource disposition.

**Verification:**
- [ ] Captain state-machine and failure tests pass.

**Dependencies:** Tasks 2.1, 2.4.

### Task 2.10 — Implement decision and hold obligations

**Scope:** Implement Open/Held/Resolved/Closed decision obligations.

**Acceptance criteria:**
- [ ] Decision resolution and closure enforce declared authority, reason, evidence, generation, and dependent consumption.

**Verification:**
- [ ] Decision property and authority tests pass.

**Dependencies:** Tasks 2.1, 2.4.

### Task 2.11 — Implement fleet-wide migration

**Scope:** Add explicit migration inventory, dry-run, backup, apply, quarantine, and status.

**Acceptance criteria:**
- [ ] General records the General/Captain home inventory plus every legacy watcher, Captain, endpoint, and active Soldier process.
- [ ] All legacy writers are stopped/drained and absence is verified through records and OS process inspection.
- [ ] The verified generation-scoped state-root isolation prevents old binaries from affecting new canonical state; old-root permissions alone are not treated as sufficient.
- [ ] The global migration lease fences new binaries; it is not treated as sufficient to fence legacy binaries.
- [ ] Drain, termination, verification, or guard failure aborts before any new-schema mutation.
- [ ] Old artifacts import once into current schema; malformed records quarantine independently.
- [ ] Migration is forward-only and records build/schema identity.

**Verification:**
- [ ] Dry-run and restore-smoke tests pass on synthetic and representative shadow homes.
- [ ] Re-running migration is idempotent.
- [ ] Binary rollback is rejected after schema activation.

**Dependencies:** Tasks 2.1–2.10.

### Continuous review C

Keep canonical state, recovery, migration, and state-machine failure evidence current throughout implementation.

---

## Workstream D — Physical consolidation and test replacement

### Task 3.1 — Consolidate backend implementation

**Scope:** Move session, worktree, hometag, and related provider implementation into backend internal subsystems.

**Acceptance criteria:**
- [ ] External callers use typed capability operations only.
- [ ] Resolution-only fallback returns resolved backend and IsFallback.
- [ ] Task-bound backend failures fail closed.
- [ ] Old backend/session/worktree/hometag packages are removed.

**Verification:**
- [ ] Shared terminal and worktree adapter contract suites pass.
- [ ] Direct imports of removed packages are zero.

**Dependencies:** Workstreams B–C.

### Task 3.2 — Absorb Uplink and audit implementation into orchestrator

**Scope:** Move mailbox/uplink/wakedelivery/event behavior behind the receiver-local journal interface.

**Acceptance criteria:**
- [ ] Orthogonal notification/receiver facts and Ack semantics match ADR-0002.
- [ ] Old Uplink-related packages are removed after all callers move.

**Verification:**
- [ ] Uplink canaries pass through the orchestrator interface.

**Dependencies:** Tasks 2.2–2.3, 3.1.

### Task 3.3 — Absorb watcher ownership and Wake Lease into orchestrator

**Scope:** Move watcher lease, takeover, wake claim/renew/Ack, and queue projection behavior.

**Acceptance criteria:**
- [ ] Watcher uses typed fleet/backend ports and does not glob foreign layouts.
- [ ] Wake delivery and fencing contracts survive package removal.

**Verification:**
- [ ] Watcher takeover and Wake Lease race tests pass.

**Dependencies:** Tasks 2.3, 3.2.

### Task 3.4 — Absorb recovery scheduling into orchestrator

**Scope:** Move retry deadlines, projection repair scheduling, and typed recovery dispatch.

**Acceptance criteria:**
- [ ] Recovery calls typed fleet/backend ports and preserves retry dispositions.
- [ ] No global hook/function-variable seams remain.

**Verification:**
- [ ] Recovery scheduling and crash-repair tests pass.

**Dependencies:** Tasks 2.2–2.3, 2.9–2.10, 3.3.

### Task 3.5 — Absorb AFK policy into orchestrator

**Scope:** Move AFK activation, batching/digest policy, and safe agent activation.

**Acceptance criteria:**
- [ ] AFK requires watcher readiness and recognized-agent submission.

**Verification:**
- [ ] AFK readiness and safe-activation tests pass.

**Dependencies:** Tasks 3.3–3.4.

### Task 3.6 — Absorb work intake, project, and task-start implementation into fleet

**Scope:** Move backlog/project/scope/task/spawn/soldier-state behavior.

**Acceptance criteria:**
- [ ] Only fleet mutates authoritative work-intake and task records.
- [ ] StartTask and resource-fencing contracts survive package removal.

**Verification:**
- [ ] Work-intake and StartTask critical contracts pass.
- [ ] Fleet does not import orchestrator.

**Dependencies:** Tasks 2.4–2.5, 3.1.

### Task 3.7 — Absorb delivery preparation into fleet

**Scope:** Move provider evidence capture and PrepareDelivery behavior.

**Acceptance criteria:**
- [ ] Provider adapters return normalized evidence.
- [ ] Ship DeliveryReady guards and immutable identity binding survive package removal.

**Verification:**
- [ ] Delivery preparation and stale-evidence tests pass.

**Dependencies:** Tasks 2.6, 3.6.

### Task 3.8 — Absorb retirement into fleet

**Scope:** Move RetireTask, integration finalization, teardown, and fenced resource cleanup.

**Acceptance criteria:**
- [ ] Retirement and abandon guards survive package removal.
- [ ] Resource generations fence all destructive cleanup.
- [ ] No abandon path can mark Retired before verified disposal.

**Verification:**
- [ ] Retirement crash/cleanup/NeedsOperator-attention tests pass.

**Dependencies:** Tasks 2.8, 3.7.

### Task 3.9 — Absorb Captain lifecycle into fleet

**Scope:** Move Captain provision, activation, recovery, retirement, and abandon behavior.

**Acceptance criteria:**
- [ ] Captain transition table and bounded recovery survive package removal.
- [ ] Captain endpoint mutations use backend typed operations.

**Verification:**
- [ ] Captain lifecycle and recovery tests pass.

**Dependencies:** Tasks 2.9, 3.6.

### Task 3.10 — Absorb decision and hold obligations into fleet

**Scope:** Move decision/hold persistence, authority, resolution, and closure behavior.

**Acceptance criteria:**
- [ ] Open/Held/Resolved/Closed transitions and generation rules survive package removal.

**Verification:**
- [ ] Decision authority and consumption tests pass.

**Dependencies:** Tasks 2.10, 3.6.

### Task 3.11 — Consolidate domain and infra leaves

**Scope:** Remove generic shared packages and place schemas/rules in owning modules.

**Acceptance criteria:**
- [ ] Domain contains only pure rules/types/outcomes.
- [ ] Config emits immutable versioned snapshots.
- [ ] Harness is the verified adapter registry.
- [ ] Bootstrap returns diagnostics and repair plans without automatic mutation.
- [ ] Contract/home/helper packages outside the target map are removed.

**Verification:**
- [ ] Exact package policy passes.
- [ ] `go vet ./...` passes.

**Dependencies:** Tasks 3.1–3.10.

### Task 3.12 — Replace domain transition tests

**Scope:** Replace domain tests with state-machine properties and typed error/outcome contracts.

**Acceptance criteria:**
- [ ] Uplink, task-kind, Captain, and decision tables have complete property coverage.

**Verification:**
- [ ] Property suite passes with deterministic generated transitions.

**Dependencies:** Tasks 3.2–3.11.

### Task 3.13 — Replace Uplink and backend tests

**Scope:** Replace overlapping tests with Uplink lifecycle contracts and backend adapter contracts.

**Acceptance criteria:**
- [ ] Uplink, Ack/idempotency, quarantine paths, and backend fail-closed behavior remain covered.
- [ ] Redundant implementation tests are removed with ledger rationale.

**Verification:**
- [ ] Focused fast/race/fuzz/platform suites pass.

**Dependencies:** Tasks 3.1–3.3, 3.11–3.12.

### Task 3.14 — Replace fleet lifecycle and destructive-safety tests

**Scope:** Replace task saga, delivery, retirement, Captain, decision, and resource-cleanup tests through fleet interfaces.

**Acceptance criteria:**
- [ ] Task sagas, Captain lifecycle, decisions, and destructive resource safety remain covered.
- [ ] Testutil provides scripted typed-operation fakes and deterministic Clock, not full provider mocks.
- [ ] Every deleted legacy test has a ledger rationale.

**Verification:**
- [ ] Fleet fast/race/property suites pass.

**Dependencies:** Tasks 3.6–3.12.

### Task 3.15 — Replace watcher and Wake Lease tests

**Scope:** Replace watcher ownership, takeover, wake redelivery, recovery, and AFK tests.

**Acceptance criteria:**
- [ ] Wake Lease fencing and watcher takeover remain covered.
- [ ] AFK readiness and recognized-agent constraints remain covered.

**Verification:**
- [ ] Orchestrator race and failure-injection suites pass.

**Dependencies:** Tasks 3.2–3.5, 3.12–3.13.

### Task 3.16 — Establish the required test manifest and performance requirements

**Scope:** Define fast, race, property, fuzz, platform, and real-adapter CI jobs and remove remaining redundant tests.

**Acceptance criteria:**
- [ ] Versioned test manifest includes every required job per OS/capability.
- [ ] Every deleted test has a incomplete migration ledger rationale.

**Verification:**
- [ ] Warm default suite median is under five seconds on all three OSes with no run above eight seconds.
- [ ] All required extended jobs pass.

**Dependencies:** Tasks 3.12–3.15.

### Continuous review D

Before activation, the exact package invariant, zero legacy imports, critical coverage, and cross-platform performance requirements must all pass.

---

## Workstream E — CLI AXI and embedded skills

### Task 4.1 — Make CLI the typed composition/output adapter

**Scope:** Remove workflow sequencing and implementation-package knowledge from Cobra commands.

**Acceptance criteria:**
- [ ] CLI translates environment into typed context and calls one module operation per workflow.
- [ ] Core modules do not read `MUNSU_*` process environment.
- [ ] Command shapes remain stable except deprecated surfaces.
- [ ] CLI imports only the target modules required for composition.

**Verification:**
- [ ] CLI tests use typed fake operations rather than filesystem/package internals.
- [ ] Direct internal import count matches the composition policy.

**Dependencies:** Workstream D.

### Task 4.2 — Standardize AXI output

**Scope:** Apply one versioned response envelope across commands.

**Acceptance criteria:**
- [ ] Commands support `--output text|json`.
- [ ] JSON mode writes exactly one envelope to stdout.
- [ ] Typed errors map to stable nonzero exit codes and retry dispositions.
- [ ] Deprecated `--json` and compatibility aliases are removed.

**Verification:**
- [ ] Golden/schema tests cover success, queued/fallback outcomes, and each error category.
- [ ] Contract fixtures are generated from the current schema only.

**Dependencies:** Task 4.1.

### Task 4.3 — Complete the embedded skill engine

**Scope:** Introduce a versioned typed manifest and rank allowlists.

**Acceptance criteria:**
- [ ] Manifest and embedded files are validated at build/test time.
- [ ] Unknown rank, skill, or malformed metadata fails closed.
- [ ] `munsu init` installs only `munsu-ops`.
- [ ] Auxiliary skills are available through `munsu skill list/show` according to Actor Rank.

**Verification:**
- [ ] Manifest completeness and RBAC matrix tests pass.
- [ ] Soldier cannot access management-only skills.

**Dependencies:** Task 4.1.

### Continuous review E

CLI thinness, AXI stability, and skill manifest/RBAC remain required completion properties.

---

## Migration and activation sequence

### Task 5.1 — Build the reproducible evidence bundle

**Acceptance criteria:**
- [ ] Bundle contains package/import graph, before/after metrics, all platform timings, test manifest results, migration dry-run/audit, canary outcomes, schema/skill manifests, quarantine acknowledgements, and git identity.
- [ ] Bundle is stored as a CI/release artifact.
- [ ] Live activation record stores only digest, build identity, schema version, quarantine summary, and verdict.

### Task 5.2 — Run full-fleet migration

**Acceptance criteria:**
- [ ] Strict preflight and verified backup pass.
- [ ] Full quiescence is acquired and in-flight work drains.
- [ ] One-shot migration completes or independently quarantines declared obligations.
- [ ] Synthetic and shadow-copy critical canaries pass.

### Task 5.3 — General-authorized activation

**Acceptance criteria:**
- [ ] General acknowledges declared quarantine groups with reasons.
- [ ] `munsu activate --evidence-digest <digest>` verifies the exact bundle/build/schema.
- [ ] Activation record is compare-and-swapped and quiescence is released.
- [ ] Post-activation canary confirms Report→Notification→Receive→Ack→Retirement.

**Dependencies:** All implementation workstreams and activation correctness requirements.

---

## Required test manifest jobs

Each job runs on macOS, Linux, and Windows unless an adapter declares the OS unsupported and the capability test confirms the typed failure.

1. Build: `go build ./...`
2. Static analysis: `go vet ./...`
3. Fast contracts: warmed `go test ./... -count=1` three times
4. Race: `go test -race` on concurrency-bearing packages
5. Property/state-machine suites
6. Fuzz smoke corpus for journal, migration, notification, and path encoding
7. Platform filesystem contracts
8. Real adapter contract jobs where the adapter is supported
9. Migration dry-run/restore smoke
10. Critical lifecycle canary suite
11. No-mistakes delivery pipeline

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Windows durability differs from Unix | Behavioral parity, local-filesystem capability preflight, mandatory cross-platform implementation |
| Forward-only migration cannot roll back binary | Verified backup and restore smoke before mutation; explicit activation after canaries |
| Consolidation creates import cycles | Machine-enforced import policy and single composition root before code moves |
| Test deletion hides regressions | Critical-contract floor, property/race/fuzz/platform suites, evidence review |
| Long integration branch drifts | Strict feature freeze and continuous architecture review |
| Resource cleanup affects newer generation | Lease IDs and fencing tokens on every acquire/release |

## Authorization boundary

The user explicitly authorized implementation, migration, and activation. Commit and push remain unauthorized and require separate explicit instruction.
