# 0020. Internal/Home Round-1 Durable-Store Hardening

* **Status:** Accepted
* **Date:** 2026-08-31
* **Extends:** ADR-0005 (mutation fencing), ADR-0007 / ADR-0008 (task-authority store, owner-clean reset)
* **Triggered by:** internal/home ultra-review round 1 (2026-08-30), shipped as #716–#720

## Context

The internal/home round-1 receive addressed concrete durability, fencing, recovery,
path-safety, and lifecycle findings in the durable store. The least-painful patch
would have kept parallel lease and journal authorities, or moved multi-key read
semantics into a general-purpose home MVCC layer. That route was rejected: the
shipped changes keep one owner for each semantic decision and use the existing
scoped fencing and journal mechanics rather than adding a second protocol.

PR #716 was the mechanical hardening batch rather than a new architectural fork.
It removed the dead post-commit journal rewrite and its committed marker, made the
pre-transaction journal sweep enforce at most one pending record per scope, added
foreign-lock rejection and fail-closed journal validation, moved the fence counter
to a sibling `.fence` file, and made mailbox, task-meta, and writer-identity writes
crash-durable with fsync-before-rename. The batch also carried the surrounding
path and durability fixes that the later focused decisions rely on.

## Evidence

The shipped behavior is recorded by commits f2726d5e (#718), 8f6cde8f (#719),
0eed593e (#717), 34d6532a (#720), and eff66007 (#716). The focused changes are
accepted as one round of the existing home and task-authority design, not as
additional compatibility or migration stages.

## Decision

### 1. Commit authority is the existing fenced lock, not a lease handshake

`Home.Commit` verifies the identity and current fence of the supplied `Lock`. A
lock from another home is rejected with `ErrForeignLock`; a released, stale, or
otherwise invalid fence is rejected rather than being allowed to commit. The
fence token is read from the scoped sibling `.fence` file while the caller's
scoped lock remains the authority.

The canonical `Lease` implementation was production-dead: it had zero production
callers, while the existing commit sites in `internal/taskauthority` and
`internal/fleet` already committed under a plain fenced `Lock`, as ADR-0008 and
CLAUDE.md require. Commit therefore does not require an active lease or a lease
handshake. The existing lock and its fence verification are sufficient to reject
foreign and stale commits without creating a second authority path.

### 2. Multi-key delivery-query atomicity belongs to Task Authority

The three task-authority queries that resolve multiple records in one task scope —
`Canonical.DeliveryCurrency`, `Canonical.DeliveryAuthorization`, and
`Canonical.DeliveryOutcome` — acquire that task's scope lock, call
`Home.RecoverPending`, read and resolve the current evidence, and then release the
lock. `RecoverPending` sweeps an in-doubt scope journal while the lock is held, so
the read observes a whole change-set rather than a torn application. The shared
`Home.requireLiveFencedHolder` guard is used by both `Home.Commit` and
`Home.RecoverPending`.

The single-key `*ByOperation` variants remain lock-free. The three multi-key
queries may briefly block behind an in-flight same-task commit and may rarely
return `ErrLockTimeout` or `ErrFenced`; they fail closed in those cases. They must
not be called while the caller already holds the same task scope because the
underlying flock is non-reentrant.

This places read atomicity at Task Authority, where the task scope and the
meaning of the combined records are known. Home supplies the lock and recovery
primitive; it does not acquire task semantics or publish an MVCC generation
format.

### 3. Mailbox GC removes payloads and retains tombstones

After an inbox message is acknowledged or superseded, the heavy `.json` payload
is removed while the small `.ack` or `.superseded` tombstone is retained
indefinitely. The tombstone is the replay-suppression evidence: readers treat a
missing payload with a valid handled tombstone as handled/absent, and an
acknowledgment can replay from the retained accepted record.

The ordering is crash-safe. The accepted or superseded tombstone is made durable
before its payload is removed and is retained indefinitely. An orphan tombstone is
harmless; deleting a tombstone before its payload would be unsafe because it
could resurrect an already-handled message.

### 4. Wake draining is at-least-once through one atomic removal

`DrainWakes` obtains the wake lock, reads the queue, and clears it with one final
atomic `os.Remove`. It writes no wake journal. Producer exclusion comes from the
wake lock, which prevents an enqueue from occurring between the read and removal.

If removal fails, `DrainWakes` returns `(nil, err)` and leaves the queue file
intact. A later drain therefore re-delivers those records, preserving
at-least-once delivery. Once removal succeeds, the drained records are returned
with a nil error even if lock release reports an error; a post-unlink release
failure must not discard records that have already been drained.

### What was removed

The removed canonical lease was production-dead and had no public contract or
live caller. The pre-public-v1 policy therefore requires no migration: no adapter,
compatibility path, dual-read period, or dormant replacement is retained. The
same hard-cut rule applies to the removed journal marker and the pre-batch lease
grace constant.

| File or symbol | Removed |
|---|---|
| `internal/home/canonical_lease.go` | The whole file: types `Lease` and `leaseRecord`; methods `Lease.FenceToken`, `Lease.ExpiresAt`, `Lease.Renew`, `Lease.Release`; methods `Home.AcquireLease`, `Home.tryAcquireLease`, `Home.leasePath`; functions `readLeaseRecord` and `writeLeaseRecord`; and errors `ErrLeaseHeld` and `ErrLeaseExpired`. |
| `journalRecord.Committed` | The dead post-commit `Committed` journal field and its rewrite were removed in #716. The current journal has one pending record whose application and revision advance are recovered mechanically. |
| `defaultLeaseGrace` | The unused lease-grace constant was removed in the pre-batch simplification pass (#715). |

No migration is defined for these removals. They were internal development
machinery with no public contract, and the deleted canonical lease had no
production consumers. The #720 `DrainWakes` rework removed no additional
wake-mutation identifier. If any of these identifiers is reintroduced, it is a
new design decision and must not be recovered through a compatibility branch.

## Alternatives rejected

### Couple `Home.Commit` to an active lease

Rejected. The finding's lease-coupling proposal was unsatisfiable in the shipped
shape: the canonical lease had zero production callers, its single-counter fence
made lease-coupling unsatisfiable, and roughly nineteen existing commit sites
already used a plain fenced `Lock`. Verifying the existing lock and fence meets
the safety goal without retaining dead lease machinery or adding a second
authority path.

### Move multi-key reads into home MVCC

Rejected. A home-MVCC design would publish an atomic generation pointer per
commit, add an on-disk generation format, change `Home.Read` and `ReadDir`
signatures, and add generation garbage collection. Torn multi-item reads are
reachable only through the three task-authority queries, so ADR-0008 places this
atomicity at Task Authority. A snapshot/retry variant is also unsound here:
`.rev` advances at the end of `Commit`, so a writer crash during item application
can leave `.rev` stable around a torn state. Lock alone is insufficient because a
plain read does not sweep the crashed writer's journal; the shipped answer is
lock plus `RecoverPending`.

### Do no mailbox GC, or delete tombstones with payloads

Rejected. No GC leaves inbox payloads unbounded. Deleting tombstones as well as
payloads removes replay-suppression evidence and can resurrect an acknowledged
message. The shipped retention rule deletes only the heavy payload and retains
the small tombstone.

### Journal the wake drain, or write an empty queue before removing it

Rejected. The journaled batch-1 drain could clear the queue, fail to write its
completion record, drop the in-memory records, and then recover the empty queue;
that changes at-least-once delivery into at-most-once delivery in the crash
window. Writing an empty queue before removal has the same loss window. A single
atomic removal under the wake lock leaves the original queue available for retry
until the clear succeeds.

## Consequences

* `Home.Commit` has one fencing authority: the existing scoped `Lock` and its
  persisted fence token. The dead canonical lease and its separate lifecycle are
  gone.
* Task Authority owns the atomicity of its multi-key delivery reads. Those reads
  are honest about their lock, recovery, blocking, and fail-closed behavior;
  single-key operation lookups retain their lock-free contract.
* Mailbox storage remains bounded for handled payloads without losing durable
  replay suppression. A tombstone without a payload is a valid handled state,
  not corruption.
* Wake draining preserves at-least-once delivery across removal failures without
  adding a drain journal or another wake mutation authority.
* The journal and fence core is stricter and crash-durable: pending records are
  swept before a new transaction, malformed records fail closed, foreign locks
  are rejected, and the fence counter cannot be reset by a crash-truncated
  counter file.
* These are hard-cut internal v1 decisions. Future changes replace the current
  design in place; they do not add legacy readers, adapters, migration paths, or
  fallback semantics.
