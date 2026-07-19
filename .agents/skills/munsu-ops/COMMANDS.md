# munsu command map — grouped by lifecycle phase

Command names match `munsu --help` output verbatim. All commands accept `--home` to override the home directory.

---

## Home / Init

| Command | Description |
|---------|-------------|
| `munsu home [--mkdir]` | Print or create the munsu home directory. Resolution: `--home` > `MUNSU_HOME` > `~/.munsu`. |
| `munsu init` | Create home tree and seed orchestrator operating manual. |
| `munsu update` | Self-update munsu. |

## Session lifecycle

| Command | Description |
|---------|-------------|
| `munsu session-start` | Lock, bootstrap, print session-start digest (Context, Fleet State, Supervision). |
| `munsu afk` | Enter away-mode sub-supervisor daemon (reduced polling cadence). |

## Task / Backlog

| Command | Description |
|---------|-------------|
| `munsu backlog <verb> [args...]` | Manage task backlog (verbs: add, start, done, list, show, block, unblock, ready, hold, update, render). Backend: tasks-axi when available, fallback to `$MUNSU_HOME/data/backlog.md`. |
| `munsu task <verb> [args...]` | Manage task lifecycle (verbs: add, list, show, status). |
| `munsu brief <id> <repo> [--scout]` | Scaffold a task brief (`--scout` for scout tasks). |

## Spawn / Send / Peek

| Command | Description |
|---------|-------------|
| `munsu spawn <id> <project> [--kind ship|scout] [--mode no-mistakes|direct-PR|local-only] [--backend tmux|herdr] [--yolo]` | Launch a crew agent in a worktree+tmux/herdr window. |
| `munsu send <id> "<line>"` | Send a line to a crew endpoint. |
| `munsu peek <id> [--lines N]` | Read last N lines of crew pane output (default 40). |
| `munsu crew-state <id>` | Read crew current state (meta + pane liveness + status log). |

## Watch / Wake / Guard

| Command | Description |
|---------|-------------|
| `munsu watch` | Run the event-driven watcher loop (singleton-safe, home-scoped lock). Exits with a wake reason. |
| `munsu watch-arm [--restart]` | Arm the watcher daemon (home-scoped). |
| `munsu wake-drain` | Drain queued wakes. |
| `munsu guard` | Warn on tangle or stale watcher. |

See `SUPERVISION.md` for the full watch/wake-drain/guard/afk loop.

## Delivery

| Command | Description |
|---------|-------------|
| `munsu delivery pr-check <id> <pr-url>` | Record PR URL and head SHA in task meta; write check.sh for merge polling. |
| `munsu delivery pr-merge <id> <pr-url> [-- --merge\|--rebase]` | Merge a PR via gh-axi CLI (default: squash). |
| `munsu delivery merge-local <id>` | Fast-forward merge crew branch to local default branch (local-only mode only). |
| `munsu delivery review-diff <id>` | Print diff summary between crew branch and base. |
| `munsu promote <id>` | Promote a scout task to ship. |

## Teardown

| Command | Description |
|---------|-------------|
| `munsu teardown <id> [--force]` | Tear down a crew with dirty/remote/report safety checks. |

## Second

| Command | Description |
|---------|-------------|
| `munsu second launch` | Launch a second in its home. |
| `munsu second seed` | Seed a second home with charter. |
| `munsu second list` | List registered seconds. |
| `munsu second handoff` | Hand off backlog items to a second. |
| `munsu second config-push` | Push inheritable config to a second. |
| `munsu second retire` | Retire a second. |

## Fleet / Diagnostics

| Command | Description |
|---------|-------------|
| `munsu fleet view` | Render fleet view from snapshot. |
| `munsu fleet snapshot` | Emit fleet snapshot JSON. |
| `munsu fleet sync` | Fast-forward refresh project clones. |
| `munsu fleet bearings` | Compact resume report of fleet state. |
| `munsu stow <learning...>` | Capture durable learnings from the current session. |
| `munsu bootstrap [install <tools>...]` | Detect toolchain and run setup sweeps. |

## Project / Config / Worktree

| Command | Description |
|---------|-------------|
| `munsu project add\|list\|mode\|rm\|show` | Manage the project registry. |
| `munsu config get\|set` | Read/write munsu configuration. |
| `munsu worktree get\|return\|status` | Manage pooled git worktrees. |

## Harness

| Command | Description |
|---------|-------------|
| `munsu harness detect` | Detect the running agent harness. |
| `munsu harness crew` | Resolve crew harness. |
| `munsu harness second` | Resolve second harness. |

## Helpers

| Command | Description |
|---------|-------------|
| `munsu ensure-agents-md <project>` | Ensure project AGENTS.md / CLAUDE.md symlink and self-governance section. |
| `munsu completion <shell>` | Generate shell autocompletion script. |
| `munsu help [command]` | Help about any command. |
