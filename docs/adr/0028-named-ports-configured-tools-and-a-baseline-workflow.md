# 0028. Named Ports, Configured Tools, and a Baseline Workflow Without Tools

* **Status:** Accepted
* **Date:** 2026-10-09
* **Extends:** ADR-0008 §6 (resolved Config Snapshots)
* **Amends:** ADR-0022 §1 (the initial mode is derived from configured steps, not from a default key and a PATH probe), §2 (the no-mistakes → direct-PR fallback is retired) and §3 (the `--mode` flag and its re-scaffold are removed)
* **Bounded by:** ADR-0019 (single static binary), ADR-0023 (no unbuilt machinery), ADR-0024 (a new refusal needs the owner's words)
* **Triggered by:** Human design gates G888, G890 and G894 (resolved by G895) in the munsu delivery ledger

## Owner's words (ADR-0024 §1), grantor the Human, typed, relayed by the Supervisor

* 2026-10-09T04:20:01Z: "cần thiết kế munsu theo dạng module, plugins, nếu cần tool thì nên cấu hình này nọ"
* 2026-10-09T04:22:22Z: "cần có 1 workflow cơ bản without tools, nếu có cấu hình + tool vào thì sẽ đi theo hướng cấu hình"
* 2026-10-09T08:10:31Z: "theo lời khuyên đi", accepting the Lead's recommended option on every gate: full scope; a configured tool whose probe fails refuses the run at every step and never switches to the baseline; a tool entry names a compiled-in adapter; the review step is the first slice.
* 2026-10-09T08:22:54Z: "oki đồng ý", resolving the follow-up gate G894: the `--mode` flag is removed and the delivery mode is configuration only; a tool on PATH with no entry is not used; the baseline is today's `local-only` delivery, with no push and no new forge adapter.
* 2026-10-09T08:27:13Z (selected in a dialog, ledger row G902): "A: chấp nhận, tính vào lời đã nói (Khuyên)", accepting §5's refusal of a review entry naming no-mistakes without a forge entry as part of the 08:22:54Z words.
* 2026-10-10T08:25:30Z (G932): "nói munsu cleanup file docs đi, lấy codebase is truth làm chuẩn mực" ("tell munsu to clean up the docs; take the codebase as the truth"), covering the §2 refusal of path and args on the github forge adapter.

These words cover the refusals in §2, §3 and §5, the retirement of the direct-PR fallback (a configured step that fails never switches to its baseline) and the removal of `--mode`. Any other new refusal an implementing slice finds is a new gate.

## Context

munsu is already modular inside one binary, but the modules do not share one shape:

* The session backend is a Go interface (`Backend`, `internal/backend/session_session.go`) with five compiled-in adapters (tmux, herdr, zellij, cmux, orca), selected by name from the project overlay, with no auto-detection.
* Forge delivery is a Go interface (`DeliveryProvider`, `internal/fleet/delivery_deliver.go`) with GitHub and GitLab adapters.
* The worktree step is a Go interface (`Provider`, `internal/backend/worktree.go`) that uses treehouse when it is on PATH, and otherwise falls back to git worktree with a stderr note.
* A harness is a data registry of launch contracts (`internal/harness/adapter.go`), not an interface. A harness differs by data (flags, hook surfaces, patterns), so it stays a registry.
* Before this ADR, the review gate was wired directly to no-mistakes (`NoMistakesProbe`, `internal/fleet/delivery_nomistakes.go`). `ResolveDeliveryMode` (`internal/fleet/spawn_spawn.go`) selected no-mistakes when its probe was Ready, and otherwise direct-PR, unless `RequireNoMistakes` refused. Spawn preflight kept a recorded no-mistakes → direct-PR fallback when `AllowDirectPRFallback` was set (ADR-0022 §2).
* Quota balancing (`internal/harness/quota_balanced.go`) uses quota-axi when present, and silently takes the first candidate otherwise.

Apart from the OS fence's fixed `/usr/bin/sandbox-exec`, every external tool is found by bare name on PATH. No tool has a configured path or arguments. There is no plugin mechanism. When a tool is missing, munsu runs only where the code happens to fall back, and those fallbacks are either silent (quota) or a stderr note (worktree).

## Decision

This section states the target. Nothing in it is built when this ADR is accepted; the Consequences list which slice builds each part.

### 1. Ports are named and their adapters compile in

munsu has six ports, each with one owning package and one contract:

| Port | Owner | Contract | Baseline |
|---|---|---|---|
| session | `internal/backend` | `Backend` and its structured probe | none: a backend is required (§4) |
| worktree | `internal/backend` | `Provider` | git worktree |
| forge | `internal/fleet` | `DeliveryProvider` | local handoff (the `local-only` delivery mode) |
| review | `internal/fleet` | a review step ending in a head-bound `ReviewVerdict` (ADR-0025) | a reviewer seat or the Human |
| guard | `internal/cli`, `internal/fence` | git shim plus the OS write fence (ADR-0024 §3) | the same |
| quota | `internal/harness` | candidate ordering | dispatch profile order |

The guard port is a name only: it has no tool entry, no alternative adapter and no conformance test. Every other port gets one conformance test that each of its adapters passes, added by that port's slice.

Adapters compile into the single binary (ADR-0019). The capability a step uses is what its adapter's own structured probe proves at run time. No declared capability table is reintroduced (`docs/architecture.md`, session backend).

### 2. A tool is a typed config entry

A tool entry names a compiled-in adapter of one port, with two optional parts: an absolute binary path that replaces PATH lookup, and extra arguments appended to the adapter's fixed argv. An unknown adapter name, or an entry for a port without adapters, fails config validation. The github forge adapter accepts neither part, because it runs gh-axi and gh from PATH (G932). There is no free-form command template, because a template would be arbitrary execution from a config file.

Tool entries live in the typed config documents and resolve into the published Config Snapshot (ADR-0008 §6) like every other setting. A tool that is on PATH but has no entry is not used. `munsu init` and `munsu doctor` report the tools they detect, and write or suggest entries for them.

### 3. One resolution rule per step, one path per run

At snapshot resolution each step resolves exactly once:

* No tool entry: the step resolves to its baseline, and the run says so.
* A tool entry whose probe is Ready: the configured adapter owns the step.
* A tool entry whose probe is not Ready, at resolution or at spawn preflight: the run refuses before any task mutation. The refusal names the step, the adapter and the probe reason. To run the baseline, the operator removes the entry. munsu never switches a configured step to its baseline on its own.

The resolved choice for each step and its probe result are recorded in the published snapshot and in the task's `DeliveryContract` (ADR-0022), so a run never re-decides mid-flight. Each run prints one line per step: `review tool: no-mistakes (ready; <probe detail>)` for a configured step, or `review tool: baseline (no tool configured)` for a step with no entry. The forge step prints as `forge tool: …` in the same form. There is no second path and no silent skip.

### 4. The baseline workflow needs no tool beyond git, a session backend and a harness

The baseline is today's `local-only` delivery:

1. Worktree: `git worktree add`.
2. Commit: the Soldier commits on its task branch, reports its exact head and stops. It does not push. A Soldier push to a remote is a separate Human grant (G878, delivered separately).
3. Handoff: the branch, the exact head and the base are the handoff. No forge PR is opened.
4. Review: a reviewer seat or the Human records a head-bound `ReviewVerdict` (ADR-0025).
5. Merge: munsu asks the Human to merge, naming the branch and the exact head. munsu never merges in the baseline.

Any seat needs a session backend (tmux at minimum) and one harness binary. These are requirements, not optional tools.

### 5. The delivery mode is derived from the resolved steps

When a task's contract is first recorded (ADR-0022 §1), the default mode comes from the resolved review and forge steps, not from a configured default or a PATH probe:

| Review step | Forge step | Delivery mode |
|---|---|---|
| no-mistakes (configured, Ready) | github or gitlab (configured, Ready) | `no-mistakes` |
| baseline | github or gitlab (configured, Ready) | `direct-PR` |
| baseline | baseline | `local-only` |

A no-mistakes run ends in a forge pull request, so a review entry naming no-mistakes without a forge entry fails config validation.

`DefaultMode`, `RequireNoMistakes` and `AllowDirectPRFallback` are deleted from the config documents and the snapshot, with no migration (pre-launch). Requiring no-mistakes now means configuring it: a configured no-mistakes whose probe fails refuses under §3. ADR-0022 §2's fallback is retired. The fallback branch of spawn preflight, `DeliveryFallback`, the `RecordDeliveryFallback` operation and every reader of them are deleted. The `--mode` flag is removed, with its re-scaffold of a recorded contract (ADR-0022 §3). The delivery mode is configuration only. A task generation records its contract once, at its first spawn, and does not re-decide it within that generation. A reopen starts a new generation with no contract, so the next spawn records one from the steps resolved then. A per-task `--mode` that dropped a configured review step would bypass §3's refusal, and holding back a single task's publication is already owned by the Soldier push grant (G878).

### 6. No dynamic Go plugins; out-of-process adapters are deferred

No Go `plugin` package and no exec-and-JSON adapter protocol are built. Go plugins break the single static binary (ADR-0019) and are not portable. An out-of-process adapter protocol shaped like a git credential helper is a deferred option: munsu would run a named helper, write a JSON request on stdin and read a JSON result. Its trigger is a second real consumer that needs an adapter which cannot live in this repository. Until then it is unbuilt machinery (ADR-0023).

## Consequences

* The first slice is the review and forge resolution needed by §5. It covers:
  * the review and forge tool entries;
  * the seat baseline;
  * the derived mode and the removal of `--mode`;
  * the per-step output line;
  * deletion of the three config keys and of the ADR-0022 §2 fallback machinery.

  It is a behavior change in its own pull request.
* Later slices bring the worktree and quota steps under §3. Each removes a quiet fallback or a PATH-only lookup and cites this ADR's words for the refusal it adds.
* Each port's conformance test lands with that port's slice, not ahead of it (ADR-0023).
* Until a step's slice lands, that step keeps its current behavior, and `docs/architecture.md` describes the code as it is.
