# 0003. Config Deepening — Typed Documents, Per-Project Resolved Overlay, and 1:1 Captain–Project Binding

* **Status:** Accepted; substantially implemented — typed `config/base.json`, per-project overlay, 1:1 Captain–Project binding, pure resolve/digest, published-snapshot push, and per-project digest+nudge all landed. Remaining residual work (captain config CLI, scalar consolidation) is tracked in the [owner-clean residual roadmap](../plans/2026-09-03-owner-clean-residual-roadmap.md); the env-override boundary layer was retired (§10). §9 migration is superseded by ADR-0008.
* **Date:** 2026-07-30
* **Extends:** ADR-0002 §8 (config), §11 (migration and activation), §12 (AXI and env)
* **Triggered by:** `munsu-workflow-incident-report-2026-07-30` and the goal of one General supervising many Captains across many projects

## Context & Problem Statement

ADR-0002 §8 assigned `internal/config` ownership of "typed settings, storage, parsing, precedence, defaults, and validation," with long-running modules receiving immutable versioned snapshots published at operation boundaries. The current implementation does not yet deliver that: configuration is one file per key under `config/`, plus two hand-parsed markdown registries (`data/captains.md`, `data/projects.md`), a shared `data/general-shared.md`, and a `soldier-dispatch.json`. The module interface is effectively "read a file by key"; the real complexity — the inheritable set, the multi-token `captain-harness` line, the inheritance digest, and the registry parsing — lives scattered across `internal/fleet`. The interface is not the test surface.

This shape cannot express one General supervising many Captains across many projects:

* A Captain launch resolves its profile from the General home (`harness.CaptainProfileFromHome(parentHome)`), so every Captain shares one `captain-harness`, one `model`, one `default-mode`, and one `require-no-mistakes`. There is no per-project knob.
* Only `soldier-harness`, `soldier-dispatch.json`, and `backlog-backend` are pushed to Captains; the rest are read live from the General home by every Captain and Soldier.
* `Register` is always called with empty scope and project, so `Info{Scope, Project, Added}` are parsed fields that nothing consumes — a shallow structure by the deletion test.
* The inheritance digest covers the entire inherited surface, so any General-side change nudges every Captain.

The incident exposed direct symptoms: the no-mistakes fallback ambiguity hit the whole fleet at once because `default-mode` is single-valued; unscoped task observation selected the wrong home because scope is informational, not authoritative; and the legacy `.wake-resolutions` file-to-directory mishap showed that state migrations must be idempotent and non-destructive.

## Decision Drivers

* Deliver multi-project supervision: different projects under different harness, model, delivery mode, and dispatch profile sets.
* Make the Config Snapshot domain term mean *resolved* configuration, not a raw directory read.
* Concentrate config authority, parsing, resolution, and inheritance behind one deep module, consistent with ADR-0002 §8 and the nine-package topology.
* Preserve ADR-0002 §11 forward-only, no-dual-read migration semantics.
* Keep the interface as the test surface (pure resolution and digest), addressing the incident's test-breakage finding.
* Remain fail-closed and AXI-first.

## Decision

### 1. Typed config documents

Configuration is stored as three typed JSON documents, each with its own `schemaVersion`:

1. `config/base.json` — fleet-wide defaults, including the fleet-default dispatch profile set.
2. `data/captains.json` — typed Captain registry (replaces `data/captains.md`).
3. `data/projects.json` — typed project registry (replaces `data/projects.md`), each project carrying its Project Overlay.

The load path reads JSON only. A document whose `schemaVersion` is not understood fails closed; the migration command is the only place a version is advanced.

### 2. Captain–Project Binding (1:1)

A Captain supervises exactly one project, and a project has at most one owning Captain. A project may exist without a Captain (for General ad-hoc dispatch). The binding is the authoritative scope for task observation and config resolution. A Captain record carries `{ id, home, project, captainProfile }`; a project record carries `{ name, path, mode, config }`.

### 3. Per-project resolved overlay

