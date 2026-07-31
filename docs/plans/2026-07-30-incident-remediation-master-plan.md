# Incident Remediation Master Implementation Plan

* **Date:** 2026-07-30
* **Status:** Approved design; implementation not started
* **Sources:** incident report, [ADR-0003](../adr/0003-config-deepening-typed-documents-and-project-overlay.md), [ADR-0004](../adr/0004-authoritative-task-lifecycle-delivery-and-projections.md), [ADR-0005](../adr/0005-runtime-bindings-supervision-recovery-and-mutation-fencing.md), [ADR-0006](../adr/0006-state-migration-build-provenance-and-compatibility-gates.md)
* **Delivery mode:** no-mistakes; dependency-first with immediate outage containment

## Goal and success criteria

Implement the accepted configuration and incident-remediation architecture so one General safely supervises many one-project Captains, project-scoped multi-harness Soldiers, authoritative task/delivery lifecycle, backend-neutral supervision, bounded recovery, explicit migrations, and typed AXI outcomes.

The program is complete when:

1. Every traceability entry below has landed acceptance tests.
2. No current-state mutation depends on primary-first task lookup, mutable endpoint/worktree metadata, append-only status as authority, or implicit legacy reads.
3. Canonical Pi integration loads once; deterministic startup failures preserve diagnostics and stop retrying.
4. Handoff → brief → spawn → delivered → authorized merge → Issue reconciliation → retirement works without duplicate registration or stale projections.
5. Herdr and other adapters report typed liveness consistently; per-home watchers supervise their own state.
6. Config and state hard-cutover migrations have rehearsed plan/apply paths and rollback evidence.
7. `go build ./...`, `go vet ./...`, and `go test ./...` pass, followed by the no-mistakes gate.

## Program rules

* Migrations precede removal of old readers/writers, but no normal runtime path permanently dual-reads or dual-writes.
* Every mutation names exact home, owner, Task Generation, and expected revision/digest where applicable.
* Every new transaction returns typed AXI data and typed errors with retry disposition and exact remediation.
* Pure domain functions and adapter contract tests are primary test surfaces; CLI tests verify composition and output contracts.
* Temporary compatibility recognition must have an explicit removal phase and test.
* Do not acknowledge or act on pending envelope `70f33ebfc28c6fb5092a8210a687fb8e` as part of this implementation plan.

## Phase 0 — Immediate deterministic-outage containment

### 0.1 Canonical Pi integration only

**Decisions:** C1.

**Change:**

* Remove alias-writing loop from `internal/cli/captain_integration_port.go`.
* Keep only `munsu-pi-integration.ts` in `captainPiExtensionNames` in `internal/fleet/captain_captain.go`.
* Reuse existing `munsu.integrate/v1`, version, digest, and ownership marker; do not introduce another marker.
* Seed, config-push, ensure, and recovery must never recreate the two aliases.

**Tests:** launch args contain at most one extension registering `munsu_wake_resolve`; canonical-only seed/reseed/recover is idempotent; existing aliases are not loaded and are removed only through an explicit owned-artifact cleanup path. Verify Grok's four distinct hooks remain unchanged.

**Failure/rollback:** launch fails closed if canonical integration is missing or digest-invalid. Rollback is source revert; no state migration.

**Acceptance:** Pi Captain reaches stable alive with canonical integration; duplicate alias reproduction is impossible through normal provisioning.

## Phase 1 — Shared domain, schema, evidence, and transaction foundations

### 1.1 Typed identities and revisions

**Decisions:** B1, B2, C7, C10, C11, C12, C15, C18.

Introduce shared typed identities for `TaskID + TaskGeneration`, owner/home, provider identity, endpoint/worktree lease, operation attempt, Decision/Hold, schema version, evidence reference, and expected revision. Extend `internal/domain` error/result conventions rather than returning ambiguous strings.

**Primary seams:** `internal/domain/`, `internal/home/generation.go`, `internal/home/taskmeta.go`.

