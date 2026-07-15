# munsu COMMANDS.md

Full command map grouped by lifecycle phase.

## Init and Setup

| Command | Description |
|---------|-------------|
| `munsu home [--mkdir]` | Print the munsu home directory (`~/.munsu`). With `--mkdir`, create the directory tree. |
| `munsu init` | Create home directory and seed the orchestrator operating manual. |
| `munsu config get <key>` | Read a configuration value. |
| `munsu config set <key> <value>` | Write a configuration value. |
| `munsu project add <name> <path-or-url>` | Register a project. Git URLs are cloned automatically. |
| `munsu project list` | List registered projects. |
| `munsu project show <name>` | Show project details. |
| `munsu project rm <name>` | Remove a registered project. |
| `munsu project mode <name>` | Resolve delivery mode for a project. |
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
| `munsu harness crew` | Resolve crewmate harness (dispatch.json > config/crew-harness > detected). |
| `munsu harness secondmate` | Resolve secondmate harness (config/secondmate-harness > config/crew-harness > detected). |

## Crewmate Lifecycle

| Command | Description |
|---------|-------------|
| `munsu spawn <id> <project> [--kind ship\|scout] [--mode no-mistakes\|direct-PR\|local-only] [--yolo] [--backend tmux\|herdr]` | Spawn a crewmate agent — acquire worktree, create session window, write task meta. |
| `munsu send <id> <line>` | Send a line to a crewmate session endpoint. |
| `munsu peek <id> [--lines N]` | Capture and print crewmate output (default 40 lines). |
| `munsu crew-state <id>` | Read crewmate current state (status, pane liveness, no-mistakes run-step). |
| `munsu brief <id> <repo> [--scout]` | Scaffold a task brief (ship or scout template). |
| `munsu teardown <id> [--force]` | Tear down a crewmate with safety checks. |
| `munsu promote <id>` | Promote a scout task to ship. |
| `munsu review-diff <id>` | Review diff between crewmate branch and base. |

## Supervision

| Command | Description |
|---------|-------------|
| `munsu watch` | Run the event-driven watcher loop. Singleton-safe via home-scoped lock. Exits with a wake reason when an actionable event is found. |
| `munsu watch-arm [--restart]` | Arm the watcher as a background process. With `--restart`, signal existing watcher first. |
| `munsu wake-drain` | Drain all queued wake records and print them. |
| `munsu guard` | Warn on tangle (non-default branch in primary checkout) or stale watcher beat. |
| `munsu afk` | Enter away-mode supervision daemon; polls at reduced cadence; stops on SIGTERM/SIGINT. |

## Fleet

| Command | Description |
|---------|-------------|
| `munsu fleet-sync [<project>]` | Fast-forward refresh project clones. |
| `munsu fleet-snapshot` | Emit fleet snapshot as JSON. |
| `munsu fleet-view` | Render fleet view from snapshot. |
| `munsu bearings [<project-dir>]` | Compact resume report. |
| `munsu secondmate seed <id> <home-path>` | Seed a secondmate home with charter. |
| `munsu secondmate launch <secondmate-home>` | Launch a secondmate in its home. |
| `munsu secondmate retire <secondmate-home>` | Retire a secondmate. |
| `munsu secondmate list` | List registered secondmates. |

## Delivery

| Command | Description |
|---------|-------------|
| `munsu pr-check <id> <pr-url>` | Record PR URL and SHA in task meta, write check.sh to poll merge status. |
| `munsu pr-merge <id> <pr-url> [-- --merge\|--rebase]` | Merge a PR via gh-axi CLI. Default method is squash. |
| `munsu merge-local <id>` | Fast-forward merge crewmate branch to local default branch (no-remote projects only). |

## Backlog

| Command | Description |
|---------|-------------|
| `munsu backlog add <id> <description> [--kind ship\|scout\|task] [--repo <name>] [--start]` | Add a task to the backlog. |
| `munsu backlog list [state-filter]` | List backlog items. |
| `munsu backlog show <id>` | Show backlog item details. |
| `munsu backlog start <id>` | Mark a backlog item as in-flight. |
| `munsu backlog done <id>` | Mark a backlog item as done. |
| `munsu backlog block <id>` | Block a backlog item. |
| `munsu backlog ready <id>` | Unblock a backlog item (mark ready). |
| `munsu backlog unblock <id>` | Alias for ready. |

Uses `tasks-axi` CLI when available (>= 0.1.1), falling back to hand-editing `$MUNSU_HOME/data/backlog.md`. Custom home paths force the manual backend.

## Task Meta

| Command | Description |
|---------|-------------|
| `munsu task add <id> <description> [--kind ship\|scout] [--repo <name>]` | Add a new task to local state. |
| `munsu task list [--state <filter>]` | List tasks (delegated to tasks-axi). |
| `munsu task show <id> [--full]` | Show task details with optional status log. |
| `munsu task status <id> <state> <message>` | Append a status line to a task. |

## Knowledge

| Command | Description |
|---------|-------------|
| `munsu stow [text...] [--kind learning\|captain] [--captain]` | Sweep session for durable knowledge. Uses inspect-then-update to avoid duplicates. |

## Maintenance

| Command | Description |
|---------|-------------|
| `munsu update` | Self-update munsu binary (fast-forward + rebuild with version stamp). |
