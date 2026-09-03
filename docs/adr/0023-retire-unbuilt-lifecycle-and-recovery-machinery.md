# 0023. Retire Unbuilt ADR-0004/0005 Speculative Machinery

* **Status:** Accepted
* **Date:** 2026-09-03
* **Retires:** ADR-0004 §9 (Bounded Context Manifest); ADR-0005 §4 (durable recovery series, deterministic-failure circuit, and the redacted launch-diagnostic bundle)
* **Records subsumed:** ADR-0004 §7 (the distinct dependency-interpretation record) — covered by `DispatchHold` plus head-bound `DeliveryAuthorization` (ADR-0008)
* **Extends:** ADR-0004 (authoritative task lifecycle), ADR-0005 (runtime bindings, supervision, recovery), ADR-0008 (owner-clean single authority)
* **Triggered by:** [owner-clean residual roadmap](../plans/2026-09-03-owner-clean-residual-roadmap.md) decision gates G1, G2, and T3

## Context

Three clauses from the 2026-07-30 incident-remediation ADRs were specified but
never built, and nothing in ADR-0009–0022 either built or retired them. Under
the owner-clean rule that the tree carries one live contract and no speculative
machinery, an unbuilt clause is a standing claim the code does not honour: it
reads as debt, invites a partial re-implementation, and keeps a citation waiver
alive for a struct that has no declaration. The owner resolved all three gates
on 2026-09-03 (roadmap gates G1, G2, T3).

## Decision

### 1. ADR-0004 §9 Bounded Context Manifest — retired (won't-build)

The revision-bound bounded-context manifest — per-task budgeted paths, symbols,
line ranges, tests, commands, invariants, and digests — is not built and will
not be. Soldiers receive their launch context through the charter and the
SHA-verified launch-artifact manifest (`internal/fleet/soldier_charter.go`),
not a §9 bounded-context budget; that is the accepted context contract. No
aggregate field, store, or CLI carries a context manifest.

### 2. ADR-0005 §4 Durable recovery series and diagnostics — retired (won't-build)

The failure-signature-keyed durable recovery series, the deterministic-failure
circuit breaker, and the redacted launch-diagnostic bundle are not built and
will not be. The accepted recovery contract is the existing Captain-side
machinery in `internal/fleet/captain_recover.go`: relaunch on a verified-dead
endpoint, bounded nudge-retry, and the TTL relaunch-guard that prevents
duplicate panes after a deterministic startup failure. This machinery is
Captain-scoped by design; §3's soldier-endpoint extension is not a gap but the
accepted boundary — recovery acts on Captain endpoints, and soldier liveness
reaches recovery through the Captain that owns the soldier.

### 3. ADR-0004 §7 DispatchInterpretation — subsumed

The distinct dependency-interpretation record (directive revision, dependency
snapshot digest, divergence class, evidence) is not built as its own artifact.
Its two obligations are already met by shipped mechanisms: a directive that
diverges from the dependency-ready set is gated by a durable `DispatchHold`
carrying its `Reason`, `Scope`, and `Actions`
(`internal/taskauthority/dispatch.go`), and the authority to deliver a divergent
head is a head-bound `DeliveryAuthorization` pinned by its `Revision`,
`Generation`, and `BindingDigest` (`internal/taskauthority/canonical_delivery.go`).
No separate interpretation struct is added.

## Consequences

* ADR-0004 §7 and §9, and ADR-0005 §4, are closed. Their section bodies remain
  as the historical record; their headers carry the retirement; no tree work
  tracks them.
* ADR-0005's remaining residual narrows to §5 (General→Captain watcher-lease
  observation and recovery-request relay) and §6 (Git-fencing tier decision,
  roadmap gate G3). §3 and §4 are closed by this ADR.
* No code changes. The names ContextManifest, DispatchInterpretation, and
  LaunchDiagnostic never had a Go declaration; their citation-waiver rows in
  `.github/citations.allow` stand for the ADR-0004/0005/0013 bodies that still
  reference them.
* If bounded-context budgeting or a durable recovery engine is wanted later, it
  enters as new greenfield work under a new ADR — not as a debt these clauses
  left open.
