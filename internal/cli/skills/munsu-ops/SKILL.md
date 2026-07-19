---
name: munsu-ops
description: Operate munsu as the fleet orchestrator CLI (init home, session-start, spawn/supervise crews, backlog management, seconds, watcher, delivery helpers). Use when the user wants to run munsu, set up a munsu home, spawn or steer crews, arm the watcher, drain wakes, manage backlog or seconds, or asks how to use munsu as a fleet orchestrator.
---

# munsu-ops — fleet orchestration operator skill

Root virtue: **predictability** (same process each run, not same output).

## Steps

### 1. Resolve home

Run `munsu home` to check whether the munsu home directory exists. The resolution order is: `--home` flag > `MUNSU_HOME` env > `~/.munsu`.

**Completion:** Home path is known and exists. If missing, run `munsu init` to create it, which also seeds the orchestrator operating manual at `<home>/AGENTS.md`.

**Context pointer:** To update munsu itself or any second home after the repo advances, run `munsu skill show munsu-update` and follow the auxiliary skill. It runs `munsu update` to fast-forward the install root, then the manual procedure for each second home.

### 2. Session start

Run `munsu session-start` to lock, bootstrap, and print the session-start digest. The digest has five sections:
1. **Bootstrap Diagnostics** — toolchain readiness lines (see context pointer below).
2. **Backlog Digest** — bounded, metadata-only summary of queued/in-flight/blocked tasks (IDs only, no bodies).
3. **Context** — content of `data/captain.md`, `data/learnings.md`, `data/projects.md`, and `data/seconds.md` (first 20 lines each).
4. **Fleet State** — in-flight tasks with last status line and pane liveness.
5. **Supervision** — wake-handling reminder block.
**Completion:** Digest understood; lock status known. Do not re-bulk-read what the digest already printed.

**Context pointer:** Bootstrap diagnostics lines (`MISSING:`, `NEEDS_GH_AUTH`, `TANGLE:`, `CREW_HARNESS_OVERRIDE:`, `CREW_DISPATCH:`, `FLEET_SYNC:`, `SECOND_SYNC:`, `SECOND_LIVENESS:`, `TASKS_AXI:`) are handled by the auxiliary skill — run `munsu skill show bootstrap-diagnostics`. Review the Context and Fleet State sections — they replace reading those files by hand.

### 3. Classify work

Determine the task kind (ship vs scout), identify the project from the registry, and decide whether a second is in scope.

**Completion:** One clear task id + project known.

### 4. Spawn / brief

- `munsu backlog add <id> "<desc>" --kind ship|scout --repo <name> --start` — register the task.
- `munsu brief <id> <repo> [--scout]` — scaffold the crew brief.
- Fill in the `{TASK}` placeholder in `data/<id>/brief.md`.
- `munsu spawn <id> <project> [--kind ship|scout] [--mode no-mistakes|direct-PR|local-only]` — launch the crew.

**Completion:** Meta exists, endpoint is alive (verify with `munsu peek <id>` or `munsu crew-state <id>`).

**Context pointer:** The auxiliary skill `harness-adapters` describes launch templates per harness (model flags, effort flags) and turn-end hooks — run `munsu skill show harness-adapters` and consult it before spawning if harness-specific flags are needed.

**Context pointer:** For second lifecycle operations (seed, launch, retire, handoff, config-push), run `munsu skill show second-provisioning` and follow the auxiliary skill.

### 5. Supervise

- Ensure the watcher: `munsu watch ensure [--restart]`.
- On wake: `munsu wake-drain` then `munsu crew-state <id>` (not raw status tail) as ground truth.
- Steer as needed: `munsu send <id> "<line>"`.
- Peek at output: `munsu peek <id> [--lines N]`.

**Completion:** Actionable wakes handled; persistent watcher remains healthy while tasks are in flight.

**Context pointer:** If a crew is unresponsive or stuck, run `munsu skill show stuck-crew-recovery` and follow its escalation ladder (peek -> steer -> interrupt -> relaunch -> fail).

**Context pointer:** Supervision behavior varies by harness. Run `munsu skill show harness-adapters`
for launch templates. The per-harness supervision protocols at `docs/supervision-protocols/`
describe the exact arm/wake/drain/guard loop for each supported harness (claude, codex, grok, pi, opencode).

Delivery mode is set at spawn time (`--mode`). Act according to mode:

- **no-mistakes** (default): The crew runs the no-mistakes pipeline. When it notifies completion, verify the PR is open and checks are green.
- **direct-PR**: `munsu delivery pr-check <id> <pr-url>` to record the PR, then `munsu delivery pr-merge <id> <pr-url>` once approved.
- **local-only**: `munsu delivery merge-local <id>` for a fast-forward merge to the local default branch.

**Completion:** PR URL (for remote modes) or local merge note documented.

### 7. Teardown

Only run when the task is fully landed (merged or report submitted).

```sh
munsu teardown <id> [--force]
```

**Completion:** `munsu teardown <id>` succeeds.

### Fleet-wide checks

- `munsu fleet view` — see the full fleet.
- `munsu guard` — run after every fleet action to catch tangle or stale watcher.
- `munsu fleet bearings` — compact resume report.
- `munsu stow <text...>` — capture durable learnings (data/learnings.md); inspect-then-update: matching entries are replaced, not duplicated.
- `munsu stow --captain <text...>` — capture captain preferences (data/learnings.md); created lazily if absent.
- `munsu stow --kind captain <text...>` — same as --captain.

## Reference rules

- Never invent flags — verify with `munsu <cmd> --help`.
- Heed `blocked:`, `needs-decision:`, and `paused:` statuses from crews.
- Run `munsu guard` after every fleet action.
- The seeded orchestrator manual at `<home>/AGENTS.md` is the authoritative per-project operator guide.
- Before marking a scout or review complete, load the `decision-hold-lifecycle` auxiliary skill
  via `munsu skill show decision-hold-lifecycle` to durably track unresolved captain decisions.

## See also

- `COMMANDS.md` — full command map grouped by lifecycle phase.
- `docs/supervision-protocols/` — per-harness supervision protocol docs.
- `docs/skills/decision-hold-lifecycle.md` — decision-hold lifecycle canonical reference.
- `munsu doctor` — toolchain diagnostics with fix commands.
