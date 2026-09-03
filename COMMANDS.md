# munsu COMMANDS.md

A curated command map grouped by lifecycle phase. It is not an exhaustive index:
`munsu` registers more commands than are listed here. The complete registered set
is whatever `munsu --help` prints (and `munsu <command> --help` for each group);
that output is the authority, not this file.

## Init and Setup

| Command | Description |
|---------|-------------|
| `munsu home [--mkdir]` | Print the munsu home directory (`~/.munsu`). With `--mkdir`, create the directory tree. |
| `munsu init` | Create home directory and seed the orchestrator operating manual. Auto-detects backend and soldier harness. |
| `munsu init --reconfigure` | Re-run auto-detection and overwrite existing config files. |
| `munsu doctor` | Run read-only diagnostics with fix commands for missing tools. |
| `munsu doctor --orphans` | Report processes whose owning run has ended, grouped GARBAGE / UNKNOWN / OWNED. Never terminates anything; exit 1 leftovers found, 2 nothing conclusive but a member should look. |
| `munsu config get <key>` | Read a configuration value. |
| `munsu project add <name> <path-or-url>` | Register a project. Git URLs are cloned automatically. |
| `munsu project list` | List registered projects. |
| `munsu project show <name>` | Show project details. |
| `munsu project rm <name>` | Remove a registered project. |
| `munsu project mode <name>` | Resolve delivery mode for a project. |
| `munsu project config get <name> <key>` | Read a project's overlay value (empty if unset, the project inherits the fleet base). |
| `munsu project config set <name> <key> <value>` | Write a project's overlay value; an empty value clears it so the project inherits the base. Keys: default-mode, soldier-harness, model, backend, require-no-mistakes, allow-direct-pr-fallback. |
| `munsu worktree get <repo-path> [--lease]` | Acquire a pooled worktree via treehouse. |
| `munsu worktree return <path>` | Return a worktree to the pool. |
| `munsu worktree status` | Show worktree pool status. |
| `munsu bootstrap [install <tools>...]` | Detect toolchain and run setup sweeps. |
| `munsu ensure-agents-md <project>` | Create/update AGENTS.md and CLAUDE.md symlink for a project. |

## Session

| Command | Description |
|---------|-------------|
| `munsu session-start` | Lock, bootstrap, and print session-start digest (Context, Fleet State, Supervision). |
| `munsu harness detect` | Detect the running agent harness. |
| `munsu harness soldier` | Resolve soldier harness (dispatch.json > config/soldier-harness > detected). |
| `munsu harness captain` | Resolve captain harness (config/captain-harness > config/soldier-harness > detected). |

## Soldier Lifecycle

| Command | Description |
|---------|-------------|
| `munsu spawn <id> [<project>] [--kind ship\|scout] [--mode no-mistakes\|direct-PR\|local-only] [--yolo] [--backend tmux\|herdr] [--arm]` | Spawn a soldier agent. Project inferred from cwd if omitted. With `--arm`, start the watcher after spawn. |
| `munsu send <id> <line>` | Send a line to a soldier session endpoint. |
| `munsu peek <id> [--lines N]` | Capture and print soldier output (default 40 lines). |
| `munsu soldier-state <id>` | Read soldier current state (status, pane liveness, no-mistakes run-step). |
| `munsu brief <id> <repo> [--scout]` | Scaffold a task brief (ship or scout template). |
| `munsu teardown <id> [--force]` | Tear down a soldier with safety checks. |
| `munsu promote <id>` | Promote a scout task to ship. |

## Lifecycle (Happy Path)

The recommended workflow for running a soldier task end-to-end:

1. **`munsu task add <id> <desc> [--kind ship|scout] [--repo <name>]`**
   Create the canonical task; additions always start queued. Use `munsu task start <id>` as the separate validated start mutation.
2. **`munsu brief <id> <repo> [--scout]`**
   Scaffold a task brief that the soldier reads on startup.
3. **`munsu spawn <id> [<project>] [--arm]`**
   Spawn the soldier — acquires a worktree, creates a session pane, writes task meta, and launches the harness. Project inferred from cwd if omitted.
4. **`munsu peek <id>` / `munsu send <id> <line>`**
   Monitor and interact with the running soldier as needed.
5. **`munsu delivery pr-merge <id> <pr-url>`**
   Land the work while the task is still working; delivery is refused once the task is terminal.
   Skip this step only for the local-only mode, where the branch lands outside munsu.
6. **`munsu task done <id>`**
   Mark the task complete. Completion must precede a standalone teardown: teardown
   retires the generation, and a retired task can no longer be completed.
7. **`munsu teardown <id>`**
   Terminate the soldier, release the worktree, and clean up runtime state.

> **Note:** Add tasks queued, then use `task start <id>` after readiness checks; the Task Authority links brief → spawn → closure → teardown.

