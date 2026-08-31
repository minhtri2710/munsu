# 0022. Durable Per-Task Delivery Contract

* **Status:** Proposed
* **Date:** 2026-08-31
* **Extends:** ADR-0008 (task-authority owns durable task truth), ADR-0016 (review-before-merge has no technical gate)
* **Triggered by:** firstmate → munsu parity refresh, gap G5 (firstmate #1563, "explicit per-task delivery contract, refuses to guess")

## Context

A task's delivery mode (no-mistakes / direct-PR / local-only) decides how its
work reaches `origin`. In munsu the mode is **resolved fresh on every spawn** and
never stored on the canonical task. `ResolveDeliveryMode` (called from
`internal/fleet/spawn_config_snapshot.go` line 76) combines, in precedence
order, the `--mode` flag, the project registry default, and auto-detection; the
result is projected only into the ephemeral home meta (`meta["mode"]`).

Two consequences follow. First, because resolution re-runs each generation
against live inputs (registry, PATH, capability), **the same task can silently
acquire a different delivery mode across re-spawns** — the contract is not
fixed to the task. Second, there is an authorized mid-spawn *downgrade*:
`preflightNoMistakes` (`internal/fleet/delivery_preflight.go`) can fall a task
back from no-mistakes to direct-PR when the no-mistakes capability is absent or
lost, and a late capability loss can do the same after launch.

firstmate #1563 records mode as a machine-readable per-task brief line, re-checks
brief ↔ spawn ↔ promote against it, and demotes the project registry to
*advisory*. munsu's design instead treats the registry as an enforced default
and prizes fallback resilience. The parity refresh asked which philosophy munsu
should hold. The decision (recorded here) is the **middle path**: fix the
contract to the task durably, but keep the authorized fallback — as an
explicitly recorded transition rather than a silent re-resolution.

## Evidence

* `internal/fleet/spawn_config_snapshot.go:76` — `ResolveDeliveryMode(args.Mode,
  …DefaultMode, …RequireNoMistakes)`, run per snapshot; result lands in home meta
  only, not on the canonical aggregate.
* `internal/fleet/delivery_preflight.go` `preflightNoMistakes` — the authorized
  no-mistakes → direct-PR fallback path.
* `internal/fleet/brief.go` `ScaffoldOptions.Mode` — the resolved **delivery
  mode** (no-mistakes / direct-PR / local-only). `shipBriefTemplate` renders it as
  `Delivery mode: %s` and `internal/cli/session_cmd.go` sets it from
  `ResolveDeliveryMode`, so the field already carries delivery-mode semantics: the
  mode is rendered into the brief and, separately, projected into home meta
  (`meta["mode"]`) by the spawn path, but it is never stored on the canonical task
  aggregate.
* ADR-0008 §2 — durable task truth lives in `internal/taskauthority`, not in
  home meta projections.

## Decision

### 1. Delivery mode is durable task truth, resolved once

On first spawn of a task, `ResolveDeliveryMode` runs as today, but the resolved
mode is **recorded on the canonical task aggregate** in
`internal/taskauthority` as the task's delivery contract. Subsequent generations
read the recorded contract instead of re-resolving from live inputs. A task's
mode therefore cannot silently change across re-spawns. The home
`meta["mode"]` value becomes a projection of this durable truth, not its source.

### 2. Fallback is retained but recorded as an explicit transition

The authorized no-mistakes → direct-PR downgrade (`preflightNoMistakes` and the
late-capability-loss path) is kept — munsu's fallback resilience is deliberate
and not surrendered to firstmate's strict refuse-to-guess. But a fallback now
**mutates the recorded contract through a task-authority operation** that
records the transition (from-mode, to-mode, reason, generation), rather than
producing a divergent fresh resolution each spawn. The contract always states
the mode in force and how it got there.

### 3. Project registry stays an input, not silently authoritative

The registry keeps feeding the *initial* resolution (munsu does not adopt
firstmate's pure advisory stance), but once a task's contract is recorded, the
registry cannot silently override it on a later spawn — only an explicit
`--mode` re-scaffold or a recorded fallback changes it. This closes the drift
without demoting the registry to advisory-only.

### Cleanup

`ScaffoldOptions.Mode` already carries delivery-mode semantics (its runtime value
is the resolved mode and `shipBriefTemplate` renders `Delivery mode: %s`), so no
rename or disambiguation is needed. The only genuine cleanup, folded into D1, is
to correct the misleading `// delivery mode (feat, fix, refactor, etc.)` struct
comment and the `brief_test.go` fixtures that pass `feat`/`fix` so they use real
delivery-mode values (no-mistakes / direct-PR / local-only).

### Existing machinery (no parallel record)

An ephemeral fleet-runtime record already exists in
`internal/fleet/delivery_attestation.go`: `CapabilityAttestation` carries
`RequestedMode`, `EffectiveMode`, `FallbackReason`, and `FallbackPolicy`, and
drives the authorized no-mistakes → direct-PR fallback via
`HandleLateCapabilityLoss` at soldier-launch preflight. It is a 24h-expiry
capability snapshot — not the canonical aggregate and not durable across
re-spawns — so the D1/D2 durable contract is distinct. D1/D2 must relate to
this existing machinery (one source of truth for the delivery-mode transition,
per one-live-contract) and must never introduce a parallel from/to/reason
record.

### Non-goals

* No change to the no-mistakes gate itself (ADR-0016 stands).
* munsu does **not** adopt firstmate's registry-advisory-only model or its hard
  refuse-to-guess on missing mode; auto-detection remains the first-spawn
  default.
