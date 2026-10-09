# 0028. Named Ports, Configured Tools, and a Baseline Workflow Without Tools

* **Status:** Accepted
* **Date:** 2026-10-09
* **Extends:** ADR-0008 §6 (resolved Config Snapshots), ADR-0022 (per-task delivery contract)
* **Bounded by:** ADR-0019 (single static binary), ADR-0023 (no unbuilt machinery), ADR-0024 (a new refusal needs the owner's words)
* **Triggered by:** Human design gate G888/G890 in the munsu delivery ledger

## Owner's words (ADR-0024 §1), grantor the Human, typed, relayed by the Supervisor

* 2026-10-09T04:20:01Z: "cần thiết kế munsu theo dạng module, plugins, nếu cần tool thì nên cấu hình này nọ"
* 2026-10-09T04:22:22Z: "cần có 1 workflow cơ bản without tools, nếu có cấu hình + tool vào thì sẽ đi theo hướng cấu hình"
* 2026-10-09T08:10:31Z: "theo lời khuyên đi", accepting the Lead's recommended option on every gate: full scope; a configured tool whose probe fails refuses the run at every step; a tool entry names a compiled-in adapter; the review step is the first slice.

These words cover the refusals this ADR introduces (§3 and §5). Any other new refusal an implementing slice finds is a new gate.

## Context

munsu is already modular inside one binary, but the modules do not share one shape:

* The session backend is a Go interface (`Backend`, `internal/backend/session_session.go`) with five compiled-in adapters (tmux, herdr, zellij, cmux, orca), selected by name from the project overlay, with no auto-detection.
* Forge delivery is a Go interface (`DeliveryProvider`, `internal/fleet/delivery_deliver.go`) with GitHub and GitLab adapters.
* The worktree step is a Go interface (`Provider`, `internal/backend/worktree.go`) whose selection uses treehouse when it is on PATH, and otherwise falls back to git worktree with only a stderr note.
* A harness is a data registry of launch contracts (`internal/harness/adapter.go`), not an interface. A harness differs by data (flags, hook surfaces, patterns), so it stays a registry.
* The review gate is wired directly to no-mistakes (`NoMistakesProbe`, `internal/fleet/delivery_nomistakes.go`). `ResolveDeliveryMode` (`internal/fleet/spawn_spawn.go`) auto-selects no-mistakes when its probe is Ready, otherwise falls back to direct-PR unless `RequireNoMistakes` refuses.
* Quota balancing (`internal/harness/quota_balanced.go`) uses quota-axi when present and silently takes the first candidate otherwise.

Every external tool is found by bare name on PATH. No tool has a configured path or arguments. There is no plugin mechanism, and munsu cannot run its workflow when a tool is missing unless the code happens to fall back. Two of those fallbacks (worktree, quota) are silent.

## Decision

### 1. Ports are named and their adapters compile in

munsu has six ports. Each has one owning package, one contract, and one conformance test that every adapter of the port passes:

| Port | Owner | Contract | Baseline |
|---|---|---|---|
| session | `internal/backend` | `Backend` and its structured probe | none: a backend is required (§4) |
| worktree | `internal/backend` | `Provider` | git worktree |
| forge | `internal/fleet` | `DeliveryProvider` | local handoff (the `local-only` delivery mode) |
| review | `internal/fleet` | a review step ending in a head-bound `ReviewVerdict` (ADR-0025) | a reviewer seat or the Human |
| guard | `internal/cli`, `internal/fence` | git shim plus the OS write fence (ADR-0024 §3) | the same; it has no tool |
| quota | `internal/harness` | candidate ordering | dispatch profile order |

Adapters compile into the single binary (ADR-0019). The capability a step uses is what its adapter's own structured probe proves at run time; no declared capability table is reintroduced (`docs/architecture.md`, session backend).

### 2. A tool is a typed config entry

A tool entry names a compiled-in adapter of one port, with an optional absolute binary path that replaces PATH lookup and optional extra arguments appended to the adapter's fixed argv. An unknown adapter name, or an entry for a port that has none, fails config validation. There is no free-form command template: a template would be arbitrary execution from a config file.

Tool entries live in the typed config documents and resolve into the published Config Snapshot (ADR-0008 §6) like every other setting. A tool found on PATH but not configured is not used; `munsu init` and `munsu doctor` report detected tools and write or suggest entries.

### 3. One resolution rule per step, one path per run

At snapshot resolution each step resolves exactly once:

* No tool entry: the step resolves to its baseline, and the run says so.
* A tool entry whose probe is Ready: the configured adapter owns the step.
* A tool entry whose probe is not Ready: the run refuses before any task mutation, naming the step, the adapter and the probe reason. The operator removes the entry to run the baseline. munsu never switches a configured step to its baseline on its own.

The resolved choice for each step, with its probe result, is recorded in the published snapshot and pinned into the task's `DeliveryContract` (ADR-0022), so a run never re-decides mid-flight. Each run prints one line per step, for example `review: no-mistakes (configured, ready)` or `review: seat (baseline, no tool configured)`. There is no second path and no silent skip.

### 4. The baseline workflow needs no tool beyond git, a session backend and a harness

1. Worktree: `git worktree add`.
2. Commit: the Soldier commits on its task branch, reports its exact head and stops. A Soldier push to a remote needs the Human's exact-head grant (the soldier push grant ADR, delivered separately).
3. Handoff: the branch, the exact head and the base are the handoff. No forge PR is opened.
4. Review: a reviewer seat or the Human records a head-bound `ReviewVerdict` (ADR-0025).
5. Merge: munsu asks the Human to merge, naming the branch and the exact head. munsu never merges in the baseline.

A session backend (tmux at minimum) and one harness binary are required to run any seat; they are not optional tools.

### 5. The delivery mode is derived from the resolved steps

The configured default mode and the auto-selection by PATH probe are replaced by derivation from the resolved review and forge steps:

| Review step | Forge step | Delivery mode |
|---|---|---|
| no-mistakes (configured, Ready) | any | `no-mistakes` |
| baseline | github or gitlab (configured, Ready) | `direct-PR` |
| baseline | baseline | `local-only` |

`DefaultMode`, `RequireNoMistakes` and `AllowDirectPRFallback` are deleted from the config documents and the snapshot, with no migration (pre-launch). Requiring no-mistakes is now configuring it: a configured no-mistakes whose probe fails refuses the run under §3. An explicit `--mode` stays the per-task choice recorded in the `DeliveryContract` (ADR-0022), unchanged by this ADR.

### 6. No dynamic Go plugins; out-of-process adapters are deferred

No Go `plugin` package and no exec-and-JSON adapter protocol are built. Go plugins break the single static binary (ADR-0019) and are not portable. An out-of-process adapter protocol in the shape of a git credential helper (munsu runs a named helper, writes a JSON request on stdin and reads a JSON result) is a deferred option. Its trigger is a second real consumer that needs an adapter which cannot live in this repository. Until then it is unbuilt machinery (ADR-0023).

## Consequences

* The first slice is the review step: the review tool entry, the seat baseline, the derivation in §5 (which needs the forge entry to replace `direct-PR` versus `local-only` selection), and the deletion of the three config keys. It is a behavior change in its own pull request.
* Later slices bring the worktree, quota and forge steps under §3. Each removes a silent fallback or a PATH-only lookup and cites this ADR's words for the refusal it adds.
* Shared conformance tests are added per port as each port's slice lands, not ahead of it (ADR-0023).
* Until a step's slice lands, that step keeps its current behavior, and `docs/architecture.md` describes the code as it is.
