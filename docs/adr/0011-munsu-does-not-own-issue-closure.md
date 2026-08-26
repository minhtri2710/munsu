# 0011. munsu Does Not Own Issue Closure on Delivery

* **Status:** Accepted
* **Date:** 2026-08-14
* **Extends:** ADR-0008 (one owner and one canonical implementation path per lifecycle)
* **Retires:** ADR-0004 §5 "Linked Issue policy"
* **Triggered by:** BEO-67 deadcode triage → BEO-83 → BEO-85 verdict

## Context

ADR-0004 §5 specified that munsu carries typed `IssueLink[]` records bound to Task
Generation, provides a canonical PR-body fragment at delivery preparation, fails
closed at `delivered` when required closing references are absent or incorrectly
scoped, and reconciles Issue closure after merge. The implemented fraction was the
`IssueLink` model in `internal/domain/domain.go` plus
`VerifyDeliveryIssueLinks` / `PrepareDeliveryIssueLinks` / `ReconcileIssueLinks` in
`internal/fleet/delivery_issuelinks.go`.

The only two call sites into that guard lived in `internal/fleet/delivery_terminal.go`
and were removed with `1d0c4b5`, when delivery was rebuilt on the canonical Delivery
Authorization. The Authority-tier owner (`Authority.ReconcileIssueLinks`,
`validateIssueLinkDefinition`) was removed in the same change. Since then:

* nothing writes `issue_link_N_*` keys into `.meta`, and nothing reads them;
* `TaskDefinition` has no issue link field;
* the Delivery Authorization precondition set is a closed set of three values
  (`pr-mergeable`, `pr-head-current`, `worktree-clean`) with no slot for issue links;
* the "missing closing reference" refusal branch was not truly covered by any test —
  the two tests that claimed to cover it built links that `ValidateIssueLink` rejects
  first, so the error they observed came from a different branch.

The guard had no call site, and no data to guard. Restoring it would not be
restoring a guard; it would be building an unbuilt feature.

## Decision

**1. munsu does not own the invariant "a merged PR closes its issue."** That
invariant has two owners, both outside munsu: the PR body author writes the closing
keyword, and GitHub enforces it at merge. munsu's typed delivery request carries only
the merge method and pinned head/base refs; it neither composes a merge commit message
nor creates PRs.

**2. The delivery invariant munsu does own is the canonical Delivery Authorization** —
current generation, working phase, owner present, correct worktree/endpoint binding,
identity head matching the bound worktree head, no delivery hold, no committed
terminal outcome, no live authorization, and a currency check immediately before the
irreversible mutation. Issue closure is not part of that contract.

**3. The entire `IssueLink` model and guard cluster is deleted**, including the
`GitHubClient.ViewIssueState` seam that existed only to serve it. No tombstones, no
key blacklists, no `.meta` back-reading adapter.

**4. ADR-0004 §5 is retired.** No document claims munsu fails closed on closing
references any more.

**5. If auto-close guarantees are wanted later**, they enter through the front door:
an issue link field on `TaskDefinition` bound to generation, a new precondition in
the closed Delivery Authorization set, verification inside `AuthorizeDelivery`, and
post-merge reconciliation — with two-directional proof that a malformed link is
refused and a valid link is *not* refused. The deleted cluster is not revived.

## Consequences

Positive: an Accepted ADR describing a phantom guard is gone; `internal/domain` loses
a model with no users; the deadcode allow file shrinks by 11 entries; no tests remain
guarding functions the binary cannot reach.

Negative, stated plainly: after this ADR, nothing in munsu guarantees that a delivery
closes its issue. That is not a regression introduced here — it has been the actual
state since `1d0c4b5`. This ADR only stops the documentation from claiming otherwise.

Known blind spots of the deadcode lane, both hit by this deletion and both requiring
manual inspection: a dead function hidden behind a package-level function-value
variable (`defaultCheckIssueStateImpl` behind `defaultCheckIssueState`) is reachable
to the tool, and an interface method that loses its last caller
(`GitHubClient.ViewIssueState`) stays in the RTA set while the interface lives.