**Tests:** pure validation, generation monotonicity, identity equality, expected-revision/CAS conflict, retry disposition, JSON round-trip, corrupt/unknown schema refusal.

### 1.2 Durable transaction/evidence store

Provide atomic write, lock/CAS, receipt, staged install, and append-only audit primitives used by handoff, migration, delivery, recovery, and watcher leases. Define retention references so unresolved evidence cannot be garbage-collected.

**Failure/rollback:** interrupted writes leave either old authority or a verifiable staged/receipt state, never a partially accepted current record.

**AXI:** every transaction reports operation ID, target identity/generation, before/after revision, outcome, retry, and evidence refs.

### 1.3 Migration framework

Implement explicit single-home migration registration, detect/plan/apply/receipt interfaces and immutable fleet plan format. No specific config/task/wake migration activates until its schema converter is tested.

**Acceptance:** crash-injection tests at every stage prove source preservation and idempotent resume.

## Phase 2 — Config deepening and project-scoped dispatch

**Depends on:** Phase 1 migration, schema, transaction, and evidence primitives.

### 2.1 Typed JSON documents and pure resolution

**Decisions:** Config deepening.

Implement:

* `config/base.json`
* `data/captains.json`
* `data/projects.json`
* independent `schemaVersion` values;
* one Captain per project, at most one owning Captain;
* `resolve(base, projectOverlay, boundaryOverrides)`;
* `digest(base, projectOverlay)`;
* frozen `LoadResolvedSnapshot(home, project)`.

Deepen `internal/config`; retain lifecycle mutation in `internal/fleet` and profile matching in `internal/harness`.

**Primary seams:** `internal/config/config.go`, `internal/config/snapshot.go`, `internal/cli/config_dispatch_cmd.go`, `internal/cli/project_cmd.go`, `internal/fleet/project_project.go`, `internal/fleet/captain_configreread.go`, `internal/harness/dispatch.go`.

**Tests:** overlay precedence, environment-at-boundary precedence, distinct project/harness profiles, Captain profile fallback, deterministic digest, targeted nudge, frozen operation snapshot, registry uniqueness.

### 2.2 Explicit config hard cutover

Add `munsu config migrate --home <exact-home>` on the Phase-1 migration primitive and fleet plan/apply composition. Ingest legacy file-per-key config, markdown registries, and `soldier-dispatch.json`; validate all input; stage JSON; archive legacy with timestamp/digest; write receipt. Load paths detect and return `config not migrated` with exact command.

**Clean-break removals:** legacy config readers/writers, markdown registry mutation, direct environment reads inside core config, auto-triggered migration.

**Acceptance:** migration is idempotent, archives rather than deletes, rejects conflicting Captain/project binding, and supports different project dispatch profiles concurrently.

## Phase 3 — Authoritative Task aggregate, ownership, readiness, and context

**Depends on:** Phase 1 domain/transaction primitives and Phase 2 authoritative project identity/config resolution.

### 3.1 Typed Task repository and conflict migration

**Decisions:** B2, C18 Q1.

Create the ADR-0004 Task aggregate repository. Migrate backlog + `.meta` definition fields into one Task Generation per logical task. Collect all candidate owners before selection. Conflicting active owners or incompatible fields quarantine the task and require explicit reconciliation.

**Primary seams:** `internal/home/taskmeta.go`, `internal/fleet/backlog_domain.go`, `internal/fleet/delivery_resolve.go`, `internal/cli/task_cmd.go`.

**Tests:** one current owner, historical generations, duplicate active owner quarantine, conflicting field refusal, exact scoped correction, destination-ready handoff.

**Clean-break removals:** `task add` as identity creation; primary-first lookup; `.meta` as task authority.

### 3.2 Atomic handoff and readiness projection

Handoff transfers the complete Task aggregate and preserves Task Generation. Readiness is a pure function over phase, dependencies, Dispatch Holds, watcher capability, capacity, migration state, and compatibility requirements.

