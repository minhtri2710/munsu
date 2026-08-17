# 0016. Review Before Merge Has No Technical Gate; the Orchestrator Owns It Until a Second Review Identity Exists

* **Status:** Accepted — temporary constraint, see [Removal condition](#removal-condition)
* **Date:** 2026-08-17
* **Extends:** ADR-0008 (the least-painful patch is chosen only under a bounded constraint, and then the constraint and its removal condition are recorded in-repo)
* **Triggered by:** BEO-111 (branch protection audit during the #502 review) ← BEO-101 verdict on #483

## Context

`main`'s protection, read from `GET /repos/minhtri2710/munsu/branches/main/protection`
on 2026-08-17:

```
required_status_checks.strict        true
required_status_checks.contexts      Build and test, Race detector, Integration tests, Repo invariants
enforce_admins                       true
allow_force_pushes                   false
allow_deletions                      false
required_linear_history              false
required_conversation_resolution     false
required_pull_request_reviews        key absent
```

Four CI gates are real, and `enforce_admins: true` applies them to the owner too. There
is **no** review gate of any kind. "Review before merge" is held by the orchestrator's
discipline; nothing in the repository or in GitHub refuses a merge that skipped it.

### The case this is not hypothetical

PR #483 (flake ledger v0) opened `2026-08-16T06:28:39Z` and merged `09:11:45Z`
(`6ce60da`) by `minhtri2710` — the same account that authored it. `GET /pulls/483/reviews`
returns `[]`: zero reviews, zero review comments.

Its independent review, BEO-101, was created at `09:15:54Z` — **four minutes after the
merge** — and its own text describes the PR as open and unmerged. The review was
commissioned against a state that no longer existed. The verdict arrived at `09:35:14Z`:
**FAIL**, twelve findings, three of them High and one Medium-High — by then findings on
`main` rather than on a pull request:

* A transient API failure on the sweep's fast path makes it delete the ledger row it just
  filed, together with that row's `owner_issue` and `deadline`, and open a bot PR
  proposing exactly that deletion. Every other API call in the script is `|| die`; these
  two swallow the error. [High]
* The only half that runs on **every** PR (`.github/scripts/flake-ledger.sh`) has no test
  at all: 10 of 10 mutations survived, including removing the deadline comparison. [High]
* Both failure directions of the sweep's second acceptance criterion are unguarded —
  replacing either with `if false` left `selftest` green. [High]
* `state: fixed:<any-ref>` silences a row permanently, with no check that the ref exists
  or that the owning issue closed. [Medium-High]

The self-deleting path is the sharpest of the four: a mechanism built to accuse erases its
own evidence, automatically, with nobody pressing anything.

Two things this evidence does **not** say. The four CI checks were green on #483 — the
findings are precisely the class CI cannot see, so this is not an argument for more
checks. And a review gate would not have *found* these; the reviewer found them. A gate
would only have kept them off `main` while the reviewer was still reading — which is the
entire difference between a finding and a defect in production.

## Decision

### 1. There is no technical review gate. State it plainly, everywhere.

Do not read "four required checks, `strict`, `enforce_admins`" as review protection. Those
checks prove the tree builds, the tests pass, the race detector is quiet and the repo
invariants hold. They do not prove anyone read the diff. The only thing between an
unreviewed change and `main` is the orchestrator choosing to wait.

Nothing in this repository may be written as though a review gate exists.

### 2. The constraint has one owner: the orchestrator

Whoever performs the merge owns the claim that review happened. Concretely: an independent
verdict exists on the issue, from an identity other than the change's author, before the
merge — not after.

Because nothing checks this, it is **owned** rather than enforced. Two consequences follow
directly. A merge with no verdict waits, however long the queue is. And a merge performed
without one is a breach of this rule, reported as a breach — not repaired by
commissioning the review afterwards, which is what happened on #483 and is why the review
issue contradicted reality four minutes after it was written.

### 3. Do not enable `required_pull_request_reviews` in the current configuration

This is the section most likely to be lost, and the most expensive to rediscover. Turning
the flag on today deadlocks the entire repository:

1. Every PR here is authored by `minhtri2710`; the agents run under that credential.
2. GitHub does not let an author approve their own pull request.
3. Agent review verdicts live on Multica issues, not as GitHub reviews. Nothing in the
   system produces the approval the flag would demand.
4. `enforce_admins: true` — correct, and kept — means the owner cannot bypass the block
   either. The deadlock has no in-repo escape; clearing it requires editing protection at
   the account level.

The result is every merge in the repository blocked indefinitely, with no error message
explaining why: the silent-hang failure class BEO-109 was opened to remove.
`required_pull_request_reviews` becomes a gate only *after* a second review identity
exists, and never before.

The existing four checks, meanwhile, are load-bearing beyond CI: the flake-ledger bot's PR
is unmergeable only because a `GITHUB_TOKEN`-authored PR has no check runs and sits
pending. Loosening the required checks would quietly hand that bot a merge path.

## Removal condition

Sections 1–3 are a temporary constraint. They exist because a second review identity does
not, and they must be **deleted** when it does — a temporary constraint that outlives its
reason is debt.

A GitHub App or machine user is the right answer, not a rejected one. The reason it is not
being done in this ADR is an **access constraint, not cost**: creating a GitHub App,
installing it on the repository, and storing its private key as a secret are all
account-level GitHub operations that agents cannot perform. Deferring is preferable to
holding the delivery queue against an action nobody in the loop can take.

**What a second identity requires**

1. An identity with write access to `minhtri2710/munsu` that is not the PR author: a
   GitHub App installed on the repository with `pull_requests: write` and
   `contents: read`, or a machine user added as a collaborator.
2. Its credential reachable by the reviewer agent — App ID plus private key as Actions
   secrets, or the agent runtime's credential store. Never in the repository.
3. The reviewer agent submits its verdict as a real GitHub review,
   `POST /repos/minhtri2710/munsu/pulls/{n}/reviews` with `event: APPROVE` or
   `REQUEST_CHANGES`, *in addition to* the issue comment, which stays the readable record.

**Verify before flipping the flag.** On a throwaway PR, confirm that a review submitted by
the chosen identity actually satisfies `required_approving_review_count`. A collaborator
machine user does. An App installation review does when the App holds `pull_requests:
write` — worth observing once rather than assuming, because if it does not, enabling the
flag deadlocks the repository exactly as §3 describes.

**What changes once it exists**

* Enable `required_pull_request_reviews` on `main` with `required_approving_review_count:
  1` and `require_code_owner_reviews: false`.
* Set `dismiss_stale_reviews: false` initially, deliberately. `strict: true` requires
  branches to be up to date, so every update-branch push would dismiss the approval and
  force a re-review each time `main` moves — a livelock on a busy queue. Revisit only
  against a measured queue.
* The protection endpoint is a **full replacement**: resend `required_status_checks` with
  all four contexts and `strict: true`, plus `enforce_admins` and `restrictions`, in the
  same `PUT`, or they are cleared. A protection edit is the worst place to learn this.
* Delete §1–§3 of this ADR, and the orchestrator's ownership of the rule with them. An
  enforced gate does not need an owner written down.

## Alternatives rejected

**A CI-generated required status check that reads review state from the Multica issue and
reports `success`/`failure` onto the commit.** Rejected on three independent grounds, each
sufficient alone. (a) It is itself repository automation of exactly the class that produced
the #483 findings — building the gate out of the same material the gate exists to catch is
self-referential. (b) It makes the merge gate depend on a network call to an external
system: fail-open makes the gate fake, which is worse than no gate because it looks like
one; fail-closed makes every Multica incident block the whole merge queue, and that
incident class is real — 2026-08-17 saw hours of GitHub 429/503, three CI jobs dying in
`Set up job`, and API failures severe enough to need a retrying REST merge. (c) It still
runs under the single credential, so it creates no second identity and relocates the
discipline rather than enforcing it.

**`required_conversation_resolution: true`.** The one review-adjacent lever that needs no
second identity. Rejected because it gates on unresolved GitHub PR conversations, while
agent reviews are Multica issue comments: there is nothing to resolve, so it is green on
every PR by construction. A gate that cannot fail is a false claim of protection.

**Leave the state as it is and write nothing down.** Rejected — that is precisely the
state that produced #483, where the rule lived only as a habit and its breach was invisible
until a review that had been commissioned too late contradicted itself.

## Consequences

* No branch protection changes. The measured configuration above stays exactly as it is,
  and this ADR authorises no `gh api` write against `main`.
* The rule "an independent verdict before merge" now has a named owner and a written
  breach condition, instead of being an assumption each participant re-derives.
* A future reader who finds `required_pull_request_reviews` missing has the reason and the
  prerequisite in one place, and does not enable it into a repository-wide deadlock.
* The gap is stated, not closed: until a second identity exists, a hurried merge is still
  possible, and #483 remains the recorded proof that it happens.
