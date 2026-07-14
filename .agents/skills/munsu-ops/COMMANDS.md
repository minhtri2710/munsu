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
| `munsu session-start` | Lock, bootstrap, wake-drain, digest fleet state. |
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
| `munsu spawn <id> <project> [--kind ship\|scout] [--mode no-mistakes\|direct-PR\|local-only] [--yolo]` | Launch a crewmate agent in a worktree+tmux/herdr window. |
| `munsu send <id> "<line>"` | Send a line to a crewmate endpoint. |
| `munsu peek <id> [--lines N]` | Read last N lines of crewmate pane output (default 40). |
| `munsu crew-state <id>` | Read crewmate current state (meta + pane liveness + status log). |

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
| `munsu pr-check <id> <pr-url>` | Record PR URL and head SHA in task meta; write check.sh for merge polling. |
| `munsu pr-merge <id> <pr-url> [-- --merge\|--rebase]` | Merge a PR via gh-axi CLI (default: squash). |
| `munsu merge-local <id>` | Fast-forward merge crewmate branch to local default branch (local-only mode only). |
| `munsu review-diff <id>` | Print diff summary between crewmate branch and base. |
| `munsu promote <id>` | Promote a scout task to ship. |

## Teardown

| Command | Description |
|---------|-------------|
| `munsu teardown <id> [--force]` | Tear down a crewmate with dirty/remote/report safety checks. |

## Secondmate

| Command | Description |
|---------|-------------|
| `munsu secondmate launch` | Launch a secondmate in its home. |
| `munsu secondmate seed` | Seed a secondmate home with charter. |
| `munsu secondmate list` | List registered secondmates. |
| `munsu secondmate handoff` | Hand off backlog items to a secondmate. |
| `munsu secondmate config-push` | Push inheritable config to a secondmate. |
| `munsu secondmate retire` | Retire a secondmate. |

## Fleet / Diagnostics

| Command | Description |
|---------|-------------|
| `munsu fleet-view` | Render fleet view from snapshot. |
| `munsu fleet-snapshot` | Emit fleet snapshot JSON. |
| `munsu fleet-sync` | Fast-forward refresh project clones. |
| `munsu bearings` | Compact resume report of fleet state. |
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
| `munsu harness crew` | Resolve crewmate harness. |
| `munsu harness secondmate` | Resolve secondmate harness. |

## Helpers

| Command | Description |
|---------|-------------|
| `munsu ensure-agents-md <project>` | Ensure project AGENTS.md / CLAUDE.md symlink and self-governance section. |
| `munsu completion <shell>` | Generate shell autocompletion script. |
| `munsu help [command]` | Help about any command. |
