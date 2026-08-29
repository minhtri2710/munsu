# 0019. The Hand-Rolled Durable Store Is a Single-Binary Constraint, Not an Accident

* **Status:** Accepted — see [Removal condition](#removal-condition)
* **Date:** 2026-08-30
* **Extends:** ADR-0007 (which builds the transactional store as a deep module but never records why the store engine is hand-rolled rather than embedded)

## Context

`internal/home` implements durable state as a hand-rolled, filesystem transactional
store: a write-ahead journal of change-sets, `fsync` of file and parent directory,
per-scope optimistic-concurrency revisions, flock-plus-fence-token locking, and
mechanical crash recovery of interrupted commits. `internal/home/canonical_journal.go`
(`Commit`, `recover`) is the core of that mechanism, and `internal/taskauthority` is
its typed consumer.

A whole-project premise audit asked the obvious question a reviewer asks first: why is
this hand-rolled rather than delegated to a mature embedded engine (SQLite, bbolt)? The
audit found the answer nowhere in the tree. ADR-0007 treats the store as settled but
records no constraint that forecloses an embedded engine. An unstated constraint reads
as an accident, and an accident invites a well-meaning "just use SQLite" rewrite that
would silently break a shipping property. This is the shape ADR-0016 and ADR-0017 named:
a constraint nothing states is not owned. This ADR states it.

## Decision

Two constraints, both already embodied by the codebase, make the hand-rolled store the
owner-clean choice and not a stopgap:

1. **Single static binary, zero cgo.** `go.mod` declares five pure-Go direct
   dependencies and no cgo. munsu ships as one statically linked binary that runs on a
   host with no runtime, library, or database to install. A cgo-linked SQLite driver
   would break that build posture outright.

2. **Durable state is a directly inspectable filesystem surface.** Operators and agents
   `cat` and `grep` the on-disk documents under the munsu home, and cross-home handoff
   is defined as copying those files. An opaque embedded database — including a pure-Go
   one such as bbolt, which clears constraint 1 — would make that state unreadable
   without munsu itself, dissolving a product property the rest of the system relies on.

The transactional guarantees munsu needs are narrow: atomic per-scope change-sets with
optimistic concurrency and crash recovery. That surface is small enough that owning it
in `internal/home` costs less than importing an engine and then fighting it to preserve
constraint 2. The hazards such hand-rolled durability invites are real and are addressed
where they live (see `internal/home/canonical_journal.go` recovery), not by outsourcing
the layer.

## Removal condition

Reopen when either constraint stops holding: if the transactional needs outgrow atomic
per-scope change-sets (multi-scope transactions, secondary indexes, range queries), or if
directly inspectable on-disk state is deliberately abandoned. Absent that, "replace the
hand-rolled store with an embedded engine" is a rejected alternative, not an open question.
