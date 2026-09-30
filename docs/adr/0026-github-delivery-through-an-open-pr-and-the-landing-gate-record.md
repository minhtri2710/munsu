# 0026. GitHub Delivery Merges an Open PR Pinned to the Exact Head; Every External Write Is Preceded by a Gate Record

* **Status:** Accepted
* **Date:** 2026-09-30
* **Replaces:** the GitHub delivery refusal of ADR-0010 §1 ("GitHub merge is out of scope")
* **Extends:** ADR-0010 (the delivery head invariant), ADR-0018 (the pinned-source provider shape), ADR-0025 (head-bound verdict and Words), ADR-0024 (a new refusal needs the owner's words)
* **Triggered by:** Human decision G297 ("munsu: đồng ý apply hết"): A5 (GitHub delivery through an open PR), G1 (the Human landing gate record) and the lesson groups 15, 24 and 39, in the munsu-roadmap delivery

## Context

ADR-0010 recorded that `Deliver` refuses a GitHub identity before any journal write, because
no GitHub adapter could enforce mergeability, exact head and exact base at the irreversible
boundary. ADR-0018 then gave GitLab an adapter whose merge call carries the authorized head,
so the provider refuses a moved head. `gh pr merge --match-head-commit <sha>` is the same
constraint for GitHub: the merge fails unless the pull request head is still that commit.

Two gaps sat beside it. Delivery reached its one external write (the provider merge) with
only internal journal receipts behind it: nothing recorded, in one place a Human can read,
which exact head was written under whose words. And a CI reading could come from a check
list that was empty, or from a watch command's exit code, which says nothing about which
check concluded how.

## Decision

### 1. GitHub delivery merges an open PR, pinned to the exact head

`githubDeliveryProvider` (`internal/fleet/delivery_github_merge.go`) implements the same
`DeliveryProvider` seam as GitLab. `validateDeliverRequest` no longer refuses GitHub;
`deliveryProviderFor` resolves it only when gh-axi and gh are Ready, with no fallback route.

The irreversible mutation has one implementation path: `gh pr merge <number> --repo
<owner>/<repo> --squash|--merge|--rebase --match-head-commit <authorized head>`. It never
passes `--auto` (which would defer the merge), `--admin` (which would bypass the PR's own
requirements) or `--delete-branch`. GitHub therefore refuses a moved source head as part of
the merge itself.

An OPEN observation is mergeable only when `mergeable == MERGEABLE` (conflicts are a
separate provider fence, as `detailed_merge_status` is for GitLab), the CI proof of section 2
is complete and `domain.PR.CanMerge` accepts it. A provider review requesting changes
blocks; a provider approval carries no weight (ADR-0025 §4).

### 2. CI proof is per check, by id and conclusion, and an empty proof refuses

The proof is read from the head commit's check runs and commit statuses, never from a watch
command's exit code (`gh run watch` and `gh pr checks --watch` are not used). For each check
name the report with the highest id is the current one, and its own `conclusion` (or status
`state`) decides:

* a check that is not completed is pending; `success` is passed; every other conclusion is
  failed, including ones this code does not know;
* a skipped or neutral check counts as passed only when the base branch requires it, as
  GitHub's own requirement does; a skipped optional check carries no proof and is left out.

The observation refuses, before any mutation, when:

* GitHub reported no check for the head;
* only skipped optional checks remain;
* a check the base branch requires never reported for the head. The required set is the union
  of the branch protection `required_status_checks` contexts and the ruleset
  `required_status_checks` rules for the base. A read of either source that fails for any
  reason other than "no required checks configured" (`Branch not protected`, `Required
  status checks not enabled`) refuses.

The ProviderSnapshot that `delivery pr-merge` uses as its pre-journal gate reads the same
evidence through the same function, so there is one reader of GitHub CI state.

### 3. The base ref is a named residual

`gh pr merge` has no expected-base parameter, as GitLab's merge endpoint has none
(ADR-0018). A base change after the final observation and before the call could land the
merge on a branch that was not authorized. The compensating check is the same
`verifyProviderHead` fence: immediately before the mutation boundary, the observed base ref
is compared with the identity's pinned base ref. Removal condition: GitHub exposes an atomic
expected-base constraint on merge.

A token that cannot read branch protection (it needs admin rights) and whose failure does not
read as "not protected" makes the required-set read fail, so the delivery refuses; it is
never read as "no required checks".

### 4. Every external write is preceded by one gate record

`Canonical.RecordGate` (`internal/taskauthority/canonical_gate.go`) appends one immutable
evidence document per external write, keyed by task and operation identity and committed with
its operation receipt through `Home.Commit` under the task scope lock (ADR-0008 §2). The
record carries the operation (`provider-merge`), the exact head, the authorization it
accompanies and the Human's `domain.Words` (ADR-0025).

It refuses unless that authorization is the task's current, unrevoked, non-terminal one,
still current (no hold, no reservation, no drift), for the same operation and exactly the
gated head. The same Operation ID with the same digest replays; a record is never rewritten.
It does not advance the Task revision, so the authorization stays current.

`Deliver` appends the record after the provider validates the merge request and before it
persists the irreversible-mutation boundary. A write whose record cannot be appended is
refused: the authorization is released and no merge runs. Internal journal receipts stay
receipts; they are not gate records.

The merge is the only external write Fleet performs today. Fleet does not push, and the
mutation of a pull request other than its merge has no caller. The gate record's operation
is the closed `DeliveryAuthorizationKind`, so a later external write adds its kind there
and its own record before its call, with no change to the record shape.

### 5. Words for the new refusals

Under ADR-0024 D1, the landing-gate refusal (section 4) and the missing-required-check and
empty-proof refusals (section 2) ship under G297: grantor the Human, channel
supervisor-relay:typed, quote "munsu: đồng ý apply hết".

`delivery pr-merge` takes `--grantor`, `--channel` and `--quote` (required) and carries them
into `DeliverRequest.Words`, the authorization and the gate record. `delivery record-verdict`
records the reviewer's verdict through `Canonical.RecordReviewVerdict`; it refuses a reviewer
that is the task's own endpoint (its incarnation or its handle).

## Consequences

* GitHub delivery is available for an open PR. Deliveries that used to refuse at
  `validateDeliverRequest` now run the same journaled path as GitLab, including the verdict
  and Words requirements of ADR-0025.
* ADR-0018's sentence that approval-rule evidence is the approval authority is superseded by
  ADR-0025 §4: neither provider's approval is read. GitLab reads only reviewers who requested
  changes.
* The provider approval states (approved, pending and dismissed), the GitHub review
  normalizer and GitLab's approval-state read are deleted. A GitHub review decision of
  CHANGES_REQUESTED blocks; an empty decision no longer refuses.
* Tests that pin the GitHub refusal, the provider-approval reads, the deleted normalizer, or
  a delivery without a gate record are rewritten in the test phase.
* ADR-0010 keeps its two-owner decision, its `.meta` rule and its amendment-orphan record.
  Its Status line names this ADR as the replacement of the GitHub refusal.
