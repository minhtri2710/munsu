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
| `munsu spawn <id> <project> [--kind ship|scout] [--mode no-mistakes|direct-PR|local-only] [--backend tmux|herdr] [--yolo]` | Launch a soldier agent in a worktree+tmux/herdr window. |
| `munsu send <id> "<line>"` | Send a line to a soldier endpoint. |
| `munsu peek <id> [--lines N]` | Read last N lines of soldier pane output (default 40). |
| `munsu soldier-state <id>` | Read soldier current state (meta + pane liveness + status log). |

## Watch / Wake / Guard

| Command | Description |
|---------|-------------|
| `munsu watch` | Run the persistent watcher daemon (singleton-safe, home-scoped lock). Queues actionable wakes until stopped. |
| `munsu watch ensure [--restart]` | Ensure the watcher daemon is healthy (home-scoped, idempotent). |
| `munsu wake-drain` | Drain queued wakes (legacy pull-based).
| `munsu wake claim <consumer-id>` | Claim a batch of pending wakes with lease management (preferred API).
| `munsu wake ack <lease-id> <event-id...>` | Acknowledge a processed wake to release its lease.
| `munsu guard` | Warn on tangle or stale watcher.

See `SUPERVISION.md` for the full watch/wake-drain/guard/afk loop.

## Delivery

| Command | Description |
|---------|-------------|
| `munsu delivery pr-check <id> <pr-url>` | Record PR URL and head SHA in task meta; write check.sh for merge polling. |
| `munsu delivery pr-merge <id> <pr-url> [-- --merge\|--rebase]` | Merge a PR via gh-axi CLI (default: squash). |
| `munsu delivery merge-local <id>` | Fast-forward merge soldier branch to local default branch (local-only mode only). |
| `munsu delivery review-diff <id>` | Print diff summary between soldier branch and base. |
| `munsu promote <id>` | Promote a scout task to ship. |

## Teardown

| Command | Description |
|---------|-------------|
| `munsu teardown <id> [--force]` | Tear down a soldier with dirty/remote/report safety checks. |

## Captain

| Command | Description |
|---------|-------------|
| `munsu captain launch <captain-home>` | Launch a captain in its home.
| `munsu captain list` | List registered captains.
| `munsu captain handoff <captain-home> <item-key...>` | Hand off backlog items to a captain.
| `munsu captain config-push <captain-home>` | Push inheritable config to a captain.
| `munsu captain converge` | Converge all registered captains (flush outbox, retry nudges, liveness).
| `munsu captain recover [captain-home]` | Probe liveness and relaunch launched-but-dead endpoints.
| `munsu captain retire [--force] <captain-home>` | Retire a captain.

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
| `munsu harness soldier` | Resolve soldier harness. |
| `munsu harness captain` | Resolve captain harness. |

## Helpers

| Command | Description |
|---------|-------------|
| `munsu ensure-agents-md <project>` | Ensure project AGENTS.md / CLAUDE.md symlink and self-governance section. |
| `munsu completion <shell>` | Generate shell autocompletion script. |
| `munsu help [command]` | Help about any command. |