Soldier/spawn configuration is keyed by **project**, not by Captain, so that both a Captain and a direct General spawn resolve the same overlay. Resolution for a Soldier spawn under project P is `resolve = base ⨂ P.config` (project overlay overrides base; project dispatch profiles fall back to the base dispatch set). The Captain's own launch profile (`captainProfile`) is a separate field on the Captain record and resolves independently, falling back to base for unset fields. There are two typed layers only; the boundary environment-override tier was retired (§10).

### 4. Resolved Snapshot

`LoadResolvedSnapshot(home, project)` freezes `base ⨂ project overlay` for the full duration of an operation, honoring the Config Snapshot domain term. A newer resolved snapshot takes effect only at an operation boundary; a mid-operation read does not observe a concurrent write. Captain launch and Soldier spawn both resolve against the snapshot of their project's scope.

### 5. Resolution authority and push model

The General is the single resolution authority. It resolves a project's configuration and pushes the resolved snapshot down to the owning Captain. The Captain is a static consumer: it applies the resolved snapshot to its Soldiers and does not re-resolve. Resolution exists in one place, matching the charter contract that the General sets Captain configuration.

### 6. Per-project digest and targeted nudge

The config-reread digest is computed per project as `hash(base ⨂ P.config)`. A base change changes every project's digest and nudges every Captain; a change to only one project's overlay nudges only that project's Captain. The Captain's own `captainProfile` is checked at launch time and is excluded from the nudge digest, because it affects the next relaunch, not a running Captain's behavior.

### 7. Module ownership

`internal/config` deepens to own the three documents, resolution, the resolved snapshot, the per-project digest, and migration. `internal/fleet` keeps lifecycle operations (seed, retire, project add/remove, handoff) and mutates the registries through `config`'s write API rather than parsing files itself. `internal/harness` keeps dispatch *matching* (natural-language match on the brief body); `config` resolves the dispatch profile *list* per project, and `harness` matches a brief to a profile. `config` does not import `fleet`; there is no import cycle. The nine-package topology from ADR-0002 is unchanged.

### 8. CLI surface

The operator surface is noun-driven and AXI-first:

* `munsu config get/set <key> <value>` — fleet base (backward compatible with the existing base-scoped commands).
* `munsu project config get/set <name> <key> [value]` — project overlay.
* `munsu captain config get/set <id> <key> [value]` — Captain launch profile.
* `munsu project mode <name>` remains as a thin alias for setting `defaultMode`.

Resolution is internal; operators address the three scopes through their nouns.

### 9. Migration

Migration is a hard cutover, consistent with ADR-0002 §11 (forward-only, no dual-read, no dual-write). `munsu config migrate --home <exact-home>` is a one-shot ingest of legacy file-per-key configuration and the markdown registries into the three JSON documents. After a verified ingest, legacy files are archived (renamed to `*.legacy-<timestamp>-<digest>`), never deleted — the wake-resolution lesson. The General-level `soldier-dispatch.json` is folded into `base.dispatch`. The load path fails closed with an actionable "config not migrated; run `munsu config migrate --home <exact-home>`" message when JSON is absent but legacy is present. `converge`, `session-start`, and doctor detect and report required migration but do not trigger it. Fleet-wide migration uses the explicit plan/apply orchestration defined by ADR-0006, keeping the load path pure and mutation scope auditable.

### 10. Environment overrides at the boundary (retired)

Per ADR-0002 §12, core modules do not read the process environment; the direct `os.LookupEnv` read inside `config.Get` was removed as part of this deepening. The typed boundary-override layer that would translate `MUNSU_<KEY>_OVERRIDE` into resolution input was never wired — every resolver call passed an empty override set. Under the [owner-clean residual roadmap](../plans/2026-09-03-owner-clean-residual-roadmap.md) (G4, 2026-09-03) that dormant plumbing was deleted; resolution is the two typed layers `base ⨂ project overlay` with no environment-override tier. Reintroducing environment overrides is a future decision, not current architecture.

