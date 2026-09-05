# Repo Refresh — deletion/keep plan (munsu)

- **Date:** 2026-09-05
- **Authority:** Human directive via Supervisor — "cho munsu ... sử dùng skill refresh repo, dọn test proof này kia đi, cleanup những thứ đã không dùng không liên quan deprecated đi".
- **Skill:** repo-refresh (apply mode); test-proof-debt-audit for the proof cluster.
- **Base:** main@dcf22c1f. **Delivery:** one branch, partitioned Engineers (pi/cliproxyapi-gpt-5.6-luna, alt zai/glm-5.3-flash), Reviewer kind=agy, no-mistakes gate.
- **HARD GATE:** this list is the Human's to approve BEFORE any deletion. Scope judgement ("không liên quan"/"deprecated") is theirs. Tiers B and C carry judgement calls flagged **[HUMAN RULING]**.

## Method — every claim is verified, not inferred

Ran the actual `citations.sh check` in a clean worktree at HEAD, deleted the candidate set with **no** compensating edits, and read the exact failures. That is the CI gate itself, not a guess. Result: the citations gate is the only gate any deletion touches; it fails only where noted, and the compensating edit for each is listed. Baseline (HEAD) = exit 0; candidate-set delete without compensation = exit 1 with precisely the couplings in Tier A/C below. Residual-roadmap deletion produced **no** citations error (its ADR references are markdown links, which the gate does not extract).

---

## KEEP (load-bearing — not touched)