## Supervision

| Command | Description |
|---------|-------------|
| `munsu watch` | Run the persistent watcher daemon. Singleton-safe via home-scoped lock. Actionable conditions are durably queued while the watcher keeps polling until SIGTERM or SIGINT. |
| `munsu watch-arm [--restart]` | Arm the watcher as a background process (deprecated: use `munsu watch ensure`). With `--restart`, signal existing watcher first. |
| `munsu wake claim --consumer <id> [--lease-seconds 60] [--limit 10]` | Claim a batch of pending wakes under a lease. Takes no positional argument; `--consumer` is required. |
| `munsu wake ack <lease-id> <event-id...>` | Acknowledge one or more processed wakes. |
| `munsu guard` | Warn on tangle (non-default branch in primary checkout) or stale watcher beat. |
| `munsu afk` | Enter away-mode supervision daemon; polls at reduced cadence; stops on SIGTERM/SIGINT. |

## Fleet

| Command | Description |
|---------|-------------|
| `munsu fleet sync [<project>]` | Fast-forward refresh project clones. |
| `munsu fleet snapshot` | Emit fleet snapshot as JSON. |
| `munsu fleet view` | Render fleet view from snapshot. |
| `munsu fleet bearings [<project-dir>]` | Compact resume report. |
| `munsu captain seed <id> <home-path>` | Seed a captain home with charter. |
| `munsu captain launch <captain-home>` | Launch a captain in its home (session-backed). |
| `munsu captain retire <captain-home>` | Retire a captain. Refuses with in-flight soldiers unless `--force`. |
| `munsu captain list` | List registered captains. |
| `munsu captain recover <captain-id>` | Run structured recovery: provenance, config, integration, charter-refresh, config-push, launch-readiness, relaunch-pane, watcher-ensure, legacy transport guard, nudge-retry. Each step reports ok/failed/skipped. |
| `munsu captain converge` | Locked convergence sweep: validate registry/provenance, stale legacy records, nudge retry, fast-forward, inheritance push, liveness check, instruction surface tracking. |
| `munsu captain update <captain-home>` | Safe fast-forward of a captain clone with typed outcome (already-current, fast-forwarded, state-only-skipped, etc.). |
| `munsu captain migrate <captain-home> <id>` | Migrate a state-only home to managed worktree. Use `--repo` for transactional git-worktree migration. |
| `munsu captain validate <captain-home>` | Validate a captain home structure and provenance. |
| `munsu captain config-push <captain-home>` | Push inheritable config to a captain and advance generation tracking. Creates config-reread requirement on change. |
| `munsu captain handoff <captain-home> <item-key...>` | Hand off queued tasks to a captain (tasks must be queued). |

### Recovery paths

CLI `munsu captain recover <captain-id>` runs an 11-step transactional recovery against a single captain — designed for operator-triggered repair.

Session-start `Recover()` (invoked via `munsu session-start` with captain liveness recovery) sweeps all registered captains with a lightweight 4-outcome model: alive, seeded (never launched), relaunched, or failed. Designed for automatic boot-time recovery.

## Delivery

| Command | Description |
|---------|-------------|
| `munsu delivery review-diff <id>` | Review diff between soldier branch and base. |
| `munsu delivery merge-status <id>` | Query merge status of the recorded delivery identity. Exit 0 = merged, 1 = not merged, 2+ = error. |
| `munsu delivery pr-merge <id> <pr-url> [-- --merge\|--rebase]` | Merge a PR via gh-axi CLI. Default method is squash. |

## Task Lifecycle

| Command | Description |
|---------|-------------|
| `munsu task add <id> <description> [--kind ship\|scout] [--repo <name>]` | Add a new queued task. |
| `munsu task list [--state <filter>]` | List tasks from the canonical Task Authority. |
| `munsu task show <id> [--full]` | Show task details with optional status log. |
| `munsu task start <id>` | Start a task (mark in-flight). |
| `munsu task done <id>` | Mark a task as done. |
| `munsu task block <id> [--by <dependency-id>]` | Block a task. |
| `munsu task unblock <id>` | Unblock a blocked task. |
| `munsu task reopen <id>` | Reopen a terminal task as a new generation. |
| `munsu task retry <id>` | Supersede a terminal generation as a new queued generation. |
| `munsu task status <id> <state> <message>` | Append an audit-only status line to a task. |

## Knowledge

| Command | Description |
|---------|-------------|
| `munsu stow [text...] [--kind learning\|general] [--general]` | Sweep session for durable knowledge. Uses inspect-then-update to avoid duplicates. |

## Maintenance

| Command | Description |
|---------|-------------|
| `munsu update` | Self-update munsu binary (fast-forward + rebuild with version stamp). |
