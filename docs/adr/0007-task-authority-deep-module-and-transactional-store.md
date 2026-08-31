# 0007. Task Authority Deep Module and Transactional Store

* **Status:** Accepted; implementation complete. §5's journal and read mechanics are refined by [ADR-0020](0020-internal-home-round-1-durable-store-hardening.md), which owns the shipped commit, recovery and read-atomicity contract.
* **Date:** 2026-08-01
* **Extends:** ADR-0002 §5 and §10, ADR-0004 §1–3 and §7–8, ADR-0005 supervision separation
* **Triggered by:** Architecture review and design-it-twice analysis of task lifecycle and dispatch authority

## Context

Task lifecycle and dispatch authority are currently split across `internal/fleet`, `internal/cli`, and `internal/home`. Fleet workflows reach through to filesystem-oriented functions such as aggregate writes, metadata writes, generic state updates, and dispatch checks. This makes the effective interface nearly as broad as the implementation, lets callers bypass lifecycle rules, and forces tests to exercise or fake storage details instead of one authoritative module.

The current locking path already preserves an important invariant: dispatch control is locked before the per-task record while `Start` checks applicable Dispatch Holds and mutates the current aggregate. However, independent atomic file writes and post-commit `.status` appends are not one crash-consistent transaction. A process can stop after committing the Authoritative Task Aggregate but before committing related dispatch or audit records.

ADR-0004 establishes one Authoritative Task Aggregate, Task Lifecycle revision, durable Dispatch Holds, and projection-only `.meta` and `.status`. This ADR fixes the module, interface, implementation seam, and migration shape needed to realize that decision without retaining dual authority.

## Decision

### 1. One deep Task Authority module

Create `internal/taskauthority` as the deep module that owns:

* Authoritative Task Aggregate types and invariants.
* Task Generation and Task Revision semantics.
* Lifecycle transitions and readiness.
* Dispatch Holds, Dispatch Interpretations, and Dispatch Decisions.
* Named semantic operations and typed outcomes.
* Typed audit events produced by authoritative mutations.

`internal/fleet` consumes this module and retains orchestration such as spawn, delivery, cross-home lookup, and handoff sagas. `internal/home` retains home resolution and generic filesystem mechanics, but does not own task lifecycle or dispatch business rules.

The Authority is a concrete implementation, not an interface that callers replace. Its implementation seam is the storage interface below the business rules.

### 2. Named semantic interface

Callers use named operations that express domain intent, such as:

* Queries: `Get`, `List`, and `Readiness`.
* Lifecycle: `Create`, `Start`, `Block`, `Unblock`, `Complete`, `Reopen`, and `ConfirmSpawn`.
* Dispatch control: `CreateHold`, `ReleaseHold`, `InterpretDispatch`, and `ResolveDecision`.

New operations are added only when they hide a lifecycle transition, invariant, or atomic multi-record change. The module does not expose generic setters or mutation escape hatches such as `SetState`, `Patch`, `Save`, `UpdateAggregate`, or a public mutation callback.

This keeps the interface smaller than the implementation in responsibility, even when named methods outnumber a command-sum interface. A command sum or generic query/mutate protocol would reduce method count without reducing the total contract and would lower locality for common callers.

### 3. Generation fence and lifecycle revision

Task Generation identifies one lifecycle incarnation. `Reopen` creates a new Task Generation. Mutating callers provide `ExpectedGeneration` as an incarnation fence so work cannot apply to a superseded generation.

Task Revision is monotonic within a Task Generation and advances on every committed authoritative mutation. Revision provides ordering and audit identity, but is not a mandatory caller-supplied compare-and-swap token. Each semantic operation reads the latest authoritative state inside the transaction and revalidates its lifecycle, ownership, binding, evidence, and Dispatch Hold preconditions before committing.

An operation that genuinely requires an exact prior snapshot may define a revision precondition explicitly. Revision is not exposed as a universal storage concurrency protocol.

