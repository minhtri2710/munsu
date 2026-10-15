---
name: munsu-ops
description: Operate munsu as the fleet orchestrator CLI (init home, session-start, spawn/supervise soldiers, task lifecycle, captains, watcher, delivery helpers). Use when the user wants to run munsu, set up a munsu home, spawn or steer soldiers, arm the watcher, claim wakes, manage tasks or captains, or asks how to use munsu as a fleet orchestrator.
---

# munsu-ops — fleet orchestration operator skill

Root virtue: **predictability** (same process each run, not same output).

## Steps

### 1. Resolve home

Run `munsu home` to check whether the munsu home directory exists. The resolution order is: `--home` flag > `MUNSU_HOME` env > `~/.munsu`.

**Completion:** Home path is known and exists. If missing, run `munsu init` to create it, which also seeds the orchestrator operating manual at `<home>/AGENTS.md`.

**Context pointer:** To update munsu itself or any captain home after the repo advances, run `munsu skill show munsu-update` and follow the auxiliary skill. It runs `munsu update` to fast-forward the install root, then the manual procedure for each captain home.

### 2. Session start

Run `munsu session-start` to lock, bootstrap, and print the session-start digest. The digest has five sections:
1. **Bootstrap Diagnostics** — toolchain readiness lines (see context pointer below).
2. **Backlog Digest** — bounded, metadata-only summary of queued/in-flight/blocked tasks (IDs only, no bodies).
3. **Context** — content of `data/general.md` and `data/learnings.md` (first 20 lines each).
4. **Fleet State** — in-flight tasks with last status line and pane liveness.
5. **Supervision** — wake-handling reminder block.
**Completion:** Digest understood; lock status known. Do not re-bulk-read what the digest already printed.

**Context pointer:** Bootstrap diagnostics lines (`MISSING:`, `NEEDS_GH_AUTH`, `SOLDIER_HARNESS:`, `SOLDIER_DISPATCH:`, `SECOND_LIVENESS:`) are handled by the auxiliary skill — run `munsu skill show bootstrap-diagnostics`. Review the Context and Fleet State sections — they replace reading those files by hand.

### 3. Classify work

Determine the task kind (ship vs scout), identify the project from the registry, and decide whether a captain is in scope.

**Completion:** One clear task id + project known.

### 4. Spawn / brief

- `munsu task add <id> "<desc>" --kind ship|scout|review [--reviews <ship-id>] [--repo <name>]` — register the queued task and write its brief to `data/<id>/brief.md`, with the description in place of `{TASK}`. The project is `--repo`, else the reviewed task's project, else the registered project of the current checkout; it must be registered.
- For a ship brief, replace the `{TEST_IMPACT}` placeholder with the test-impact map: for each path the task will change, the existing tests that exercise it, found by code search (`semble` or `zg`).
- `munsu spawn <id>` — launch the soldier in the task's project and ensure the watcher. Delivery mode comes from that project's configured forge step. Do not run `munsu task start <id>` before spawn.

**Completion:** Meta exists, endpoint is alive (verify with `munsu peek <id>` or `munsu task show <id>`, which reports `pane_alive`).

**Context pointer:** The auxiliary skill `harness-adapters` describes launch templates per harness (model flags, effort flags) and turn-end hooks — run `munsu skill show harness-adapters` and consult it before spawning if harness-specific flags are needed.

**Context pointer:** For captain lifecycle operations (seed, launch, retire, handoff, config-push), run `munsu skill show captain-provisioning` and follow the auxiliary skill.

#### Test-quality rules

munsu writes the test-quality rules into the briefs and charters itself; never copy them into a brief by hand.

- Review brief, Review method steps 5-10: a check that did not run, tamper, red proof, kept tests, production behaviour, lean code (`reviewBriefTemplate` in `internal/fleet/brief.go`).
- Ship brief: the red-proof row for each added or changed test, in the done evidence (`shipBriefTemplate` in `internal/fleet/brief.go`).
- Soldier charter (`internal/fleet/soldier_charter.go`): the kept-test rules (`## Tests`), the spec-versus-test `needs-decision` rule (Forbidden Actions item 12) and the Human-selected heavy proofs (`## Validation Scope`). The reviewer charter's `## Review Evidence` holds its evidence rule.

### 5. Supervise

