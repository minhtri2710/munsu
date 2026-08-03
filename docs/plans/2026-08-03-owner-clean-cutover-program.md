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

## Peer context compaction rule (Supervisor, 2026-08-03)

- When a Peer's observed context/token usage exceeds 200,000 tokens, require that Peer to
  compact its context before continuing substantial work.
- Use provider-supported compaction/session summarization. Preserve in the compacted
  context: assigned outcome and ownership boundary, Lead agent ID (`5331588`), exact
  branch/workspace/head, decisions and rulings, files changed or reviewed, unresolved
  findings, verification already run, current next action, and the mandatory final
  direct-report protocol.
- Do not compact solely from an unsupported guess. Apply when Paseo/provider metadata
  reports usage above the threshold or the Peer explicitly reports it.
- Compaction must not reset ownership, create a replacement implementation path, discard
  unresolved review findings, or silently lose the final-report requirement.
- After compaction, the Peer confirms continuity to the Lead before resuming edits/review.
  If compaction is unavailable or fails, the Peer reports BLOCKED with observed token usage
  and provider limitation; the Lead decides whether to relaunch a replacement Peer with a
  bounded context pack.
- Do not poll running agents merely to measure tokens; apply using normal notifications,
  reports, or already-available lifecycle metadata.
- Applies to all Engineer, Reviewer, Architect, Shadow, Advisor, and Council Peers under
  this Lead.

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