# 0010. The Delivery Head Invariant Has Two Owners, One Per Layer

* **Status:** Accepted
* **Date:** 2026-08-14
* **Extends:** ADR-0008 (one owner and one canonical implementation path per lifecycle), ADR-0009 (a classification has one owner)
* **Triggered by:** BEO-64 (`verifySnapshotIdentity` / `verifyAncestry` unreachable on `main`) → BEO-70 (provider fence) → BEO-72

## Context

*The delivery head invariant* is the rule that a delivery mutation may only land
on the exact head that was authorized: the same commit, on the same PR, into the
same base branch. It is the invariant that stops an agent's work from being
merged after the branch underneath it moved.

Four pieces of code in `internal/fleet/delivery_amend.go` read as if they owned
part of that invariant — `verifySnapshotIdentity` (branch replacement),
`verifyAncestry` (force-push / rewritten history), `appendAmendHistory` and
`incrementRevision` (the amendment audit trail behind them). None of them had a
production call site. `deadcode ./cmd/munsu` reported all four; two carried the
`OPEN-BUG(BEO-64)` marker in `.github/deadcode.allow` precisely because a guard
that cannot be reached cannot refuse anything.

BEO-64 traced the real write path instead of reasoning from the function names,
and ran the refusal conditions as mutations. The result reshaped the problem:
the invariant *was* enforced, twice, in neither of those functions — and the one
piece the removed guards had genuinely owned (base ref) had been inherited by
nobody. BEO-70 fenced the base ref where the live check already was. This ADR
records the ownership so the next reader does not re-derive it from a third
place.

This is the failure mode ADR-0009 was written about, one layer over: BEO-23
fixed `EnsureNotPrimary`, a guard nobody called, and the sentence "the guard is
fixed, therefore the checkout is protected" was never true. Here the equivalent
sentence would have been "`verifyAncestry` exists, therefore force-push is
refused". It was not true either.

## Decision

### 1. There are two owners, and they own different truths

Not one. ADR-0009's shape — a single owner for a single question — does not
apply here, because "the head is still the authorized head" is two questions
answered against two different sources of truth, at two different moments.

| Owner | Site | Truth it guards | Fails on |
|---|---|---|---|
| Canonical delivery fence | `internal/taskauthority/canonical_delivery.go` (issuance and currency) | **Local truth** — the bound worktree head recorded in the Task Aggregate | typed authorization/currency refusal |
| Provider observation and mutation boundary | `internal/fleet/delivery_deliver.go` | **Provider truth** — exact head, base ref, and mergeability evidence for the authorized delivery | fail-closed refusal when evidence is missing or the adapter cannot enforce all constraints |

The canonical fence refuses to *issue* a delivery authorization whose identity
head differs from the bound worktree head, and the currency read
(`authorizationCurrencyReasons`, `checkHead=true`) refuses to keep treating an
authorization as current once the worktree head has advanced underneath it.

The provider boundary answers the question the canonical fence structurally
cannot: local state says nothing about what happened on the provider between
capture and delivery. Provider observations must carry exact head and base-ref
evidence, plus mergeability evidence for an OPEN request. The irreversible
provider operation must enforce mergeability, exact head, and exact base
constraints together; an adapter that cannot guarantee that contract refuses
before mutation. The current GitHub and GitLab adapters intentionally take that
fail-closed path for OPEN mutation. MERGED and CLOSED observations are
reconciled without mutation, but only when their consumed head and base evidence
exactly match the journal identity. A MERGED observation must also carry a
non-zero, full hexadecimal Git object ID for the merge commit (40 or 64
characters); missing, null, malformed, or all-zero evidence fails closed. The
same validation applies to pinned journal outcomes and outcomes read during
commit-conflict replay, so recovery cannot bypass the provider fence.

Neither owner is redundant with the other, and neither is defence in depth for
the other. Deleting either one removes a refusal nothing else can make. Any
third check over the same invariant needs an argument at this level — which
source of truth, at which moment — or it is a second weaker owner of an existing
question, which §2 of ADR-0009 rejects.

### 2. `.meta` grants nothing

`.meta` delivery fields are a display projection and never authorize a delivery.
This follows ADR-0008 §2 ("Durable `backlog.md`, `tasks-axi` runtime
integration, `.meta`, `.status`, and other Task projections are removed.
Backlog is only a query concept over Task state") and §3 (Task Authority issues
the immutable Delivery Authorization bound to the exact Task Generation and
Operation ID; Fleet alone executes delivery against it).

Concretely, after BEO-72 the only production reader of `delivery_state` is
`home.ListMeta` (`internal/home/taskmeta.go:382`), which picks the string shown
in a listing column. Nothing downstream of it grants, extends, or revalidates a
delivery authorization.

### 3. The four amendment orphans are removed

`verifySnapshotIdentity`, `verifyAncestry`, `appendAmendHistory` and
`incrementRevision` are deleted together with their tests
(`internal/fleet/delivery_amend_test.go` in full), the `buildAmendGitRepo`
helper, the `AmendRecord` type, and the meta-key constants of the retired
amendment lifecycle (`MetaIdentityRevision`, `MetaAmendExpectedHead`,
`MetaAmendStartedAt`, `MetaAmendHistory`, `MetaLegacyMergeAuth`). Their four
lines leave `.github/deadcode.allow` in the same commit, which is the point of
that file being a ratchet.

The tests go with the functions rather than being kept as "coverage". BEO-64
showed they only ever exercised the preconditions of `verifyAncestry` (missing
repo, empty SHA, identical SHAs) — never the refusal branch that gave the
function its reason to exist. Tests that outlive their subject, or that pin a
guard's easy half, are the mechanism by which a dead guard looks alive.

`ProviderSnapshot` stays: it is live through type inference at
`internal/cli/delivery_cmd.go:76` via `fleet.FetchProviderSnapshot`.

`DeliveryStateAmending`, `DeliveryStateRemoteUnknown` and `DeliveryStateDelivered`
also stay. They have no references, but they are exported members of a closed
set; removing them is a deliberate decision about the shape of `DeliveryState`,
not dead-code cleanup, and it is not this ADR's decision to make.

## Consequences

* The head invariant has a written map. A future guard for it either extends one
  of the two owners or states which third truth it guards.
* Deleting `verifyAncestry` costs no refusal that was ever reachable; its
  relevant provider-side head/base constraint now belongs to the shared
  observation and mutation boundary rather than an unreachable function.
* Provider adapters must fail closed when they cannot enforce mergeability plus
  exact head and base constraints at the irreversible boundary. Terminal
  reconciliation remains mutation-free and identity-fenced, with valid merge
  object evidence required for completed outcomes on fresh, resumed, and
  conflict-replay paths.
* `.github/deadcode.allow` contains only current, reviewed exceptions; it is not
  an imported BEO-63 baseline.
* `internal/home`'s `ValidMetaFields` (`taskmeta.go:309-320`) still lists the
  retired amendment keys. Nothing writes them and nothing reads them; the list
  is `home`-owned validation vocabulary, and pruning it is a separate change
  with its own compatibility question about existing on-disk `.meta` files.
  Named here so it is not mistaken for an owner of anything.