- `munsu spawn` ensures the watcher; repair it with `munsu watch ensure [--restart]`.
- On wake: a delivered `[mu-system:wake]` prompt is answered with the `munsu wake resolve` command it carries;
  without delivery, claim with `munsu wake claim --consumer <id>`. `SUPERVISION.md` owns the rule.
- Ground truth: `munsu task show <id>` (not raw status tail).
- Steer as needed: `munsu send <id> "<line>"` (downlink only; Soldier and Captain targets are valid, while uplink to `general` is refused).
- Report status: `munsu report <state> "<msg>"` reports up the hierarchy (soldier/captain/general).
- **inbox** — preview: `munsu inbox` lists queued and claimed wakes and last captain status lines side by side.
  Use before `munsu wake claim` to preview what needs attention.

**Completion:** Actionable wakes handled; persistent watcher remains healthy while tasks are in flight.

**Context pointer:** If a soldier is unresponsive or stuck, run `munsu skill show stuck-soldier-recovery` and follow its escalation ladder (peek -> steer -> interrupt -> relaunch -> fail).

**Context pointer:** Supervision behavior varies by harness. Run `munsu skill show harness-adapters` for launch templates and the supervision table, and see `SUPERVISION.md` for the canonical watch/wake/guard loop.

### 6. Deliver

Delivery mode is derived from the project's configured forge step. Act according to the resolved mode:

- **direct-PR**: The Soldier pushes its committed task branch and opens the PR. Then `munsu delivery review-diff <id>` to review the branch and `munsu delivery pr-merge <id> <pr-url>` once approved. A landed merge marks the task done and tears the soldier down in the same command; re-run it to resume a failed teardown. `munsu delivery merge-status <id>` answers whether a delivery landed.
- **local-only**: munsu registers no local merge command; land the branch outside munsu, then close the task with `munsu task done <id>`.

**Completion:** PR URL (for remote modes) or local merge note documented.

### 7. Teardown

A direct-PR task is torn down by `munsu delivery pr-merge`. For local-only and scout tasks, run teardown only after the task is done and fully landed (merged or report submitted).

```sh
munsu teardown <id> [--force]
```

Teardown refuses a task whose delivery authorization is not revoked and has no outcome yet, since a merge may be in flight; the error names `munsu delivery merge-status <id>` and re-running `munsu delivery pr-merge`, and `munsu teardown --force <id>` retires it anyway.

**Completion:** `munsu teardown <id>` succeeds.

Teardown stops only holders of the task worktree. A task process that survives it (a dev server under `/tmp`, a `setsid` watcher) is listed as a `task process <pid> still running` step with its executable, cwd and listening ports; teardown never stops it and still succeeds. Stop it by hand or inspect it with `munsu doctor --orphans`. A process whose environment munsu cannot read (on macOS, Apple platform binaries) is not listed, so no line does not prove no leftover.

### Fleet-wide checks

- `munsu fleet view` — see the full fleet.
- `munsu captain converge` — validate registry entries, retry pending nudges, fast-forward Captain homes, push inherited config, refresh charters, check liveness (relaunching only when absence is proven), reconcile pending mailbox records, and report watcher status after Captain lifecycle changes.
- `munsu guard` — run after every fleet action to catch tangle or stale watcher.
- `munsu inbox` — preview pending wakes and captain status lines in one view.
- `munsu stow --general <text...>` — capture General preferences (`data/general.md`); created lazily if absent.
- `munsu stow --kind general <text...>` — same as --general.

## Reference rules

- Never invent flags — verify with `munsu <cmd> --help`.
- Heed `blocked:`, `needs-decision:`, and `paused:` statuses from soldiers.
- Run `munsu guard` after every fleet action.
- The seeded orchestrator manual at `<home>/AGENTS.md` owns the role rules (who decides, escalation, direction); this skill and `COMMANDS.md` and `SUPERVISION.md` own procedures and commands.
- Before marking a scout or review complete, load the `decision-hold-lifecycle` auxiliary skill
  via `munsu skill show decision-hold-lifecycle` to durably track unresolved general decisions.

## See also

- `COMMANDS.md` — curated command map grouped by lifecycle phase; use `munsu --help` and per-command `--help` for the complete registered set.
- `SUPERVISION.md` — canonical watch/wake/guard/AFK loop.
- `munsu skill show decision-hold-lifecycle` — decision-hold lifecycle reference.
- `munsu doctor` — toolchain diagnostics with fix commands.
