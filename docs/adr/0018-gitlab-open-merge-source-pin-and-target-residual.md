# 0018. GitLab OPEN Merge Pins the Source Head and Names the Target-Branch Residual

* **Status:** Accepted
* **Date:** 2026-08-28
* **Extends:** ADR-0010 (delivery head invariant)
* **Triggered by:** Issue #659, follow-up to #651

## Context

ADR-0010 and #651 correctly refused OPEN GitLab mutation while the adapter had no
atomic way to bind the authorized delivery to the provider operation. The GitLab
Merge Requests API accepts the expected source commit SHA on its merge endpoint,
so retaining an unconditional `Merge` refusal would leave the capability
unavailable even though the source-head constraint can now be enforced by the
provider.

The same endpoint operates on the merge request's current target branch and does
not accept an expected target-branch parameter. The delivery identity still pins
the authorized base, so this missing parameter must remain an explicit residual
rather than an assumed invariant.

## Decision

`gitlabDeliveryProvider` accepts an OPEN merge request only after the existing
observation has established mergeability, a current passing `head_pipeline`
whose SHA equals the observed MR head, and authoritative approval evidence with a
nonempty `approved_by`. Its validation also requires the request head and base to
match the delivery identity and rejects GitLab's unsupported `rebase` method.

The irreversible mutation has one implementation path: `glab api` performs
`PUT /projects/:id/merge_requests/:iid/merge` with the authorized `sha` and an
explicit `squash` value. GitLab therefore refuses a moved source head as part of
the merge request. The former shell-out merge path is not retained.

### Named residual risk: GitLab target-branch observation-to-mutation race

The merge endpoint does not atomically compare the current target branch with the
authorized base. A target-branch change after the final provider observation and
before the API mutation could therefore direct the merge to a branch that was not
authorized.

The compensating check is the existing `verifyProviderHead` fence in
`internal/fleet/delivery_deliver.go`: immediately before entering the mutation
boundary it compares the observed provider base ref with the delivery identity's
pinned base ref. The source head remains atomically pinned by the API `sha`
parameter, and GitLab applies its own current mergeability, pipeline, and approval
checks during the merge request.

## Removal condition

Remove this residual only when GitLab exposes an atomic expected-target constraint,
or when a documented provider invariant or transaction prevents the merge request's
target branch from changing during the authorized window. Until then, the base
observation is a compensating check, not atomic target-branch enforcement.

## Consequences

OPEN GitLab delivery is available through the typed API path while stale observed
heads still fail closed before the irreversible call. A source-head race is
provider-refused by the pinned `sha`; the narrower target-branch race remains
visible and bounded by the named compensating check above.
