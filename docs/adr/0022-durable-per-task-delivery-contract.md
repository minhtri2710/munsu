# 0022. Durable Per-Task Delivery Contract

* **Status:** Accepted; amended by [ADR-0028](0028-named-ports-configured-tools-and-a-baseline-workflow.md): §1's initial mode is derived from configured tools, §2's fallback is retired, and §3's `--mode` re-scaffold is removed
* **Date:** 2026-08-31
* **Extends:** ADR-0008 (task-authority owns durable task truth), ADR-0016 (review-before-merge has no technical gate)
* **Triggered by:** firstmate → munsu parity refresh, gap G5 (firstmate #1563, "explicit per-task delivery contract, refuses to guess")

## Context

A task's delivery mode (no-mistakes / direct-PR / local-only) decides how its
work reaches `origin`. Before ADR-0028 the mode was **resolved fresh on every spawn**
and never stored on the canonical task. `ResolveDeliveryMode` (called from
`internal/fleet/spawn_config_snapshot.go` line 76) combined, in precedence
order, the `--mode` flag, the project registry default, and auto-detection; the
result was projected only into the ephemeral home meta (`meta["mode"]`).

Two consequences follow. First, because resolution re-runs each generation
against live inputs (registry, PATH, capability), **the same task can silently
acquire a different delivery mode across re-spawns** — the contract is not
fixed to the task. Second, there was one authorized mid-spawn *downgrade*, retired by ADR-0028 §2:
`Runner.preflightNoMistakes` (`internal/fleet/spawn_runner.go`) could fall a task
back from no-mistakes to direct-PR when the no-mistakes capability is absent or
lost. A capability loss detected after attestation and before soldier launch
does not downgrade the mode; it blocks the launch and requires a parent Decision.

firstmate #1563 records mode as a machine-readable per-task brief line, re-checks
brief ↔ spawn ↔ promote against it, and demotes the project registry to
*advisory*. munsu's design instead treats the registry as an enforced default
and preserves the explicit preflight fallback. The parity refresh asked which
philosophy munsu should hold. The decision (recorded here) is the **middle
path**: fix the contract to the task durably, while retaining only the
preflight fallback as an explicitly recorded transition rather than a silent
re-resolution.

> Superseded by ADR-0028 §2: the preflight fallback is retired. The per-task durable contract stands, now captured per task generation (ADR-0028 §5).

## Evidence

* `internal/fleet/spawn_runner.go` `resolveMode` resolves the generation's steps. When the task carries a `taskauthority.DeliveryContract`, `ResolveSpawnProjectConfig` (`internal/fleet/spawn_config_snapshot.go`) takes the review and forge steps from it; otherwise it resolves them from the project's configured tools. The mode is `taskauthority.DeliveryModeForSteps` over those steps. `recordDeliveryContract` records the contract on the task's generation at its first spawn.
* `internal/fleet/spawn_runner.go` `preflightNoMistakes` probes the review step when the mode is no-mistakes and refuses when it is not Ready. It never changes the mode. A late capability loss, or an expired attestation, blocks the launch through `checkAttestation` / `HandleLateCapabilityLoss` for a parent Decision (F028).
* `internal/fleet/brief.go` `ScaffoldOptions.Mode` is the resolved **delivery mode** (no-mistakes / direct-PR / local-only). `shipBriefTemplate` renders it as `Delivery mode: %s` and selects the delivery rules by it. `internal/cli/session_cmd.go` reads the mode from the canonical delivery contract (`taskauthority.DeliveryContract`) when the owning home's canonical record carries one, and otherwise resolves it from the project's resolved steps (`fleet.ResolveBriefProject`). The mode is durable task truth on the canonical aggregate.
* ADR-0008 §2 — durable task truth lives in `internal/taskauthority`, not in
  home meta projections.
* `internal/fleet/spawn_runner.go` `submitLaunch` — on a fresh submission, the
  attestation is rechecked immediately before endpoint delivery; recovery with
  matching durable `LaunchEvidence` returns through its fast path without the
  final submission-boundary recheck or resubmission.

## Decision

### 1. Delivery mode is durable task truth, resolved once

> Superseded by ADR-0028 §5: the initial mode is derived from the resolved review and forge steps; no default key, `require-no-mistakes` or PATH probe selects it. The contract is captured per task generation, so a reopen starts a new generation with no contract and the next spawn records one (G926 A6=b). The text below is the 2026-08-31 decision as recorded.

On first spawn of a task, `ResolveDeliveryMode` runs as today, but the resolved
mode is **recorded on the canonical task aggregate** in
`internal/taskauthority` as the task's delivery contract. With neither an
explicit nor typed default mode, a Ready capability probe selects
`no-mistakes`; any non-Ready probe result is refused when
`require-no-mistakes` is set and otherwise selects `direct-PR`. Subsequent
generations read the recorded contract instead of re-resolving from live
inputs. A task's mode therefore cannot silently change across re-spawns. The recorded contract
also carries across task transfer: `Canonical.ReceiveTransfer` writes the
received `DeliveryContract` onto the destination generation, so a transferred
task delivers under the same contract rather than re-resolving the mode from
the destination home's live inputs on its next spawn. The home
`meta["mode"]` value becomes a projection of this durable truth, not its source.

### 2. Fallback is retained but recorded as an explicit transition

> Superseded by ADR-0028 §2 and §5: there is no fallback. A configured step whose probe is not Ready refuses the run before any task mutation, and the recorded contract is never mutated. `DeliveryFallback`, `RecordDeliveryFallback` and their readers are deleted.

The authorized no-mistakes → direct-PR downgrade at `Runner.preflightNoMistakes` is
kept — munsu's fallback resilience is deliberate and not surrendered to
firstmate's strict refuse-to-guess. (The late-capability-loss path is no longer
a fallback: F028 makes it block the launch for a parent Decision.) But a
fallback now **mutates the recorded contract through a task-authority operation**
that
records the transition (from-mode, to-mode, reason, generation), rather than
producing a divergent fresh resolution each spawn. The contract always states
the mode in force and how it got there.

### 3. Project registry stays an input, not silently authoritative

> Superseded by ADR-0028 §5: `--mode` is removed. A generation's steps come from the project's configured tools, and the recorded contract fixes them for that generation.

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
`internal/fleet/delivery_attestation.go`: `CapabilityAttestation` carries the
review and forge `DeliveryStep`s it attested, and `HandleLateCapabilityLoss`
detects late capability loss at soldier launch (`checkAttestation`). The loss
blocks the launch for a parent Decision; it does not fall back (F028). The
attestation is a 24h-expiry snapshot, not the canonical aggregate and not durable
across re-spawns. The durable record is `taskauthority.DeliveryContract`, one per
task generation. Once recorded, its mode and steps are never changed, so there is
no transition record.

### Non-goals

* Superseded by ADR-0028 §2: the mid-spawn no-mistakes → direct-PR fallback at
  `Runner.preflightNoMistakes` no longer exists. Late capability loss is not
  a fallback; it blocks the launch for a parent Decision.
* munsu does **not** adopt firstmate's registry-advisory-only model or its hard
  refuse-to-guess on missing mode.
