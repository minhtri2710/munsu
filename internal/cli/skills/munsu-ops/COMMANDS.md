# munsu-ops — command map

Command names match `munsu --help` output verbatim. All commands accept `--home` (or `MUNSU_HOME`).

---

## Init / Config

| Command | Description |
|---------|-------------|
| `munsu init` | Create a munsu home directory. |
| `munsu config <key>` | Get or set a config value. |
| `munsu bootstrap` | Detect toolchain and run setup sweeps. |
| `munsu doctor` | Toolchain diagnostics with actionable fix suggestions. |

## Project / Worktree / Harness

| Command | Description |
|---------|-------------|
| `munsu project add <name> [--repo <url>]` | Register a project. |
| `munsu project list` | List registered projects. |
| `munsu project mode <name> [--kind ship\|scout]` | Show or resolve delivery mode. |
| `munsu worktree add <path>` | Add and track an existing worktree. |
| `munsu worktree list` | List tracked worktrees. |
| `munsu worktree release <path>` | Release a worktree. |
| `munsu harness detect` | Detect your current coding-agent harness. |
| `munsu harness list` | List known adapters. |

## Session / Fleet

| Command | Description |
|---------|-------------|
| `munsu session-start` | Lock, bootstrap, ensure watcher for in-flight work, and print the session-start digest. |
| `munsu fleet snapshot --version 2` | Compact fleet state snapshot with aggregate counts. |
| `munsu fleet sync [<project>]` | Clone or pull project repos. |
| `munsu fleet view` | See the full fleet. |
| `munsu fleet bearings` | Compact resume report (snapshot + captain table). |
| `munsu home [--mkdir]` | Print or create the munsu home directory. |
| `munsu inbox` | Show actionable wakes and last captain status lines (General convenience view). |

## Spawn / Send / Peek

| Command | Description |
|---------|-------------|
| `munsu spawn <id> <project> [--kind ship\|scout] [--mode no-mistakes\|direct-PR\|local-only] [--backend tmux\|herdr] [--yolo]` | Launch a soldier agent in a worktree+tmux/herdr window. |
| `munsu send <id> "<line>"` | Send a line to a soldier endpoint (downlink only). |
| `munsu report <state> "<msg>" [--key <slug>]` | Report status up the hierarchy (rank-aware uplink). |
| `munsu notify <state> "<msg>"` | Alias for 'munsu report'. |
| `munsu peek <id> [--lines N]` | Read last N lines of soldier pane output (default 40). |
| `munsu soldier-state <id>` | Read soldier current state (meta + pane liveness + status log). |

## Watch / Wake / Guard

| Command | Description |
|---------|-------------|
| `munsu watch` | Run the persistent watcher daemon. |
| `munsu watch ensure [--restart]` | Start or restart the persistent watcher. |
| `munsu watch run` | Run one diagnostic cycle. |
| `munsu watch stop` | Stopping the watcher (SIGTERM). |
| `munsu wake claim <consumer-id> [--lease-seconds 60] [--limit 10]` | Claim a batch of pending wakes under a lease. |
| `munsu wake ack <lease-id> <event-id...>` | Acknowledge one or more processed wakes. |
| `munsu wake-drain` | Drain all pending wakes (legacy, no lease management). |
| `munsu guard` | Report fleet guard conditions, or act as a harness Stop-hook guard. |

## Captain

| Command | Description |
|---------|-------------|
| `munsu captain list` | List registered captains. |
| `munsu captain seed <id> <home> [--charter <text>]` | Create a new captain home with charter and provenance marker. |
| `munsu captain launch <id>` | Launch a seeded captain in a new pane. |
| `munsu captain retire <id>` | Retire a captain (teardown pane, remove marker). |
| `munsu captain converge` | Flush send outboxes, retry nudges, push config. |
| `munsu captain handoff <id> <new-home>` | Hand off a captain to a different home. |
| `munsu captain config-push <id>` | Push inherited config from parent to one captain. |

## Task / Brief / Backlog

| Command | Description |
|---------|-------------|
| `munsu backlog add <id> "<desc>" [--kind ship\|scout\|task] [--repo <name>] [--start]` | Register a task. |
| `munsu backlog list` | List all backlog entries. |
| `munsu backlog show <id>` | Show a backlog entry. |
| `munsu backlog block <id>` | Block a task on a dependency. |
| `munsu backlog ready <id>` | Mark a blocked task as ready again. |
| `munsu backlog done <id>` | Mark a task as done in backlog. |
| `munsu task observe <id> [--fields description,branch,pane_alive,no_mistakes_step]` | Observe one task using the orchestration contract. |
| `munsu brief <id> <repo> [--scout]` | Scaffold a task brief. |
| `munsu soldier-state <id>` | Read soldier current state. |
| `munsu promote <id>` | Promote a scout task to ship. |
| `munsu teardown <id> [--force]` | Tear down a soldier by its task ID. |

## Delivery

| Command | Description |
|---------|-------------|
| `munsu delivery pr-check <id> <pr-url>` | Record a PR URL for a task. |
| `munsu delivery pr-merge <id> <pr-url>` | Merge a PR when instructed. |
| `munsu delivery merge-local <id>` | Fast-forward merge a task branch to the local default branch. |

## Event / Stow / Decision Hold

| Command | Description |
|---------|-------------|
| `munsu event append <event-id> --type <type> [--producer <id>] [--key <slug>] [--json <payload>]` | Append a typed event to the event log. |
| `munsu stow [text...]` | Capture durable learnings. |
| `munsu stow --general [text...]` | Capture captain preference. |
| `munsu stow --kind general [text...]` | Same as --general. |
| `munsu decision-hold list [--task <id>]` | List decision holds. |
| `munsu decision-hold resolve <hold-id> [rationale...]` | Resolve a decision hold. |

## Integrate / Update / Skill

| Command | Description |
|---------|-------------|
| `munsu integrate install [--harness <name>]` | Install munsu integrate extension for a harness. |
| `munsu integrate repair [--harness <name>]` | Check or repair a munsu integrate installation. |
| `munsu integrate status [--harness <name>]` | Show integration state for a harness. |
| `munsu update` | Fast-forward the munsu install root from origin. |
| `munsu skill list` | Show available skill names. |
| `munsu skill show <name>` | Display a skill's SKILL.md content. |

## AFK (Away Mode)

| Command | Description |
|---------|-------------|
| `munsu afk` | Enter away-mode supervision. |
| `munsu afk drain --consumer <id>` | One General drain cycle. |
| `munsu afk return` | Perform ordered AFK daemon shutdown. |
| `munsu afk return check` | Check if actionable AFK state remains. |

## Capabilities / Backend

| Command | Description |
|---------|-------------|
| `munsu capabilities` | Show agent-facing orchestration capabilities. |
| `munsu backend capabilities [--backend tmux\|herdr]` | Show supported operations for one session backend. |

## Orchestration contract

| Command | Description |
|---------|-------------|
| `munsu capabilities` | Show agent-facing orchestration capabilities (contract. |
| `munsu task observe <id>` | Observe one task using the orchestration contract. |
| `munsu fleet snapshot --version 2` | Compact fleet state snapshot with aggregate counts. |
| `munsu guard` | Report fleet guard conditions. |
| `munsu event append` | Append a typed event to the event log. |
| `munsu backend capabilities` | Show supported operations for one session backend. |
