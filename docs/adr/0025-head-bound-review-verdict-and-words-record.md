# 0025. Delivery Requires a Head-Bound PASS Verdict From a Non-Author Reviewer; Decisions Carry the Human's Words

* **Status:** Accepted
* **Date:** 2026-09-30
* **Supersedes:** ADR-0016 (review before merge has no technical gate)
* **Extends:** ADR-0008 (task-authority is the one authority), ADR-0010 (the delivery head invariant), ADR-0022 (the durable delivery contract)
* **Triggered by:** Human decision G297 item 3 ("delivery requires a PASS verdict bound to the exact SHA, from a reviewer that is not the soldier. With no such reviewer, delivery refuses (fail closed).") and the S13 words record, both in the munsu-roadmap delivery

## Context

ADR-0016 recorded that review before merge was held only by the orchestrator's
discipline: nothing in munsu refused a delivery that skipped it. ADR-0010 later put the
delivery acceptance rules in the provider boundary, where `PR.CanMerge` required a
provider approval. That approval cannot be produced here. Every PR is authored under one
GitHub account, GitHub does not let an author approve their own pull request, and agent
review verdicts never reach GitHub as reviews (ADR-0016 §3). The acceptance rule was
either unsatisfiable or satisfied by a provider state that says nothing about who read
which head.

Two more gaps sat beside it. A review that ran while the reviewed checkout was moving
judged a tree nobody can name. And a decision hold could be released with any free-text
answer, leaving no record of who gave the words or where.

## Decision

### 1. A review verdict is a domain record bound to one head

`domain.ReviewVerdict` records the outcome (`pass` or `fail`), the reviewed head SHA, the
reviewed range base, the reviewer, the authoring soldier instance the reviewer must
differ from, and the review-custody observations: the checkout's HEAD and a digest of its
porcelain status before and after the review (`domain.TreeState`).
`ReviewVerdict.Validate` refuses a verdict whose base equals its head, whose reviewer is
the author, whose recorded HEAD is not the reviewed head, or whose tree moved during the
review. `ReviewVerdict.Approves` is the one acceptance rule: valid, PASS, bound to
exactly the head being delivered, and naming exactly the current authoring instance.

The authoring soldier instance is the bound endpoint's incarnation, the identity Fleet
mints for one endpoint binding. A new soldier instance for the same task is a different
author; a reviewer must differ from whichever instance authored the head.

### 2. The task records at most one verdict, for its current head

`Canonical.RecordReviewVerdict` is a taskauthority named operation. It requires a working
task with bound worktree and endpoint, a verdict naming exactly the bound worktree head,
and an author equal to the bound endpoint's incarnation. It stores one
`ReviewVerdictRecord` on the task aggregate and advances the revision. A later verdict
replaces the earlier one. A repair head therefore has no verdict until it is reviewed on
its own: no verdict carries over to another head, because the record names one head and
`Approves` compares it with the delivered one. Reopening a task starts a new generation
with no verdict.

### 3. Delivery authorization refuses without an approving verdict

`Canonical.AuthorizeDelivery` refuses unless the task's verdict `Approves` the identity
head under the bound author instance. A missing, mismatched, non-PASS, self-authored or
tree-moved verdict refuses; there is no fallback. The issued `DeliveryAuthorization`
embeds the verdict it relied on, so the evidence is immutable, and
`validateDeliveryAuthorization` re-checks it on every read.

Reviewer and author identities are claims made by the caller of `RecordReviewVerdict`.
munsu does not authenticate them. The independence guarantee is therefore exactly as
strong as the seat that records the verdict; launching a reviewer seat that cannot write
the checkout is a separate scope.

### 4. The verdict is the only approval source; a provider review can only object

Options: replace the provider review input, keep it beside the verdict, or make the
verdict the only approval source. The verdict is the only approval source.
`PR.CanMerge` keeps the provider facts that block a merge (open, checks passed, no review
requesting changes) and no longer requires an approval. `Review.IsApproving` and the
"has approval" branch are deleted.

Keeping the provider approval as a second requirement would make two approval contracts,
one of which is unsatisfiable under a single account (Context). Deleting the provider
review input outright would also drop a real objection signal and rework the GitHub and
GitLab observers, which belong to the delivery-wiring scope. A provider "changes
requested" still blocks; a provider "approved" carries no weight.

ADR-0016 §3 stands as the GitHub-side record: `required_pull_request_reviews` stays off
until a second GitHub identity exists. The munsu verdict does not depend on it.

### 5. A decision carries the Human's words

`domain.Words` records the grantor, the channel and the verbatim quote. `Words.Validate`
refuses an empty quote, grantor or channel. Two operations carry it:

* `Canonical.ReleaseHold` requires words and stores them on the released hold
  (`DispatchHold.ReleaseWords`). `munsu decision-hold resolve` and `complete` take
  `--grantor`, `--channel` and `--quote`; `complete` releases holds too, so leaving it
  without words would bypass the record.
* `Canonical.AuthorizeDelivery` requires words and stores them on the authorization
  (`DeliveryAuthorization.Words`). Fleet carries them through `DeliverRequest` and the
  delivery journal.

The record is a claim. Recording a grantor does not verify that the grantor said it, and a
claimed provenance is never verified authority. The `needs-decision` status projection is
unchanged; the resolved and unblocked projections are a later classification question.

## Consequences

* Delivery cannot start until a non-author reviewer records a PASS on the exact head.
  Nothing in the binary records a verdict yet: the reviewer seat and the delivery wiring
  that call `RecordReviewVerdict` and fill `DeliverRequest.Words` are later scopes, so
  until they land every delivery refuses. This is the intended fail-closed state.
* Any verdict, hold-release or delivery-authorization document written before this change
  no longer validates. The project is pre-launch; there is no migration.
* Tests that pin the removed provider-approval requirement, or that release holds or
  authorize deliveries without a verdict or words, describe the old contract and are
  rewritten in the test phase.
* ADR-0016 is superseded. Its incident record and its branch-protection guidance remain
  readable there.