### 4. One transactional Store seam

Create `internal/taskauthorityfs` as the on-disk adapter for a transactional `taskauthority.Store` interface. Tests use an in-memory adapter for the same interface and exercise the real Authority implementation.

The Store transaction spans:

* The current and historical Authoritative Task Aggregate records.
* Applicable Dispatch Holds.
* Dispatch Interpretations and Decisions.
* Typed audit events.
* Idempotency receipts.

The adapter exposes records and transactional persistence primitives, not lifecycle operations. Business decisions execute in `taskauthority` while the adapter owns locking, atomic replacement, serialization, permissions, and recovery.

The filesystem implementation preserves the lock order:

```text
.dispatch.lock → per-task lock
```

The initial implementation retains the authority-wide dispatch lock. Finer lock granularity requires measured contention and a separate decision because scoped holds can target projects, task sets, parent scopes, or dependency subgraphs.

### 5. Recoverable filesystem transactions

A lock prevents concurrent mutation but does not make several file replacements crash-atomic. The filesystem adapter therefore uses a recoverable write-ahead transaction. The sequence below is the original design shape, not the live mechanics:

1. Acquire the dispatch lock and applicable per-task lock.
2. Recover any pending transaction covered by those locks.
3. Run the Authority operation against the latest records and build a typed change set.
4. Persist an immutable pending transaction manifest.
5. Apply each write idempotently using atomic file replacement.
6. Persist the commit marker or move the manifest to committed state.
7. Release locks.

The manifest records the Task Operation identity, request digest, expected Task Generation, before digests, after payloads or digests, and typed audit event.

The shipped mechanics differ in two ways, both owned by [ADR-0020](0020-internal-home-round-1-durable-store-hardening.md): a commit sweeps at most one pending scope journal before opening its transaction, then applies items, advances the scope revision and removes the journal record — there is no separate commit marker or committed manifest state; and recovery on the read side is not universal, because only the three multi-key delivery queries take the task scope lock and recover pending work, while the single-key `*ByOperation` variants stay lock-free.

This is a local recoverable transaction, not full event sourcing and not a claim that POSIX can atomically rename several unrelated files at once.

### 6. Task Operation idempotency

Every authoritative mutation has a stable Task Operation ID.

* Repeating the same Operation ID with the same request digest returns the original receipt or a successful no-op.
* Reusing an Operation ID with a different request digest returns a typed non-retryable conflict.
* Durable workflows reuse their existing message, obligation, or attempt identity.
* CLI composition supplies an invocation identity without making operators invent arbitrary IDs.

This contract prevents duplicate lifecycle transitions, holds, interpretations, and decisions after process failure or uncertain retries.

### 7. Durable audit inside; projections outside

Every successful authoritative mutation commits its typed audit event in the same Store transaction as the aggregate and dispatch changes. The event records actor identity and rank, Task Generation, reason, before and after lifecycle state, time, and Task Operation ID as required by ADR-0002.

`.meta`, `.status`, backlog Markdown, briefs, fleet snapshots, and inbox summaries remain projections or audit views. They are reconciled after the authoritative commit and may be retried or rebuilt. A projection failure does not roll back or invalidate an authoritative commit.

There is no separate post-commit `TaskAuditLog` interface. Such an interface would create a distributed transaction without adding leverage. The durable typed audit event belongs to the Store transaction; `.status` remains an append-only projection of audit history.

### 8. Supervision remains outside Task Authority

Watcher health and degraded mode are runtime supervision concerns, not durable Dispatch Holds and not task phases. Fleet orchestration checks a supervision interface before handoff, start, or spawn. Task Authority independently checks durable applicable Dispatch Holds inside its transaction.

A watcher outage therefore cannot be persisted as a task lifecycle phase or silently converted into a Dispatch Hold.

### 9. CLI composition root