**Tests:** failure leaves source owner intact; handoff destination can immediately show/render/preflight; held/dependency-blocked/watcher-degraded reasons are distinct.

### 3.3 Backlog CLI clean break

**Decisions:** C2, C19.

* Remove `backlog add --start`; add always queues.
* Make `backlog ready` query-only with no task argument.
* Make `unblock`, `reopen`, and `start` canonical mutations.
* Reopen terminal history through a new Task Generation when required.
* Require explicit `captain:<id>` mutation targets and return typed retry commands for bare Captain matches.

**Primary seam:** `internal/cli/backlog_cmd.go`, backend adapters.

**AXI/exit:** query success is zero; invalid old form is typed validation/non-zero; mutation conflict is non-zero with current phase and remedy.

### 3.4 Dispatch Interpretation and Dispatch Hold

**Decisions:** C17.

Persist directive/dependency divergence evidence and configured autonomy. Add scoped durable holds evaluated by all human/automatic dispatch paths. Release never auto-starts.

**Tests:** #363→#364/#367→#368 parent-spec interpretation; ambiguity requires Decision; pause survives restart and keeps successors queued.

### 3.5 Bounded Context Manifest

**Decisions:** C18 Q2.

Generate revision-bound manifests from author hints plus bounded repository evidence. Include exact seams, nearest tests/helpers, conventions, commands, reasons, confidence, digest, and omitted categories. Enforce entry/token budgets and explicit revisioned expansion.

**Acceptance:** complex-task fixture reaches intended implementation/test seams without broad file dumping; stale digest is surfaced.

## Phase 4 — Delivery lifecycle, merge transaction, fallback, and Issue sync

**Depends on:** Phase 3 Task authority, ownership, lifecycle revision, readiness, and projection foundations.

### 4.1 `delivered` and `PrepareDelivery`

**Decisions:** B1.

Add canonical `delivered` phase. Centralize report material-state validation used by `internal/cli/report_cmd.go` and `internal/orchestrator/uplink_uplink.go`. Soldier supplies evidence; parent executes idempotent `PrepareDelivery`, verifies open PR identity/head/checks, preserves child Task ID and `key=delivery`, updates Task/backlog projection, and emits General wake when merge decision is needed.

**Clean-break:** Ship cannot use `resolved`; stale `.status` cannot override lifecycle.

**Tests:** Soldier evidence → Captain reconciliation → General wake; scout `done`/ReportReady; invalid Ship `resolved`; direct provider evidence projection.

### 4.2 Exact merge authorization

Persist Decision/Hold authorization bound to Task Generation, provider PR identity, and immutable head SHA. Changed head invalidates authorization. External merged truth records `merged_external_unapproved` without inventing approval.

### 4.3 `MergeDelivery` and `RetireTask`

**Decisions:** C7.

Refactor `internal/fleet/delivery_prmerge.go` and `internal/fleet/retirement_task.go` into separately durable transactions with a typed wrapper result. Reconcile provider truth after every mutation response. Persist `MergeAttempt`; `remote_unknown` forbids repeated mutation and schedules only read reconciliation. Cleanup retry resumes retirement only.

**Typed outcomes/exit:** all partial outcomes are non-zero except verified/already merged with successful requested post-actions. Human text must lead with remote truth (`merged=true`, `retired=false`) rather than generic failure.

**Tests:** provider merged but CLI non-zero/unparseable; provider query unavailable; verified-open permits new attempt ID; already merged; metadata failure; cleanup failure; idempotent resume.

### 4.4 Delivery Plan transitions and capability attestations

**Decisions:** C14.

Add requested/effective mode and revisioned transition evidence. Preflight in exact execution context before spawn; revalidate before pipeline submission/PR creation. Parent Decision or exact project pre-authorization controls fallback. `require-no-mistakes` remains non-overridable.

**Primary seams:** `internal/fleet/delivery_preflight.go`, `internal/fleet/delivery_nomistakes.go`, `internal/fleet/spawn_spawn.go`, resolved project config.

