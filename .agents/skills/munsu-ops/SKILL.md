---
name: munsu-ops
description: Operate munsu as the fleet orchestrator CLI (init home, session-start, spawn/supervise crewmates, backlog management, secondmates, watcher, delivery helpers). Use when the user wants to run munsu, set up a munsu home, spawn or steer crewmates, arm the watcher, drain wakes, manage backlog or secondmates, or asks how to use munsu as a fleet orchestrator.
---

# munsu-ops — fleet orchestration operator skill

Root virtue: **predictability** (same process each run, not same output).

## Steps

### 1. Resolve home

Run `munsu home` to check whether the munsu home directory exists. The resolution order is: `--home` flag > `MUNSU_HOME` env > `~/.munsu`.

**Completion:** Home path is known and exists. If missing, run `munsu init` to create it, which also seeds the orchestrator operating manual at `<home>/AGENTS.md`.

### 2. Session start

Run `munsu session-start` to lock, bootstrap, and print the session-start digest. The digest has four sections:
1. **Bootstrap Diagnostics** — toolchain readiness lines (see context pointer below).
2. **Context** — content of `data/captain.md`, `data/learnings.md`, `data/projects.md`, and `data/secondmates.md` (first 20 lines each).
3. **Fleet State** — in-flight tasks with last status line and pane liveness.
4. **Supervision** — wake-handling reminder block.
**Completion:** Digest understood; lock status known. Do not re-bulk-read what the digest already printed.

**Context pointer:** Bootstrap diagnostics lines (`MISSING:`, `NEEDS_GH_AUTH`, `TANGLE:`, `CREW_HARNESS_OVERRIDE:`, `CREW_DISPATCH:`, `FLEET_SYNC:`, `SECONDMATE_SYNC:`, `SECONDMATE_LIVENESS:`, `TASKS_AXI:`) are handled in `docs/skills/bootstrap-diagnostics.md`. Review the Context and Fleet State sections — they replace reading those files by hand.

### 3. Classify work

Determine the task kind (ship vs scout), identify the project from the registry, and decide whether a secondmate is in scope.

**Completion:** One clear task id + project known.

### 4. Spawn / brief

- `munsu backlog add <id> "<desc>" --kind ship|scout --repo <name> --start` — register the task.
- `munsu brief <id> <repo> [--scout]` — scaffold the crewmate brief.
- Fill in the `{TASK}` placeholder in `data/<id>/brief.md`.
- `munsu spawn <id> <project> [--kind ship|scout] [--mode no-mistakes|direct-PR|local-only]` — launch the crewmate.

**Completion:** Meta exists, endpoint is alive (verify with `munsu peek <id>` or `munsu crew-state <id>`).

**Context pointer:** `docs/skills/harness-adapters.md` describes launch templates per harness (model flags, effort flags) and turn-end hooks — consult before spawning if harness-specific flags are needed.

**Context pointer:** For secondmate lifecycle operations (seed, launch, retire, handoff, config-push), see `.agents/skills/secondmate-provisioning/SKILL.md`.

### 5. Supervise

- Arm the watcher: `munsu watch-arm [--restart]`.
- On wake: `munsu wake-drain` then `munsu crew-state <id>` (not raw status tail) as ground truth.
- Steer as needed: `munsu send <id> "<line>"`.
- Peek at output: `munsu peek <id> [--lines N]`.

**Completion:** Actionable wakes handled; watcher re-armed if tasks are still in flight.

**Context pointer:** If a crewmate is unresponsive or stuck, follow `docs/skills/stuck-crewmate-recovery.md` escalation ladder (peek -> steer -> interrupt -> relaunch -> fail).

**Supervision loop details:** See `SUPERVISION.md` for watch/wake-drain/guard/afk loop mechanics.

### 6. Deliver

Delivery mode is set at spawn time (`--mode`). Act according to mode:

- **no-mistakes** (default): The crewmate runs the no-mistakes pipeline. When it notifies completion, verify the PR is open and checks are green.
- **direct-PR**: `munsu pr-check <id> <pr-url>` to record the PR, then `munsu pr-merge <id> <pr-url>` once approved.
- **local-only**: `munsu merge-local <id>` for a fast-forward merge to the local default branch.

**Completion:** PR URL (for remote modes) or local merge note documented.

### 7. Teardown

Only run when the task is fully landed (merged or report submitted).

```sh
munsu teardown <id> [--force]
```

**Completion:** `munsu teardown <id>` succeeds.

### Fleet-wide checks

- `munsu fleet-view` — see the full fleet.
- `munsu guard` — run after every fleet action to catch tangle or stale watcher.
- `munsu bearings` — compact resume report.
- `munsu stow <learning...>` — capture durable learnings.

## Reference rules

- Never invent flags — verify with `munsu <cmd> --help`.
- Heed `blocked:`, `needs-decision:`, and `paused:` statuses from crewmates.
- Run `munsu guard` after every fleet action.
- The seeded orchestrator manual at `<home>/AGENTS.md` is the authoritative per-project operator guide.

## See also

- `COMMANDS.md` — full command map grouped by lifecycle phase.
- `SUPERVISION.md` — watch/wake-drain/guard/afk loop details.
