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
3. **Context** — content of `data/general.md`, `data/learnings.md`, `data/projects.md`, and `data/captains.md` (first 20 lines each).
4. **Fleet State** — in-flight tasks with last status line and pane liveness.
5. **Supervision** — wake-handling reminder block.
**Completion:** Digest understood; lock status known. Do not re-bulk-read what the digest already printed.

**Context pointer:** Bootstrap diagnostics lines (`MISSING:`, `NEEDS_GH_AUTH`, `TANGLE:`, `SOLDIER_HARNESS:`, `SOLDIER_DISPATCH:`, `FLEET_SYNC:`, `SECOND_SYNC:`, `SECOND_LIVENESS:`, `TASKS_AXI:`) are handled by the auxiliary skill — run `munsu skill show bootstrap-diagnostics`. Review the Context and Fleet State sections — they replace reading those files by hand.

### 3. Classify work

Determine the task kind (ship vs scout), identify the project from the registry, and decide whether a captain is in scope.

**Completion:** One clear task id + project known.

### 4. Spawn / brief

- `munsu task add <id> "<desc>" --kind ship|scout --repo <name>` — register the queued task; use `munsu task start <id>` only after readiness checks.
- `munsu brief <id> <repo> [--scout]` — scaffold the soldier brief.
- Fill in the `{TASK}` placeholder in `data/<id>/brief.md`.
- `munsu spawn <id> [<project>] [--kind ship|scout] [--mode no-mistakes|direct-PR|local-only]` — launch the soldier; project is inferred from the current directory when omitted.

**Completion:** Meta exists, endpoint is alive (verify with `munsu peek <id>` or `munsu soldier-state <id>`).

**Context pointer:** The auxiliary skill `harness-adapters` describes launch templates per harness (model flags, effort flags) and turn-end hooks — run `munsu skill show harness-adapters` and consult it before spawning if harness-specific flags are needed.

**Context pointer:** For captain lifecycle operations (seed, launch, retire, handoff, config-push), run `munsu skill show captain-provisioning` and follow the auxiliary skill.

### 5. Supervise

- Ensure the watcher: `munsu watch ensure [--restart]`.
- On wake: prefer `munsu wake claim --consumer <id>` with lease management;
  `munsu wake claim` is the lease-based wake queue surface.
- Ground truth: `munsu soldier-state <id>` (not raw status tail).
- Steer as needed: `munsu send <id> "<line>"` (downlink only; Soldier and Captain targets are valid, while uplink to `general` is refused).
- Uplink status: `munsu report <state> "<msg>"` reports up the hierarchy (soldier/captain/general).
  Use `munsu notify` as an alias.
- **inbox** — preview: `munsu inbox` lists pending wakes and last captain status lines side by side.
  Use before `munsu wake claim` to preview what needs attention.

**Completion:** Actionable wakes handled; persistent watcher remains healthy while tasks are in flight.

**Context pointer:** If a soldier is unresponsive or stuck, run `munsu skill show stuck-soldier-recovery` and follow its escalation ladder (peek -> steer -> interrupt -> relaunch -> fail).

**Context pointer:** Supervision behavior varies by harness. Run `munsu skill show harness-adapters` for launch templates, and see `SUPERVISION.md` for the canonical watch/wake/guard loop.

### 6. Deliver

Delivery mode is set at spawn time (`--mode`). Act according to mode:

- **no-mistakes** (default): The soldier runs the no-mistakes pipeline. When it notifies completion, verify the PR is open and checks are green, then after it is merged run `munsu task done <id>`.
- **direct-PR**: `munsu delivery review-diff <id>` to review the branch, then `munsu delivery pr-merge <id> <pr-url>` once approved. `munsu delivery merge-status <id>` reports whether it landed; after it lands, run `munsu task done <id>`.
- **local-only**: munsu registers no local merge command; land the branch outside munsu, then close the task with `munsu task done <id>`.

**Completion:** PR URL (for remote modes) or local merge note documented.

### 7. Teardown

For the standalone path, only run after the task is already done and fully landed (merged or report submitted). The combined `munsu delivery pr-merge <id> <pr-url> --teardown` path retires directly from the working phase without marking the task done.

```sh
munsu teardown <id> [--force]
```

**Completion:** `munsu teardown <id>` succeeds.

### Fleet-wide checks

- `munsu fleet view` — see the full fleet.
- `munsu captain converge` — reconcile mailbox pending records, terminal receipts, nudges, and inherited config after Captain lifecycle changes.
- `munsu guard` — run after every fleet action to catch tangle or stale watcher.
- `munsu fleet bearings` — compact resume report.
- `munsu inbox` — preview pending wakes and captain status lines in one view.
- `munsu stow --general <text...>` — capture General preferences (`data/general.md`); created lazily if absent.
- `munsu stow --kind general <text...>` — same as --general.

## Reference rules

- Never invent flags — verify with `munsu <cmd> --help`.
- Heed `blocked:`, `needs-decision:`, and `paused:` statuses from soldiers.
- Run `munsu guard` after every fleet action.
- The seeded orchestrator manual at `<home>/AGENTS.md` is the authoritative per-project operator guide.
- Before marking a scout or review complete, load the `decision-hold-lifecycle` auxiliary skill
  via `munsu skill show decision-hold-lifecycle` to durably track unresolved general decisions.

## See also

- `COMMANDS.md` — curated command map grouped by lifecycle phase; use `munsu --help` and per-command `--help` for the complete registered set.
- `SUPERVISION.md` — canonical watch/wake/guard/AFK loop.
- `munsu skill show decision-hold-lifecycle` — decision-hold lifecycle reference.
- `munsu doctor` — toolchain diagnostics with fix commands.
