# Owner-Clean Residual Roadmap (ADR-0003/0004/0005/0008)

* **Date:** 2026-09-03
* **Status:** T0 landed; G5 resolved. Remaining slices T1–T3 gated by open decision gates G1–G4.
* **Sources:** ADR-0003, ADR-0004, ADR-0005, ADR-0008; supersessions in ADR-0011/0013/0015/0020/0021/0022; prior [owner-clean cutover program](2026-08-03-owner-clean-cutover-program.md)
* **Delivery mode:** no-mistakes (every slice = one issue on one branch through the gate; push/PR/merge are Human gates)

## Finding

The owner-clean target (ADR-0008, which supersedes 0003–0007 on conflict) is **substantially shipped**. The 11-package topology, `task` sole CLI noun, contract render seam (#416), contract repo (#417) and architecture-policy activation gate (#418) are all done and enforced by tests; the deleted packages (`configmigration`, `taskauthorityfs`, `taskauthority/storecontract`) are gone; ADR-0004 §5, §3, §6 and the migration consequences are retired/superseded, not open.

The genuinely-remaining engineering is concentrated in **ADR-0005 §4** — the durable recovery-series/circuit and redacted launch-diagnostic machinery — plus a re-scope of **§3**. Note: §3 is *not* greenfield: Captain-side recovery already exists (`internal/fleet/captain_recover.go` `RecoverTransaction.stepRelaunch`/`stepNudgeRetry`, `relaunchGuard*` TTL guard, nudge markers). What is missing is (a) the durable recovery-series/circuit keyed by failure-signature and the redacted `LaunchDiagnostic` bundle (§4 — zero hits in tree), and (b) unified state-specific coverage extended to soldier endpoints, not only the Captain matrix (§3 — verify before building). The 0005 audit's "§3 absent" verdict was overstated (it cited `nudgeTracker`/`ResolveRecovery`, which do not exist in the tree). Everything else remaining is small: config CLI wiring, one schema-version reset, two one-live-contract cleanups, one legacy-path removal, and a set of retire-or-build decision gates.

## Per-ADR status (audited against current tree)

| ADR | Shipped | Remaining (build) | Remaining (removal) | Superseded / retired |
|---|---|---|---|---|
| 0003 Config | typed `base.json`, project overlay, 1:1 binding, pure resolve/digest, published-snapshot push, per-project digest+nudge | `project/captain config get/set` CLI; scalar file-per-key → `base.json` consolidation | — (`config migrate` already deleted) | §9 migration (0008) |
| 0004 Task lifecycle | Task aggregate {ID,Gen,Owner,Def,Lifecycle}, atomic handoff, head-bound merge authorization, DispatchHold, `task` noun | §9 ContextManifest (only clean build gap) | domain.go dual-key aliasing + duplicate `DeliveryState` type | §3 delivered→auth (0008), §5 IssueLink (0011), §6 (0022) |
| 0005 Runtime | immutable Endpoint/Worktree bindings, typed 7-state observation (0021), per-home watcher lease + degraded mode, `.soldier-brief.md`+manifest, **Captain recovery (relaunch/nudge/relaunch-guard)** | **§4 recovery series+circuit (failure-signature)**, **§4 LaunchDiagnostic bundle**; §3 unified/soldier-endpoint coverage (verify vs Captain matrix); §5 General→Captain relay (verify) | legacy `.soldier-md` recognition path | — |
| 0008 Owner-clean | topology, `task` noun, render seam, contract repo, arch-policy gate, package deletions | — | reset `munsu.orchestration/v2` → v1 (§11) | — |

## Decision gates (Human — resolve before the dependent slice runs)

* **G1 — ADR-0004 §9 ContextManifest:** build a bounded, revision-bound context manifest on the aggregate, or retire it via a follow-up ADR. Nothing in 0009–0022 retires it; it is the only clean build gap in 0004.
* **G2 — ADR-0004 §7 DispatchInterpretation:** mark subsumed by DispatchHold + head-bound DeliveryAuthorization (ADR-0008), or build the distinct interpretation record. Do not build blind.
* **G3 — ADR-0005 §6 Git fencing:** the wrapper exists but as a *pretool-check hook* (unhooked harnesses are unfenced) and the §6 6-tier capability ladder was collapsed to a flat Ship allowlist by #414B. Ratify the flat allowlist as the owner-clean target (mark the ladder superseded), or treat the tier model / harness-agnostic enforcement as real work. Security-adjacent → hard gate.
* **G4 — ADR-0003 §10 env-override boundary:** wire `MUNSU_*_OVERRIDE` → `BoundaryOverrides` at the CLI seam, or delete the dormant (always-empty) `BoundaryOverrides` plumbing as speculative machinery.
* **G5 — ADR-0008 §13 delivery shape (RESOLVED in T0):** §13 previously mandated "one coherent cutover on one branch… no supported phases," but the program was delivered incrementally (gate-per-issue, #402–#418). Amended in T0 to record incremental delivery as the accepted shape; see ADR-0008 §13.

## Slices (dependency order)

### T0 — Docs reconciliation (landed)
Flipped stale `Status: Accepted; implementation pending` headers on ADR-0003/0004/0005 to reflect shipped state; flipped ADR-0008 header (all of #402–#418 closed); noted the §3/§5/§6 supersessions in 0004/0005; amended ADR-0008 §13 to record incremental delivery as the accepted shape (resolving **G5**); removed the completed ADR-0021 pipeline artifacts `tasks/plan.md` + `tasks/todo.md`; and landed this roadmap. ADR-0008 §2 was left at its accepted constraint (`.meta`/`.status` removed) — retention of home projections is an owner decision and was not changed in T0. **Validation:** `citations.sh check` green; `go build/vet/test ./...` unaffected.

### T1 — Owner-clean removals (no deps, run in parallel)
* **R1** — Delete legacy `.soldier-md` migration path: `soldier_manifest.go` (`LegacyBriefMigrationPolicy`/`CheckLegacyBriefMigration`), the two `spawn_runner.go` set-lines, the `retirement_task.go` branch, the `guard_burn_down_*legacy*` tests + `soldier_contract_e2e_test.go` case; then sweep dangling rows in `.github/{uncovered-guards.baseline,deadcode.allow,citations.allow}`. **Validation:** guards/deadcode/citations lanes green both ways; full suite green.
* **R2** — Reset `munsu.orchestration/v2` → v1 (ADR-0008 §11): coordinated change across the CLI response envelope (`internal/cli/contract_model.go:9`) and the pi handshake parser (`internal/bootstrap/integration_pi.go:51,69`). Both ends are munsu-authored (the pi extension JS is seeded by munsu), so the cost is reseeding Captain integrations, not breaking a third party. Caveat: `/v2` was set deliberately by `1b0257b2 refactor(cli): absorb AXI response contracts` — confirm with the owner whether §11 wants a reset-to-v1 or a §11 note ratifying the response-contract version. **Validation:** pi handshake test green on the chosen id; no stale `orchestration/v2` survives a reset.
* **R3** — One-live-contract cleanup in `internal/domain/domain.go`: drop the `pr_base`/`pr_base_ref` + `pr_head`/`pr_head_sha` dual-key aliasing and fallback reads; consolidate the duplicate `DeliveryState`/`MetaDeliveryState` type to the single live `fleet.DeliveryState`. **Validation:** full suite green; grep confirms one definition, no alias writes.

### T2 — Config CLI completion (ADR-0003, small build; after T0)
* **C1** — Wire `munsu project config get/set` and `munsu captain config get/set` to the existing (currently CLI-unreachable) `StoreProjectOverlay`/`LoadProjectOverlay` + captain profile store. **Validation:** round-trip test; overlay reflected in resolved snapshot.
* **C2** — Consolidate scalar file-per-key config (`soldier-harness`, `model`, `model-allowlist`, `wake-delivery-mode`, `afk-*`) onto `base.json`; retire `config.Get/Set`. Keep bootstrap-identity files (`parent-home`, `install-root`) flat by explicit decision. Touches `internal/harness`, `internal/orchestrator`. **Validation:** full suite green; no live `config.Get`/`config.Set` on consolidated keys.

### T3 — Supervision recovery + diagnostics (ADR-0005 — the substantive stream)
Prerequisite: a focused re-scope pass, since Captain recovery (`captain_recover.go` relaunch/nudge/relaunch-guard) already exists. Confirm exactly which of §3/§4 is unbuilt before staffing.
* **S1** — §3 verify + extend: confirm the incident invariant (no duplicate panes after deterministic failure) holds through the existing `RecoverTransaction`, then extend the state-specific matrix to soldier endpoints if the audit shows only Captain coverage. **Validation:** relaunch only on verified-dead; no duplicate endpoint; nudge bounded — across Captain *and* soldier endpoints.
* **S2** — §4 durable recovery series + circuit breaker keyed by endpoint-generation + launch-input digest + failure signature, with backoff and circuit-open on repeated identical deterministic failure (reuse ADR-0019 store). Upgrades today's TTL relaunch-guard to a real signature-keyed series. Deps **S1**. **Validation:** circuit opens on repeated identical failure; series survives restart.
* **S3** — §4 `LaunchDiagnostic` redacted bundle (stdout/stderr tails, exit/signal, timing, endpoint evidence, owner-only atomic write) — genuinely absent today. Parallel to **S2**. **Validation:** secret redaction; truncation; metadata-only on redaction failure.
* **S4** — §5 verify/complete the General→Captain `WatcherLease` observation+recovery-request relay (Captain-side degraded mode already shipped). **Validation:** General observes typed Captain lease health and requests recovery through the control plane.

## Critical path

T0 (G5 resolved) → then T1/T2 in parallel; T3 is the substantive stream and gates on nothing new (S1→S2, S3 parallel, S4 independent). G1/G2/G3/G4 gate their own slices only. R2 is the highest-risk single change (external consumer). The heaviest real engineering is T3.

## Human gates

Every push, PR mutation, merge, and deploy. R2, G3, and T3 additionally require a design decision recorded before implementation (coordinated contract replacement / security-adjacent / runtime lifecycle-concurrency).