`internal/cli` is the composition root. After resolving the exact home it constructs the filesystem Store adapter and the concrete Authority once for the command context. Fleet workflows receive the Authority through an application object or request that genuinely needs it.

The implementation does not use package globals, a dependency-injection framework, or a generic service locator. Composition must not become pass-through plumbing across helpers that do not use Task Authority.

### 10. Cross-home handoff remains a fleet saga

One Authority is bound to one home. It does not know the Captain registry or search other homes.

Fleet coordinates handoff as a durable saga between source and destination Authorities: persist transfer intent, commit receipt at the destination, retire source ownership, and close the saga. Failure before destination receipt preserves source ownership. Retry uses stable Task Operation identities and durable receipts; no distributed filesystem transaction is implied.

### 11. Staged clean break

Implementation proceeds in vertical slices without retaining dual mutation authority:

1. Add `taskauthority`, its Store interface, and real Authority tests over an in-memory adapter.
2. Add `taskauthorityfs` over the current schema and paths, including revision, audit, transaction recovery, and adapter contract tests.
3. Migrate task creation, canonical task queries, `Start`, `Unblock`, and `Reopen`.
4. Migrate endpoint/worktree binding and `ConfirmSpawn`.
5. Migrate Dispatch Hold, Dispatch Interpretation, and Dispatch Decision operations.
6. Migrate handoff and delivery invariant clusters.
7. Move snapshots and projection consumers to canonical Authority queries.
8. Delete task lifecycle and dispatch authority from `internal/home`.

When the last caller of an old authoritative mutation moves, that mutation is removed or made inaccessible in the same slice. Projection fallback may exist during migration; authoritative mutation fallback may not.

## Consequences

* Lifecycle and dispatch rules gain locality in one deep module.
* Named semantic operations provide leverage over file-oriented read/check/write sequences.
* The Store interface is a real seam with filesystem and in-memory adapters.
* Tests exercise the real Authority implementation instead of faking business authority.
* Same-generation mutations serialize against fresh state without exposing storage revision mechanics to every caller.
* Start and concurrent Dispatch Hold creation cannot interleave across a check-commit gap.
* Aggregate, dispatch, audit, and idempotency records recover consistently after process failure.
* Projections can fail and reconcile without becoming competing current-state authority.
* CLI and fleet call sites require staged dependency injection and migration work.
* The filesystem adapter and transaction recovery add implementation complexity that is concentrated behind one interface.

## Rejected Alternatives

* Keep Task Authority inside `internal/fleet`: leaves authoritative locality inside an already broad orchestration module and forces adapters to depend on that package.
* Let `internal/home` implement lifecycle methods: preserves business authority in the filesystem module and violates ADR-0002 and ADR-0004 ownership.
* Move shared records into a neutral domain package only to avoid an import cycle: creates a dumping ground without a second domain owner.
* Use `Load` plus a command sum: reduces method count but moves complexity into command variants, validation, and result unions.
* Use generic query/mutate contracts: creates an in-process protocol with weaker locality and type guidance.
* Expose generic state setters, patches, or mutation callbacks: lets callers bypass lifecycle invariants and recreates duplicate authority.
* Require Task Revision compare-and-swap on every caller mutation: leaks storage concurrency mechanics and creates avoidable reload loops for semantic operations that can revalidate fresh state transactionally.
* Split lifecycle and dispatch into separate authorities with a coordinator: the coordinator becomes the real authority for atomic Start/Hold invariants while the other modules become shallow.
* Keep a post-commit Task Audit Log seam: permits authoritative success without durable audit and requires distributed rollback or repair.
* Treat watcher degradation as a Dispatch Hold or lifecycle phase: mixes runtime supervision with durable business control.
* Use best-effort rollback for multi-file writes: cannot recover from process death.
* Adopt full event sourcing or one giant home snapshot: adds more conceptual or corruption complexity than the required local transaction.
* Perform a big-bang migration or permanent dual write: increases blast radius or preserves competing mutation authority.
