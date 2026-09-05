# 0008. Owner-Clean Architecture and Pre-Public v1 Reset

* **Status:** Accepted; substantially implemented — topology, `task` sole noun, render seam, contract repo, and activation gate landed (#402–#418). The §2 `.meta`/`.status` retention decision was resolved 2026-09-04: these files are retained as `home`-owned durable projections read by home/backend/fleet/orchestrator/CLI mechanics, with Task Authority the sole Task-lifecycle authority (§2). §4's unbuilt "requests recovery through the canonical control/Uplink interface" clause is superseded by the running multiple-entry-point recovery model (ADR-0005 §5, 2026-09-04); its "renewable generation and fencing token" lease-mechanism claim remains a pre-existing unbuilt-design mismatch (`WatcherLease` implements neither) flagged for a separate owner decision, not a tracked roadmap item. The remaining tracked cross-ADR gate (ADR-0005 §6 Git fencing, G3) landed as #752 (fence) and #753 (fence-doc followup).
* **Date:** 2026-08-03
* **Supersedes:** ADR-0001 through ADR-0007 where their topology, ownership, projections, migration, fallback, compatibility, schema, command, or test decisions conflict with this ADR
* **Triggered by:** Whole-codebase architecture review and structured grilling

## Context

The codebase has accumulated shallow modules, duplicate durable representations, ambient inputs, direct process and filesystem access, migration machinery for unpublished development state, and tests coupled to historical implementations. Task lifecycle behavior is split across Task Authority, fleet workflows, filesystem packages, CLI commands, Markdown backlog state, and projections. Supervision similarly spans Wake records, terminal receipts, relay paths, and Uplink state. Configuration resolution is mixed with Project and Captain registries. Large exported surfaces make implementation details, rather than module interfaces, the effective test surface.

The least-painful patch would retain the current package graph and remove only the most visible migration and fallback branches. That route was rejected because it preserves shallow interfaces, weak locality, duplicate authority, and long-term recovery complexity. Development cost is not a primary decision driver. Quality, simplicity, robustness, scalability, and long-term maintainability take precedence.

The product has not shipped publicly. Existing development schemas and state therefore carry no compatibility promise. Keeping internal-history versions, migrations, or alternate paths would turn unpublished implementation history into a permanent interface.

## Decision drivers

* One owner and one canonical implementation path for every lifecycle.
* Deep modules with small interfaces, high leverage, and strong locality.
* Explicit dependency direction and scoped identity.
* Fail-fast, fail-closed behavior without alternate semantics.
* Durable idempotency, optimistic concurrency, fencing, and crash recovery.
* Scalability without global runtime locks or a global transaction coordinator.
* Interfaces as the test surface; real adapters at real seams.
* A pre-public schema and protocol baseline of `v1`.

## Decision

### 1. Topology and dependency direction

The target production topology is:

1. `internal/taskauthority` — Task truth and invariant operations.
2. `internal/fleet` — workforce execution.
3. `internal/orchestrator` — supervision policy and Uplink lifecycle.
4. `internal/domain` — only concepts proven to be shared by multiple owning modules.
5. `internal/config` — configuration resolution authority.
6. `internal/home` — domain-neutral durable mechanics.
7. `internal/backend` — terminal, worktree, repository, and provider capabilities.
8. `internal/harness` — coding-agent runtime capabilities.
9. `internal/bootstrap` — diagnostics and typed repair plans.
10. `internal/cli` — the sole composition root and AXI adapter.

`cmd/munsu` remains a minimal executable bootstrap. `internal/testutil` may contain test infrastructure only and is not a product module.

The following packages are deleted:

* `internal/configmigration`
* `internal/taskauthorityfs`
* `internal/taskauthority/storecontract`

Topology is protected by ownership and dependency-direction rules, not a literal package-count gate. Calling modules define only the narrow capability interfaces they require. There is no shared `ports`, `contracts`, or `interfaces` package. `fleet` and `orchestrator` do not import each other; `cli` composes them.

### 2. Task Authority

`taskauthority` is one deep module that owns:

* Task Aggregate, Generation, Revision, and lifecycle phases;
* Task transitions, readiness, Dispatch Holds, and dispatch eligibility;
* endpoint and worktree binding invariants;
* Task Operations, durable receipts, and Task audit facts;
* delivery authorization and delivery lifecycle consequences;
* Task document semantics and the concrete filesystem implementation;
* Task transaction and recovery semantics.

The implementation of `taskauthorityfs` is absorbed into `taskauthority`, not moved into `home`. There is one concrete filesystem implementation. No synthetic Store interface, in-memory fake, store-contract package, or persistence adapter is retained solely for testing. A storage seam is introduced only when a second real adapter exists.

All authoritative Task lifecycle reads — phase, dispatch eligibility, binding, delivery authorization, transfer — go through Task Authority; `taskauthority` reads no `.meta`/`.status` file. Durable `backlog.md` and `tasks-axi` runtime integration are removed, and backlog is only a query concept over Task state. `.meta`/`.status` are not the authoritative Task record and are retained as `home`-owned durable primitives (`internal/home/taskmeta.go`) serving home/backend/fleet/orchestrator/CLI mechanics — mailbox routing, prune, absorb classification, snapshot and task-summary display, supervision and recovery — never Task lifecycle authority. Their last-status line is a diagnostic display value, never state truth.

### 3. Fleet

`fleet` owns workforce execution:

* Project and Captain registries and the Captain–Project Binding;
* Captain and Soldier lifecycle;
* launch, retirement, resource coordination, and delivery execution;
* Task Transfer between authoritative homes;
* Config Assignment to Captains;
* owner-specific journals for cross-module workforce operations.

`fleet` remains one deep module. Captain, Soldier, transfer, configuration-assignment, and delivery workflows are private implementation clusters, not mechanically separated packages or forwarding facades. Its external interface is a small family of typed operations and queries, not its current exported function set.

Task Transfer is a Fleet-owned journaled operation over two local Task Authority interfaces. It uses durable reservation, scoped generations, idempotency, and fencing. It never copies raw documents, holds a distributed lock, or resolves divergence with ownership heuristics.

Task Authority issues an immutable Delivery Authorization bound to the exact Task Generation and Operation ID. Fleet alone executes delivery through typed backend/provider capabilities and commits the resulting lifecycle consequence through Task Authority. CLI helpers and shell commands are not parallel delivery implementations.

### 4. Orchestrator and Uplink

`orchestrator` owns supervision policy:

* one logical fenced watcher per authoritative home;
* observation interpretation;
* retry and recovery scheduling;
* AFK policy;
* one Uplink journal and lifecycle.

The Uplink lifecycle is:

`Intent → Durable → Delivered → Acked → Retired`

Terminal Receipt, Wake, Captain relay, turn-end, and drain lifecycles are not public or durable alternatives. A terminal signal may wake an implementation loop, but terminal delivery is not semantic acknowledgment. Terminal receipts, receipt reconciliation, relay compatibility, and alternate drain paths are deleted.

Adapters return typed observations. They never mutate lifecycle directly. Orchestrator interprets observations and requests typed operations from the owning module. `unknown` remains distinct from success, failure, absence, or death. Only evidence required to explain a decision or continue recovery is durable.

Each authoritative home has one logical watcher owner with a renewable generation and fencing token. Ownership here is role/authority ownership, not filesystem isolation. In its Captain-health observation path, General verifies Captain-home identity (canonical path via `filepath.EvalSymlinks` and the `.munsu-captain-home` provenance marker) and reads its own projection task-meta, but reads no Captain `WatcherLease` or Task-lifecycle state and mutates no Captain-home state; on that evidence it observes typed Captain health. Separately, the fleet-owned General-scoped `RecoverTransaction` is a recovery workflow that intentionally reads and mutates the target Captain home through its typed recovery steps, under Fleet authority regardless of which home's CLI invokes it. The original "requests recovery through the canonical control/Uplink interface" clause was never built and is superseded by ADR-0005 §5's running multiple-entry-point recovery model (General-scoped `RecoverTransaction`, the `fleet.Recover` sweep, and converge's strict-dead-only relaunch path; none is a sole canonical boundary), 2026-09-04. Process topology may host multiple watcher loops, but it does not alter ownership topology.

### 5. Home

`home` is a deep, domain-neutral durable-storage module. It owns:

* canonical Home Identity and verified roots;
* containment and no-follow path safety;
* owner-private permissions;
* scoped fenced locking and leases;
* atomic durable change-set commit;
* write-ahead journal mechanics and mechanical crash recovery.

`home` is not a bag of exported filesystem helpers. Owning modules provide logical keys, encoded bytes, expected revisions/generations, transaction identities, and digests. They retain document schemas, semantic validation, lifecycle decisions, and recovery meaning. Durable state may not bypass `home` through direct writes, private lock protocols, or ad hoc fsync sequences.

### 6. Config and bootstrap

`config` owns typed settings, defaults, validation, precedence, Project Overlays, Captain launch profiles, deterministic digests, and immutable resolved Config Snapshots. It does not own Project or Captain lifecycle.

`cli` is the boundary that turns flag and ambient inputs into typed config operations; there is no process-environment configuration override tier (the environment-override layer was retired — ADR-0003 §10). Core modules never read process environment or infer authority from the working directory. A Config Snapshot is resolved once at an operation boundary and remains immutable for that operation.

General is the single resolution authority. Fleet distributes a resolved snapshot through a journaled Config Assignment over the canonical Uplink path. A Captain verifies and durably accepts the assigned identity, generation, and digest; it never re-resolves, reads General's files, falls back to local configuration, or uses terminal nudges as authority.

`bootstrap` returns typed readiness diagnostics and repair plans. It does not mutate automatically, resolve configuration, or activate alternate recovery behavior. CLI explicitly renders or executes an authorized repair operation.

### 7. Backend, harness, and process execution

`backend` and `harness` remain separate deep modules:

* `backend` owns terminal endpoint, worktree, repository, and provider capabilities;
* `harness` owns coding-agent runtime detection evidence, launch behavior, model/effort mapping, and turn-end integration.

Fleet coordinates them through semantic interfaces. It does not directly execute processes, inspect `PATH`, or interpret raw command output. Shared process mechanics remain private implementation detail; there is no public generic Command Runner.

Adapter selection is explicit in the Config Snapshot. Missing or unhealthy requested capabilities fail closed. There is no resilient backend fallback, initial-resolution fallback, direct-exec escape hatch, or implicit adapter switch after binding. Detection verifies the requested adapter; it does not select an alternate one.

### 8. Domain and identity

`domain` retains only concepts with at least two genuine owning modules, such as shared Actor identity or error categories. Task-specific semantics belong entirely to Task Authority; Soldier and Captain semantics belong to Fleet; Uplink and Watcher semantics belong to Orchestrator. `domain` must pass the deletion test: removing it would otherwise duplicate shared complexity across multiple callers.

Every cross-module mutation uses typed scoped identity. Core modules never scan homes or use ambient context to infer ownership. Mutations carry the relevant Home Identity, Project Identity, Task or Captain identity, generation/revision, Operation ID, digest, and resource fencing data. CLI may resolve human shorthand exactly once when explicit scope makes the result unique; modules never accept ambiguous bare identity.

### 9. Operations, concurrency, and recovery

Each owning module exposes a small family of typed operations and typed queries. Interfaces are organized by domain intent, not CRUD, CLI command, file artifact, or exported helper. There is no global command bus or `map[string]any` operation payload.

Cross-module work uses owner-specific journaled operations. The owner writes durable intent before side effects; participants expose typed idempotent operations; recovery continues the same Operation ID and journal. Compensation is limited to resources that can truthfully be released. There is no global transaction coordinator and no cross-module filesystem mutation.

Mutations use optimistic expected revision/generation checks plus the smallest scoped fenced lock. Independent aggregates do not share a global runtime lock. Cross-module operations do not hold multiple module locks simultaneously. Stale processes may continue running but cannot commit after losing their fencing generation. Conflicts fail closed; there is no last-writer-wins or heuristic reconciliation.

### 10. CLI and AXI

`task` is the only CLI noun for Task operations. The `backlog` command and compatibility aliases are deleted.

Core modules return typed outcomes. `cli` owns one response-model and rendering seam:

* TOON is the default stdout representation;
* JSON is an explicit alternate representation of the same response model;
* structured successes and errors use the same seam;
* progress and debug diagnostics go to stderr;
* exit codes consistently distinguish success/no-op, operational error, and usage error;
* unknown flags, arguments, enum values, and formats fail loudly.

Response contracts are minimal and versioned by response kind where a durable or external contract exists. There is no universal envelope that forces irrelevant metadata into every response, and no command-specific printing path.

### 11. Pre-public v1 baseline and activation

Application SemVer remains `0.x`. Every supported durable schema and external contract starts at `v1`. Internal-history `v2` identities are replaced in place by the first supported `v1` definition.

Until first public shipment, a breaking design change replaces `v1` content in place and development state is discarded. There are no version branches, version registries, compatibility decoders, migration commands, read-time upgrades, shims, archived legacy state, or fallback implementations. Unknown or malformed current state fails closed without proposing migration.

Only fresh initialization into an empty home, or operation on a home already matching the current `v1`, is supported. Old development homes are discarded externally and initialized again. The product does not provide migrate, upgrade, or destructive reset commands.

### 12. Testing

The interface is the test surface. The existing test suite is replaced rather than mechanically ported.

* Module contract tests exercise current typed operations, queries, invariants, idempotency, conflict, and failure behavior.
* Concrete durable implementations are tested on isolated real filesystems, including locking, permissions, crash points, replay, and fencing.
* Shared adapter contract suites run only where at least two real adapters occupy the same seam.
* Composition-root integration tests prove canonical wiring, one mutation path, state reread, AXI rendering, and fail-closed behavior.
* Property, fuzz, and crash tests protect current transitions, current `v1` encoding, containment, deterministic resolution, and interruption recovery without weakening module shape.

Tests and fixtures authored for migration, compatibility, fallback, projections, deleted identifiers, historical versions, forwarding facades, or old package topology are deleted. Negative tests derive invalid data from current constants and current interfaces. Blacklists, tombstone registries, source-substring gates, and legacy-literal assertions are forbidden.

### 13. Cutover

Implementation is delivered incrementally as dependency-ordered gated increments, each on its own branch through the delivery gate (as issues #402–#418 and their successors were). The invariant is architectural, not single-branch: every landed increment leaves the tree owner-clean, and dependency-ordered work never creates *supported architecture phases* — no two runtimes coexist. There are no feature flags, dual registrations, temporary compatibility adapters, or old/new runtime switches at any increment boundary.

The final activation requires:

* the target topology and dependency direction;
* one canonical Task, Uplink, configuration, delivery, transfer, and output path;
* complete deletion of legacy packages, commands, schemas, tests, fixtures, comments, and documents;
* build, vet, and full tests passing;
* current contract, crash/replay/fencing, and architecture-policy tests passing;
* a full-suite performance budget achieved without weakening critical tests — for owner-clean v1 no numeric per-suite budget applies (the ADR-0002 5-8s per-suite target is superseded); the registry lock-overlap guard (independent Project and Captain aggregate operations complete within the 2s overlap budget) remains binding;
* diff review confirming deleted identifiers and literals did not survive.

## Alternatives rejected

### Keep `taskauthorityfs` as a separate adapter

Rejected. With one concrete storage implementation, the seam is hypothetical and splits Task transaction/recovery knowledge across modules. Absorbing it into Task Authority improves locality without introducing a fake Store interface.

### Move Task persistence into `home`

Rejected. It makes `home` understand Task semantics or forces dependency inversion around a single adapter. Home provides durable mechanics; Task Authority owns Task documents and recovery meaning.

### Absorb Task Authority into Fleet

Rejected. Fleet is already large, and Task truth is an independent domain owner. Absorption would mix authorization with workforce execution and reduce depth.

### Retain one-way Markdown or file projections

Rejected. They create another durable representation, synchronization obligations, and a future alternate read path without sufficient leverage.

### Preserve migration or fallback for convenience

Rejected. Both create a second semantic path and permanently encode unpublished development history. Explicit configuration and fresh initialization are simpler and more robust.

### Split Fleet or Orchestrator mechanically by workflow

Rejected. File and package count do not create depth. Private operation clusters preserve locality without multiplying shallow interfaces.

### Use a global transaction coordinator, event bus, command bus, or generic process runner

Rejected. These generic modules centralize little domain behavior, expand interfaces, obscure ownership, and create bottlenecks or pass-through abstractions.

## Consequences

### Positive

* One owner and one implementation path for each lifecycle.
* Smaller module interfaces with greater leverage and locality.
* Explicit identity, concurrency, and failure semantics.
* No permanent compatibility or migration burden before public shipment.
* Scalable per-home supervision and per-aggregate concurrency.
* Tests describe the canonical architecture rather than implementation history.

### Negative

* The reset is a large breaking cutover and old development homes are unusable.
* Most existing tests and several existing documents cannot be reused mechanically.
* Current callers, skills, and CLI contracts must be rewired together.
* Concrete filesystem tests may be slower than in-memory fakes and require disciplined test isolation.

These costs are accepted because development convenience does not outweigh quality, simplicity, robustness, scalability, or long-term maintainability.