### 11. Test surface

Resolution and digest are pure functions over in-memory structs and are the primary test surface. Required tests:

1. `resolve(base, overlay)` — base-only, overlay-override, dispatch merge with base dispatch, `captainProfile` fallback.
2. `digest(base, overlay)` — deterministic.
3. Migration — idempotent ingest, archive-not-delete, fail-closed on partial or corrupt input.
4. `LoadResolvedSnapshot` — frozen per operation.
5. Per-project nudge targeting — base change nudges all; single-project overlay change nudges only that project.
6. Incident regressions — unscoped task/Captain IDs are scoped by project; multiple Captains resolve to different configuration; `captainProfile` does not enter the nudge digest.

## Alternatives Rejected

### JSONL for the config documents

Rejected. JSONL suits append-only logs; the home and fleet config documents are single structured objects read whole. The existing `state/config-push.log` stays line-oriented.

### Overlay keyed by Captain id alone

Rejected. The General can spawn a Soldier directly for a project, which has no Captain context. Keying the Soldier overlay by project makes Captain-spawned and General-spawned Soldiers resolve identically and unifies the per-project delivery mode already present in the project registry.

### Dual-read / lazy ingest on first load

Rejected. It is a dual runtime path and violates ADR-0002 §11's forward-only, no-dual-read migration semantics. A discrete explicit migration operation keeps the load path pure and the mutation scope auditable.

### A new `internal/fleetconfig` package

Rejected. It would add a tenth package outside ADR-0002's nine-package topology and would require an ADR amendment. Deepening `internal/config` satisfies the same intent within the established topology.

### Fleet-wide inheritance digest

Rejected. With per-project resolution, a fleet-wide digest nudges every Captain on any change. A per-project digest nudges only the affected Captain when only its overlay changes.

### A single global schema version

Rejected. The three documents mutate at different rates (base rarely; registries with lifecycle). Per-document versions avoid coupling unrelated documents and match the existing per-artifact versioning (Activation Record, Captain Charter, provenance).

### Flag-driven CLI (`config set --project` / `--captain`)

Rejected. Scope flags invite the ambiguity and misuse the incident documented. Noun-driven commands keep each scope on its own command and are more discoverable and AXI-validatable.

### Testing through the CLI seam

Rejected. The incident showed CLI seam tests break when orchestration moves behind a deeper module. Pure-function resolution and digest are the stable test surface.

## Consequences

### Positive

* One General can supervise many projects under different harness, model, delivery mode, and dispatch profile sets.
* The Config Snapshot becomes genuinely *resolved* and frozen per operation, matching the domain term.
* A single resolution authority and authoritative Captain–Project binding remove the unscoped-observation ambiguity and the fragmented-config-authority root cause.
* Per-project digests produce targeted, non-redundant config-reread nudges.
* Resolution and digest are pure functions, giving a fast, stable test surface.
* `internal/config` becomes the deep module ADR-0002 §8 described.

### Negative / Trade-offs

* Hard cutover requires every existing home to migrate once; mitigated by explicit fleet plan/apply orchestration and by archiving rather than deleting legacy files.
* Registry parsing moves from `internal/fleet` to `internal/config`, so fleet lifecycle code must mutate registries through the config write API — a surgical but real refactor across seed, retire, and project commands.
* Resolution adds a layer; the leverage (multi-project scoping, targeted nudges, pure testability) justifies it.

## Explicitly Out of Scope

* Enforcing `DispatchProfile.MaxConcurrent` (declared today but dormant; a separate small fix).
* The duplicate Pi extension conflict from the incident (`munsu_wake_resolve` registered by multiple alias files) — an integration-port defect tracked separately.
* Delivery false-negative, status projection supersession, the pre-merge `delivered` lifecycle state, teardown cleanliness, GitHub Issue closure sync, backend liveness, worktree mutation safeguards, recovery loop limits, and binary/source skew diagnostics — separate workstreams enumerated in the incident report.