**Tests:** absent binary vs unsupported neutralization reason; late capability loss preserves work; authorized no-mistakes→direct-PR updates lifecycle; unauthorized fallback blocks.

### 4.5 IssueLink and closure reconciliation

**Decisions:** C9.

Add explicit Issue links to Task Definition. Render canonical PR body fragment. `PrepareDelivery` verifies required closing references and provider/repository semantics. Post-merge reconcile Issue state with eventual-consistency retry. Add idempotent `delivery issue-reconcile` repair with audit comment and manual Decision gate.

**Tests:** closing keyword; parent/related not closed; unexpectedly open Issue; pending closure; already-closed repair; manual-close authorization.

## Phase 5 — Endpoint liveness, per-home watchers, and bounded recovery

**Depends on:** Phase 1 binding/evidence primitives and Phase 3 authoritative task ownership.

### 5.1 Endpoint Binding and typed observations

**Decisions:** C10 Q1–Q2.

Persist immutable Endpoint Binding at spawn success. Replace bool-only probe interfaces and `PaneAlive && AgentAlive` collapse with typed observations and evidence.

**Primary seams:** `internal/fleet/soldierstate_soldierstate.go`, `internal/fleet/snapshot.go`, `internal/cli/fleet_contract.go`, endpoint adapters, retirement backend.

**Adapter tests:** authoritative absence, startup grace, pane alive/agent absent, timeout, unsupported/incomplete binding, lease mismatch for tmux/Herdr/zellij/cmux/orca.

### 5.2 State-specific recovery

**Decisions:** C10 Q3, C12.

Unify watcher, Captain recovery, Soldier recovery, and teardown matrices. Auto-relaunch only verified dead endpoints. Persist recovery series, budgets, circuit state, normalized signature, and exact successor binding. Require stable alive for success.

### 5.3 Diagnostic bundles

Capture bounded stdout/stderr from process launch, redact before owner-only atomic persistence, store safe command shape and executable identity, and reference from Recovery Attempt. Preserve unresolved evidence across cleanup.

**Tests:** secret redaction, truncation, redaction failure metadata-only path, deterministic signature normalization, pane cleanup ordering, changed input digest opens new series.

### 5.4 Per-home watcher leases and degraded mode

**Decisions:** C16.

One watcher owns each home. General observes Captain Watcher Leases and requests recovery through control plane. Handoff/start/spawn require healthy watcher. Unhealthy watcher blocks new dispatch but allows repair, reconciliation, authorized delivery, holds, and evidence-preserving teardown.

**Primary seams:** `internal/cli/watch_cmd.go`, `internal/orchestrator/supervision_watcher.go`, Captain converge/recover composition.

**AXI clean-up:** watcher start failure is top-level error; fleet view separates agent endpoint and watcher health.

**Tests:** first task requires watcher; watcher death degrades existing task without killing it; independent General attention; receipt drain before dispatch reopen; one live lease per home generation.

## Phase 6 — Worktree fencing and launch artifact ownership

**Depends on:** Phase 1 lease/binding primitives and Phase 5 endpoint lifecycle/recovery semantics.

### 6.1 Worktree Binding

**Decisions:** C11.

Persist repository/path/GitDir/CommonDir/head/lease identity before task mutation. Spawn, delivery, teardown, and local merge consume the binding, not ambient cwd.

### 6.2 Managed Git wrapper and capability tiers

Prepend a task-bound wrapper in managed sessions. Classify read versus mutation, resolve aliases and `-C`, verify binding and expected pre-state, and enforce worktree/history/cleanup/push/force-push capabilities. Internal munsu Git operations receive explicit scoped capabilities.

**Tests:** primary checkout refusal; wrong worktree/recycled lease; normal task commit; exact task-ref push; unauthorized reset/clean/force push; authorized amendment with expected head and force-with-lease; direct absolute-path bypass is documented, not claimed prevented.

### 6.3 Versioned runtime artifact manifest

**Decisions:** C8.

