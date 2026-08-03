# Owner-Clean Cutover Program (Issues #402–#418)

Status: active
Lead: Paseo agent `5331588` (Lead: GitHub issues 402-418)
Branch: `ai/lead/github-issues-402-418`
Canonical contract: ADR-0008 (`docs/adr/0008-owner-clean-architecture-and-pre-public-v1-reset.md`)

## Scope

One coherent pre-public architectural reset of munsu, delivered as a single owner-clean
cutover. Dependency-ordered internal work on one branch; no intermediate architecture is
supported. The final accepted state is the complete owner-clean target per ADR-0008.

## Dependency DAG

```
#403 Home init + durable mechanics                     (no deps)
  └─ #404 scoped identities + operation preconditions  (dep #403)
       ├─ #405 Task lifecycle reads/mutations          (dep #403, #404)
       │    └─ #406 Task Authority durability/bindings (dep #405)
       ├─ #407 Project/Captain lifecycle into Fleet    (dep #403, #404)
       │    ├─ #408 Deepen Config                      (dep #404, #407)
       │    │    └─ #411 Backend/Harness explicit      (dep #404, #408)
       │    └─ #413 Task Transfer                      (dep #406, #407)
       └─ #409 Uplink journal                          (dep #403, #404)
            └─ #410 Config Assignment via Uplink       (dep #407, #408, #409)
#412 Soldier launch/retirement                         (dep #406, #407, #408, #411)
#414 Delivery Authorization + execution                (dep #406, #411, #412)
#415 Supervision typed observations + per-home watchers (dep #409, #411, #412)
#416 CLI composition + rendering seam                  (dep #405, #407, #408, #409, #411, #412, #413, #414, #415)
#417 Contract repository to owner-clean                (dep #406, #410, #412, #413, #414, #415, #416)
#418 Architecture policy + activation gate             (dep #417)
```

Longest path (critical): #403 → #404 → #407 → #408 → #411 → #412 → #414 → #416 → #417 → #418

## Definition of done per issue

Each issue's acceptance criteria in its GitHub body are the acceptance contract. The
Reviewer, an independent read-only audit provider, verifies the exact commit/head against
those criteria plus the ADR-0008 invariants.

## Staffing / integration policy

- Lead `5331588` creates every Engineer (impl provider, editing) and Reviewer (audit
  provider, read-only) Peer as its own Paseo subagent.
- Each editing task runs in a worktree-isolated Paseo workspace branching off the current
  Lead branch. No two agents own the same files/scope concurrently.
- Every Peer must end with a direct report via `paseo send 5331588 --no-wait
  --prompt-file <report> --json`, confirming status=sent (retry once), with planItem,
  result, commitOrReviewedHead, verification, risks, recommendedNextAction.
- Lead rulers on all reviewer findings; rework through the original Engineer where
  appropriate.
- Integration into the Lead branch happens in dependency order; conflicts resolved
  deliberately; issue-specific and full `go build ./...`, `go vet ./...`, `go test ./...`
  run at meaningful gates.
- Delivery mode is no-mistakes. No push, PR, merge, or issue-state mutation without
  explicit human authorization.

## Acceptance gates (final, #418)

Full Go build/vet/test, current contract, crash/replay/fencing suites, architecture policy,
performance budget, and a clean diff/repo scan confirming deleted identifiers, versions,
paths, commands, fixtures, comments, skills, and documents did not survive.

## Known pre-existing issue (tracked, outside #403)

`internal/cli` `TestAgentSkillMirrorsMatchCanonical` fails on the base commit `d217664f`
(canonical skill-mirror file-count mismatch for `captain-provisioning`/`munsu-update`).
Confirmed pre-existing — not caused by #403 or by the Lead docs commit. It keeps the full
`go test ./...` gate red until repaired separately. Must be triaged/authorized before the
#418 activation gate. It is unrelated to the Home mechanics.