- **All CI proof machinery that gates:** build-tags.sh, citations.sh, deadcode.sh, flake-ledger.sh, flake-sweep.sh, uncovered-guards.sh, update-auto-merge-prs.sh + their Go tools (citations/, guardsites/, flake-sweep-topology/) + data (build-tags.manifest, citations.allow, deadcode.allow, flake-ledger.md, uncovered-guards.baseline, testdata/citations/**). All referenced by ci.yml / auto-update-branch.yml / flake-ledger.yml.
- **~200 `*.out` under `.github/testdata/uncovered-guards/**/profiles/`** — force-added test fixtures, not debris.
- **Embed/CI-pinned docs:** all 5 `docs/skills/*.md` (pinned by internal/cli/skills_test.go), all 5 `docs/supervision-protocols/*.md` (//go:embed in seed_orchestrator_manual.md), docs/architecture.md, docs/port-mapping.md, docs/workflow-topology-matrix.md (citations.sh evidence docs), docs/orchestration-contract.md, docs/images/munsu-mascot.png.
- **All ADRs** — immutable decision records. (Tier C touches only stale *path citations* inside ADR-0017, not the decision.)
- **The Go premise/guard tests** (`TestPremise*`, `TestGuardBurnDown*`) — the real refusal-branch proof; stays after Tier C.

---

## Tier A — safe deletes (self-contained; CI verified green when done with the listed coupled edit)

| # | Delete | Reason | Coupled edit (same change) |
|---|--------|--------|----------------------------|
| A1 | `docs/plans/2026-07-30-incident-remediation-master-plan.md` | terminal plan, superseded, no live tracked inbound | none |
| A2 | `docs/plans/2026-08-01-task-authority-implementation-plan.md` (124KB) | terminal; ADR-0008 is the durable owner | none |
| A3 | `docs/plans/2026-08-03-owner-clean-cutover-program.md` | terminal execution program, complete | none |
| A4 | `docs/plans/adr-0021-supervision/` (4 SPEC files + CAPABILITY-MAP) | ADR-0021 Accepted/shipped; specs were execution scaffolding | none |
| A5 | `docs/plans/adr-0022-delivery-contract/SPEC-delivery-contract.md` | ADR-0022 Accepted; spec was execution scaffolding | none |
| A6 | `docs/firstmate-skill-audit.md` | stale 2026-07-21 audit of the external firstmate tool; unrelated to munsu | **remove its 28 waiver rows** in `.github/citations.allow` (all rows keyed `docs/firstmate-skill-audit.md:` — verified: deleting the doc makes them "resolve or no document makes them", gate red until removed) |
| A7 | `.no-mistakes-document-result.json`, `.review-result.json`, `review_findings.json` (repo root) | ephemeral gate/tool output, 0 consumers | Tier D2 gitignores the patterns |

**Verified:** with A6's waiver rows removed, no citations error remains for Tier A.

---

## Tier B — residual-roadmap **[HUMAN RULING]**

`docs/plans/2026-09-03-owner-clean-residual-roadmap.md` — COMPLETE 2026-09-04 (memory: owner-clean-residual-roadmap). **Deleting it does NOT break the citations gate** (verified — the ADR references are markdown links). It leaves 4 cosmetic broken relative links:

- ADR-0023:8 (Triggered by), ADR-0003:50 (C1), ADR-0003:86 (G4), ADR-0008:3 (G3 gate).

**Option B-keep:** DEMOTE — keep as the terminal roadmap record; ADR links stay valid. Zero edits.
**Option B-delete:** DELETE + rewrite the 4 link sites to inline the landed status (each is a one-clause swap, e.g. "in the [residual roadmap](...)" → "landed as #748/#749/#753"). Scope line: editing an ADR's *status pointer* to reflect landed reality is in scope; rewriting an ADR's *decision* is not.

Recommend **B-delete** (owner-clean: a generated/terminal roadmap is a view, standard §"Plans And Trackers"), but this is the Human's call since it edits Accepted ADRs.

---

## Tier C — mutation-check.py cluster **[HUMAN RULING — SETTLED 2026-09-05: "Cắt §1 + §2.1–2.4, giữ §2.5"]**

Delete `.github/scripts/mutation-check.py`, `.github/mutation-cases/beo-69-cli.tsv`, `.github/mutation-cases/beo-69-taskauthority.tsv`.

**Human ruling (AskUserQuestion via Supervisor, verbatim):** `"Cắt §1 + §2.1–2.4, giữ §2.5"`. The clarification: ADR-0017 §2 was two different things — §§2.1–2.4 are the (now-deleted) mutation-check.py batch-tool rules, but §2.5 "The uncovered-guards baseline is an identity ratchet" documents the LIVE uncovered-guards.sh required-CI check, and §1 "Layer 2 is declared, not derived" is wholly the mutation-check Layer-2 record. Literal "excise §2" would have deleted live-gate documentation. **Executed cut:** remove §1 and §§2.1–2.4 (both exclusively the mutation-check record) + the stale Context bullet 1; PRESERVE §2.5 verbatim under its `### 2.` header (number kept so the Consequences "§2.5" reference holds); reconcile the surviving §1/§2/Layer-2/W1/W2 cross-references to past tense with removal date 2026-09-05; the three stale path citations (39/41/73) are neutralised by the removals themselves. §2.5's ratchet-admission policy is unaffected.

**test-proof-debt-audit verdict: DELETE.** It observes nothing on `main` — wired to no workflow, run by hand per burn-down batch. Zero deletion sensitivity: if the guards broke, this tool would not fail because nothing runs it; the real proof is the Go premise tests (kept). The `.tsv` anchors pin a finished batch (BEO-69 batch-5). ADR-0017 §5 prescribes deleting it together with §2 once batches end — condition met (deadcode.allow: 0 batch rows; baseline's 27 rows are all triaged permanent waivers). Keeping it is the dormant machinery ADR-0008 forbids.

**Coupled edit (verified required — gate red without it):** neutralize 3 stale *path citations* in ADR-0017 that name the deleted files:
- line 39 (`.github/scripts/mutation-check.py` — text says "is not on main", now factually stale since #511/#519 merged it),
- line 41 (`.github/mutation-cases/beo-69-taskauthority.tsv`),
- line 73 (address line `.github/scripts/mutation-check.py, pending #511`).

**Executed (2026-09-05):** the 3 path citations were removed with the blocks that carried them (R1/R3 delete lines 39/41/73 outright), not merely reworded. The reconciliation reworded 9 surviving cross-reference sites (§2 bridging note, §3.1, the Removal condition closeout, W1/W2, the "layer 2" alternative, and 3 Consequences bullets) to past tense; §2.5 is untouched. Verified: the completeness grep leaves every §1/§2/mutation hit inside §2.5 or a reconciled line, and citations.sh names no ADR-0017 / mutation-check / mutation-cases path.

---

## Tier D — hygiene fixes

- **D1** — `AGENTS.md`/`CLAUDE.md` (same file; CLAUDE.md is a symlink) line 107: delete the dead sentence *"For headless subprocess (no herdr): `antigravity-delegate` skill via delegate.sh."* — no `antigravity-delegate` skill exists anywhere and no `delegate.sh` exists (only `delegate-herdr.sh` at line 104, which stays). Owner-clean: remove the dead reference, don't invent the file.
- **D2** — add to `.gitignore`: `.no-mistakes-document-result.json`, `.review-result.json`, `review_findings.json` (prevent Tier A7 recurrence).

## Local-only (gitignored — `rm` on the seat, NOT part of the git delivery)

- `docs/ultrareview/*`, `.github/coverage/*.out` — untracked runtime debris.

---

## Disclosed gaps / weak keeps (Human may extend scope)

- **Go `*_test.go` not deep-swept for tombstone tests.** Probe: `LegacyTaskAuthoritySymbols` (named "vestigial, inert" in the deleted 124KB plan) does **not** exist anywhere in the tree — that tombstone is already gone. No other obvious `Legacy*`/`Vestigial*` test symbol found. Proposing this pass does **not** delete any Go test; a dedicated test-debt sweep can be a follow-up if the Human wants "dọn test" to reach unit tests.
- **Weak keeps (kept unless Human rules "không liên quan"):** `docs/agents/{domain,issue-tracker,triage-labels}.md`, `docs/self-hosting.md` — munsu-current agent/config docs, no CI pin; left KEEP.
- **Latent:** `AGENTS.md:50` cites `docs/plans` as a path; if `docs/plans/` ever empties (every plan deleted), that citation breaks. This delivery keeps this plan file in `docs/plans/`, so the dir stays nonempty and the gate stays green. Flagged for whoever later deletes the last plan.

---

## Partition (after approval) — split by coupled cluster, not directory

- **Engineer-1 (docs/plans):** A1–A5 + (if B-delete) residual-roadmap + its 4 ADR link rewrites.
- **Engineer-2 (firstmate):** A6 doc + its 28 `citations.allow` waiver rows.
- **Engineer-3 (proof + hygiene):** Tier C (mutation cluster + ADR-0017 3-line reconciliation) + A7 + D1 + D2.

Disjoint owned paths; each cluster's delete + its own compensating edit ship together so no scope reddens another's gate mid-flight. Lead is sole committer; local-only `rm` done by the Lead outside git.

## Verification (proportionate, per refresh-standard §Evidence)

`.github/scripts/citations.sh check` (must be exit 0) + `go build ./...` + `go vet ./...` + `gofmt -l .` + `go test ./...`, on the quiet head, then Reviewer (agy) on the exact SHA through the no-mistakes gate.