Stop writing `.soldier-md`; canonicalize `.soldier-brief.md`. Extend Soldier envelope with artifact path/digest/disposable manifest. Teardown permits only untracked matching artifacts; modified/tracked/unlisted files block. Harness adapters contribute verified entries.

**Primary seams:** `internal/fleet/spawn_runner.go`, `internal/fleet/soldier_envelope.go`, `internal/fleet/soldier_charter.go`, `internal/fleet/retirement_task.go`, `.gitignore`.

**Temporary migration:** recognize `.soldier-md` only when digest equals envelope Brief SHA. Record usage so Phase 8 can remove recognition.

## Phase 7 — Wake migration, build provenance, and compatibility rollout

**Depends on:** Phase 1 migration/provenance primitives; operation gates also consume schemas delivered by Phases 2–6.

### 7.1 Wake-resolution migration

**Decisions:** C15.

Implement file-to-directory converter in the Phase-1 migration framework. Parse all tab records, validate duplicates/states/identity, stage JSON, verify count/digest, archive source, install, and receipt. `ResolveWake` detects legacy and returns exact scoped migration command; it does not mutate layout.

**Primary seam:** `internal/home/wake_resolve.go`, new state migration CLI/domain.

**Tests:** no data loss, corrupt line untouched, duplicate handling, crash stages, already migrated, explicit General vs Captain home scope, fleet partial success/resume.

### 7.2 Build provenance and skew diagnostics

**Decisions:** C13 Q1.

Embed full provenance and executable digest/path. Observe running/PATH/install/source/General/Captain/watcher/integration identities separately. Emit typed skew and exact remediation in session-start/doctor.

**Primary seams:** `internal/cli/selfupdate_update.go`, `internal/cli/doctor_capability.go`, session diagnostics, watcher identity.

**Tests:** PATH shadowing, same version/different binary, source ahead/behind/dirty, packaged no-Git install, watcher mismatch, integration mismatch.

### 7.3 Operation-specific compatibility gates

**Decisions:** C13 Q2.

Declare requirements for task mutation, spawn, Captain launch/recovery, delivery, migration, self-update, and teardown. Keep diagnostics/read paths available. Never override incompatible decoding/corrupt schema; model authorized overridable exceptions as durable Decisions.

**Tests:** compatible mixed versions allowed; exact integration mismatch blocks Captain launch; lifecycle schema mismatch blocks task mutation; delivery does not require exact source commit; path-shadowed blocks misleading self-update/recovery.

## Phase 8 — End-to-end convergence, rollout, and clean-break removals

**Depends on:** all earlier phases and their migration/activation evidence.

### 8.1 Incident replay suite

Replay the full reported sequence with controlled adapters:

1. queued General tasks and atomic handoff;
2. Captain dependency reinterpretation;
3. healthy watcher prerequisite;
4. multi-harness project spawn in isolated worktrees;
5. no-mistakes capability failure and authorized direct-PR transition;
6. `delivered` reconciliation and General merge Decision;
7. provider mutation false-negative/unknown reconciliation;
8. Issue closure verification;
9. artifact-clean retirement;
10. paused successors remain queued;
11. stale projection cannot override lifecycle;
12. deterministic Captain integration failure preserves stderr and opens retry circuit.

### 8.2 Migration and mixed-version rehearsal

Test fresh install, fully legacy home, partially migrated fleet, corrupt Captain home, source-changed plan, old watcher/new CLI, new watcher/old Captain, packaged install, and rollback from staged-but-not-installed migration.

### 8.3 Remove temporary compatibility paths

After activation evidence confirms rollout:

* remove legacy `.soldier-md` recognition;
* remove old task/meta/backlog creation/read paths;
* remove old wake-resolution file parsing from migration builds when support window closes;
* remove deprecated Pi alias names and obsolete tests;
* remove mutating `backlog ready` and `add --start` compatibility shims;
* remove bool liveness interfaces and mutable endpoint authority;
* remove auto/fallback backend selection for bound endpoints.

### 8.4 Final gates

