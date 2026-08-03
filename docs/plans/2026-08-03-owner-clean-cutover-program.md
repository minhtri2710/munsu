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

## Progress

- #403 (Home foundation) — ACCEPTED, integrated `edbee73e`. Canonical `internal/home`
  durable mechanics (Init/Open/Identity/RootFor/Path/Read/Commit/Lock/AcquireLease,
  typed errors, journal recovery).
- #404 (scoped identity + operation preconditions) — ACCEPTED after REWORK, integrated.
  `internal/domain` now has typed nominal identities (TaskID, CaptainID, ProjectID,
  HomeID, OperationID, ResourceID), `Operation`+typed `Intent` digest, `Precondition`/
  `ConflictFrom` (only verified mismatches become ErrStalePrecondition). `Mutation`
  envelope removed. Rework review PASS on commit `99f9442f`; full suite green after
  integration.
- #405 (Task lifecycle over canonical Task Authority) — ACCEPTED after REWORK (hard cut),
  integrated. Canonical `Canonical` surface in `internal/taskauthority` (Create/Get/List/
  Start/Block/Unblock/Complete/Reopen/Readiness/AddHold/ReleaseHold/ListHolds) backed by
  `internal/home` durable mechanics and typed `domain` identities/operations/preconditions.
  `TaskAuthoritySchema` hard-cut to `munsu.task-authority/v1` (ADR-0008 §11); legacy
  `taskauthorityfs`/`storecontract` aligned to the same v1 contract and staged for deletion
  in #417. Re-review PASS on `de48a6ea`; full suite green after integration. Legacy CLI
  wiring and `taskauthorityfs` deletion deferred to #416/#417.
- Mirror parity repair (pre-existing) — resolved, integrated `49841760`.

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

## Task decomposition rule (Supervisor orchestration config, 2026-08-03)

- When one authorized task is too large for one Peer, decompose it into the smallest
  coherent outcome-and-ownership scopes and staff multiple Paseo Peers.
- Parallelize only scopes with non-overlapping file and contract ownership.
- Serialize shared foundations, shared contracts, overlapping file ownership, and
  integration-sensitive dependencies.
- Each Peer still requires its own outcome-focused brief, isolated editing workspace when
  applicable, direct final report, and independent review gate.
- Lead retains dependency ordering, architecture rulings, cross-scope decisions, integration,
  and final acceptance. Do not delegate Lead authority or create nested hidden orchestrators.
- Peer creation inside the already authorized program scope does not require additional human
  confirmation.

## Authorization policy (Supervisor, 2026-08-03)

- For answer/explain/review/diagnose/research/plan requests: inspect and report the result;
  do not modify files, agent state, GitHub state, or external systems unless the request
  explicitly asks for changes.
- For change/build/fix requests: make the requested in-scope local changes and run
  relevant non-destructive validation without asking for confirmation first.
- Require explicit human confirmation before: external writes or externally visible
  mutations; pushing/opening/merging PRs or changing GitHub issue state; destructive or
  irreversible actions; purchases or paid resource usage; credential/permission/security-
  sensitive changes; materially expanding scope beyond the authorized outcome and ownership
  boundary.
- Creating/managing authorized Paseo Peers is NOT a scope expansion, provided their work
  stays within the Lead's existing authorization.
- Ordinary local implementation, commits on isolated local branches, tests, builds, vet,
  static checks, and read-only inspection are pre-authorized when required by the assigned
  change/fix outcome.
- Do not manufacture confirmation requests for routine local decisions.
- Existing stricter program constraints remain: no push/PR/merge to origin, GitHub issue
  mutation/closure, destructive action, or material scope expansion without explicit human
  authorization.
- Does not override the Peer direct-report protocol, independent-review gates, worktree
  isolation, provider resolution, or the >200k-token compaction rule.

## Peer/Lead context compaction rule (Supervisor, 2026-08-03)

- Applies to the Lead and every Engineer, Reviewer, Architect, Shadow, Advisor, and Council
  Peer. Threshold: observed context usage >200,000 tokens.
- Do NOT poll solely to measure usage; use provider/Paseo metadata or agent self-report via
  normal lifecycle signals.
- Before continuing substantial work after crossing the threshold, use provider-supported
  compaction.
- **Lead compaction must preserve:** Lead role and Supervisor identity (`3954417`),
  authorized program scope, dependency DAG and issue status, workspace/branch/exact head,
  rulings and acceptance decisions, unresolved blockers, verification state, next action,
  every active child-agent ID/status/scope/expected report, and milestone/report protocols.
- **Peer compaction must preserve:** role, Lead agent ID (`5331588`), assigned outcome and
  ownership boundary, workspace/branch/head, decisions, files changed/reviewed, unresolved
  findings, verification, next action, and the final direct-report protocol.
- After compacting, confirm continuity to the owning parent before substantial work resumes.
- If Peer compaction fails, report BLOCKED to the Lead. If Lead compaction fails, report
  BLOCKED to the Supervisor. The owning parent may relaunch with a bounded context pack;
  ownership and unresolved findings must not be silently lost.

## Known pre-existing issue (tracked, outside #403)

`internal/cli` `TestAgentSkillMirrorsMatchCanonical` fails on the base commit `d217664f`
(canonical skill-mirror file-count mismatch for `captain-provisioning`/`munsu-update`).
Confirmed pre-existing — not caused by #403 or by the Lead docs commit. It keeps the full
`go test ./...` gate red until repaired separately. Must be triaged/authorized before the
#418 activation gate. It is unrelated to the Home mechanics.

### RESOLVED (2026-08-03) — pre-existing activation-gate repair

Authorized by the Supervisor as a pre-existing-defect repair OUTSIDE issues #402–#418.
Read-only triage (`37f2a65`) classed it as outside the program; root cause commit `0bce4616`
added canonical companions without mirroring them. Implemented as one isolated commit
`49841760` on `fix/skill-mirror-parity` (Engineer `088ddc6a`), reviewed PASS by independent
reviewer `0c0ac4a7`, integrated into the Lead branch. Added exactly three byte-identical
mirror files: `.agents/skills/captain-provisioning/{MIGRATION,REFERENCE}.md` and
`.agents/skills/munsu-update/REFERENCE.md`. Full `go test ./...` now PASSES. This is a
pre-existing activation-gate repair, NOT completion of any issue #402–#418.