Run `gofmt`, `go build ./...`, `go vet ./...`, `go test ./...`, focused race/crash tests, CLI contract snapshots, migration dry runs, secret scan, and no-mistakes. Review every changed line against this plan and ADRs before delivery.

## Implementation ledger

This ledger is the completion contract for each numbered work item. “Typed” means structured AXI data plus non-zero exit for validation, conflict, unsafe, unavailable, corrupt, or partial-failure outcomes unless the row states otherwise.

| Item | Decisions | Depends on | Primary seams | Schema/migration impact | Failure/resume | AXI/exit | Acceptance test | Clean-break removal |
|---|---|---|---|---|---|---|---|---|
| 0.1 | C1 | none | Captain integration writer/launch args | none | missing/digest-invalid canonical file blocks launch | typed integration error, non-zero | one registration across seed/recover | alias writes/load entries |
| 1.1 | B1/B2, C7/C10/C11/C12/C15/C18 | 0.1 only operationally | `internal/domain`, generation/task identity | new shared identity/version types | invalid/unknown identity fails before mutation | typed validation/conflict | round-trip and CAS conflict tests | stringly/implicit identities |
| 1.2 | B1/B2, C7/C12 | 1.1 | home/domain durable stores | transaction receipts/evidence refs | atomic old-or-new state; idempotent resume | operation/revision/evidence fields | crash injection at write stages | ad hoc partial writes |
| 1.3 | C15, config migration | 1.1–1.2 | migration domain/CLI | migration plan/receipt schemas | source preserved; staged resume | per-home typed outcomes; aggregate non-zero on remaining defects | staged crash/resume | hidden migration mutation |
| 2.1 | Config deepening | 1.x | config/project/captain/harness seams | base/captains/projects JSON schemas | unsupported schema blocks affected operation | typed scope/digest/snapshot | overlay/digest/frozen snapshot | file-per-key live resolution |
| 2.2 | Config deepening, C15 policy | 1.3, 2.1 | config migrate/load paths | hard-cutover converter/receipt | archive and resume; corrupt input untouched | migration-required/non-zero with exact command | idempotent multi-home plan/apply | legacy config/Markdown writers/readers |
| 3.1 | B2, C18 | 1.x, 2.x | taskmeta/backlog/resolver/task CLI | Task aggregate + migration | duplicates quarantine; source owner retained | ambiguity/corrupt typed errors | conflict migration/current owner | `task add` authority; primary-first lookup |
| 3.2 | B2, C17/C18 | 3.1 | handoff/readiness domain | ownership transfer/revision | failed transfer leaves source intact | typed readiness reasons | handoff immediately briefable/spawnable | synchronized duplicate stores |
| 3.3 | C2/C19 | 3.1–3.2 | backlog CLI/adapters | reopen generation event | conflicts preserve current phase | old forms non-zero with exact correction | ready query/unblock/reopen/start | add `--start`; mutating `ready` |
| 3.4 | C17 | 3.1–3.2 | readiness/Decision-Hold | interpretation/hold schemas | stale dependency digest invalidates; hold persists | typed held/divergent/decision-needed | parent-child reinterpretation and restart pause | prose-only pause/interpretation |
| 3.5 | C18 | 3.1, 2.1 | brief/context planner | ContextManifest revisions | stale entries marked; expansion appends | typed manifest/budget result | bounded intended seams | broad static discovery requirement |
| 4.1 | B1/B2 | 3.x | report/uplink/delivery prep | delivered phase/evidence | verification failure leaves prior phase | typed missing evidence; non-zero | Soldier→Captain→General wake | Ship `resolved`; status authority |
| 4.2 | B1/C7 | 4.1 | Decision/Hold/delivery identity | merge authorization record | changed head invalidates | typed unauthorized/conflict | exact generation/head binding | lifecycle-as-approval |
| 4.3 | C7 | 4.1–4.2, 1.2 | prmerge/retirement | MergeAttempt/composite result | remote unknown read-only reconcile; retirement-only resume | typed partial outcomes, partial non-zero | false-negative/unknown/already merged | binary `error` semantics; merge rerun on cleanup retry |
| 4.4 | C14 | 2.1, 3.1, 4.1 | preflight/no-mistakes/spawn | DeliveryPlan transition/attestation | preserve work and block/transition explicitly | typed reason/capability/authorization | late and early fallback cases | silent fallback/relabel |
| 4.5 | C9 | 3.1, 4.1–4.3 | Task definition/provider reconcile | IssueLink/outcome schema | eventual read retry; manual policy waits Decision | typed closure partial outcomes | close/related/parent/repair | task-ID Issue inference |
| 5.1 | C10 | 1.1–1.2, 3.1 | soldierstate/snapshot/fleet contract/adapters | EndpointBinding/Observation | unknown never dead; stale quarantines | typed state/evidence/retry | cross-adapter contract suite | bool liveness; backend re-selection |
| 5.2 | C10/C12 | 5.1 | recovery/watcher/teardown | recovery series/circuit | state-specific bounded resume | typed action/budget/circuit | only verified dead relaunch | relaunch-all/non-durable budget |
| 5.3 | C12 | 1.2, 5.2 | launch process/diagnostics | diagnostic bundle/ref | redaction failure metadata-only; evidence before cleanup | typed capture/truncation/redaction | secret/truncation/signature tests | ephemeral stderr only |
| 5.4 | C16 | 5.1–5.3, 3.2 | watch CLI/orchestrator/Captain control | WatcherLease/degraded capability | repair/drain before dispatch reopen | startup failure top-level non-zero | per-home ownership/degraded dispatch | success-with-failed-data; cross-home mutation |
| 6.1 | C11 | 1.1–1.2, 5.1 | spawn/worktree/delivery/teardown | WorktreeBinding | wrong/recycled lease blocks | typed binding conflict | exact repository/path/head identity | ambient cwd authority |
| 6.2 | C11 | 6.1 | Git wrapper/internal Git ports | capability grants/audit | CAS mismatch before mutation | typed refusal with authorized workflow | primary/wrong-ref/destructive cases | unrestricted managed-session Git mutation |
| 6.3 | C8 | 6.1, 5.2 | spawn runner/envelope/retirement | envelope artifact manifest v2 | digest mismatch blocks; matching legacy cleanup resumes | typed dirty/owned/legacy result | canonical/matching/modified tests | `.soldier-md` writer; filename allowlist |
| 7.1 | C15 | 1.3 | wake resolve/state migrate | wake file→directory receipt | corrupt source untouched; per-home resume | typed plan/apply outcomes | no-loss/crash/partial fleet | runtime lazy migration/dual-read |
| 7.2 | C13 | 1.1–1.2 | selfupdate/doctor/session/watcher identity | BuildProvenance/RuntimeIdentity | unverifiable stays diagnosable | read diagnostics zero with typed warnings | PATH/dirty/packaged/watcher skew | version-string-only identity |
| 7.3 | C13 | 7.2, schemas 2–6 | compatibility declarations/composition roots | operation requirement records | affected mutation blocked; reads remain | typed missing requirement/remedy | compatible mixed/incompatible exact gates | warning-only/global exact-match policy |
| 8.1 | all | 0–7 | E2E fixtures/adapters | none beyond prior phases | fixture resumes each durable boundary | scenario-specific typed assertions | full incident replay | obsolete incident workarounds |
| 8.2 | config/C15/C13 | 1–7 | migration/release fixtures | activation evidence | partial/corrupt/mixed resume | aggregate rollout result | legacy/partial/mixed-version matrix | unverified rollout assumptions |
| 8.3 | all clean breaks | 8.1–8.2 evidence | legacy readers/writers/shims | support-window activation | removal only after evidence; revert release if activation fails | removed forms return explicit unsupported/correction | grep + negative regression suite | all listed temporary compatibility paths |
| 8.4 | all | 8.1–8.3 | repository-wide | final schema/docs alignment | failed gate blocks delivery | no-mistakes/CI non-zero on any defect | build/vet/test/race/contracts/migrations | none; final verification |

## Traceability matrix

| Decision/workstream | Implementation phase | Required acceptance evidence |
|---|---:|---|
| Config deepening | 2, 7, 8 | distinct project overlays/harnesses; resolved snapshots/digests; explicit JSON migration |
| C1 duplicate Pi integration | 0 | one canonical loaded extension; seed/recover cannot recreate aliases |
| B1 delivered lifecycle | 4 | open green PR → delivered → General wake; Ship resolved rejected |
| B2 authoritative projection/owner resolution | 3, 4, 8 | collect-all ambiguity failure; lifecycle supersedes status; quarantined duplicates |
| C2 CLI ergonomics/scoped Captain mutation | 3 | no add --start; exact `captain:<id>` correction |
| C7 merge false-negative/transaction boundary | 4 | typed partial outcomes; remote_unknown read-only reconcile; retirement resume |
| C8 teardown cleanliness | 6, 8 | canonical brief; manifest digest; legacy match cleanup; modified file refusal |
| C9 GitHub Issue closure | 4 | explicit IssueLink; delivered body verification; merged_issue_open repair |
| C10 backend-aware liveness | 5 | typed Herdr observations; unresponsive not dead; only verified dead relaunch |
| C11 worktree mutation safeguards | 6 | primary checkout mutation refused; capability/CAS enforcement |
| C12 retry limits/stderr persistence | 5 | deterministic circuit; redacted durable diagnostics; stable-alive success |
| C13 binary/source skew | 7 | provenance/skew observations; operation-specific blocking |
| C14 no-mistakes fallback | 4 | context attestation; authorized mode transition; no silent fallback |
| C15 wake state migration | 1, 7, 8 | explicit plan/apply; archive/receipt; corrupt source untouched |
| C16 Captain watcher coverage | 5 | per-home lease; degraded dispatch block; pending reconciliation before reopen |
| C17 dependency reinterpretation/pause | 3 | durable interpretation and hold; pause survives restart |
| C18 complete task/context representation | 3 | handoff→brief→spawn without task add; bounded Context Manifest |
| C19 ready/reopen vocabulary | 3 | ready query-only; unblock/reopen/start typed semantics |

Any row without a passing acceptance artifact blocks program completion.

## Critical implementation seam index

* `internal/cli/captain_integration_port.go` — canonical integration writer.
* `internal/fleet/captain_captain.go` — Captain launch/recovery/converge/watcher composition.
* `internal/home/taskmeta.go` — legacy projection and Task migration source.
* `internal/fleet/delivery_resolve.go` — replace primary-first ownership lookup.
* `internal/fleet/delivery_prmerge.go` — typed merge transaction and reconciliation.
* `internal/fleet/retirement_task.go` — retirement transaction and artifact verification.
* `internal/fleet/spawn_runner.go` — bindings, canonical brief, wrapper, diagnostics.
* `internal/fleet/soldier_envelope.go` — v2 artifact manifest and integrity.
* `internal/fleet/soldierstate_soldierstate.go` — typed endpoint observation.
* `internal/fleet/snapshot.go` — authoritative lifecycle/watcher/endpoint projection.
* `internal/cli/fleet_contract.go` — stop bool collapse and backend re-selection.
* `internal/home/wake_resolve.go` — detect-only runtime and explicit converter.
* `internal/cli/backlog_cmd.go` — query/mutation clean break.
* `internal/cli/selfupdate_update.go` — provenance and exact executable identity.
* `internal/cli/doctor_capability.go` — typed skew and remediation.

## Explicit non-goals

* No new terminal backend is introduced.
* The Git wrapper is not claimed as a security sandbox.
* Parent/epic Issue closure is never inferred.
* Read-only diagnostics are not blocked merely because source and binary commits differ.
* This plan does not implement or authorize the pending PR merge request contained in acknowledged envelope `2bdf4edb222af871595acb3ee94ec054`.
