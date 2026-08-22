# munsu-ops — command map

Command names match `munsu --help` output verbatim. All commands accept `--home` (or `MUNSU_HOME`).

This is a curated subset, not an exhaustive index: `munsu` registers more commands
than are listed here. Run `munsu --help` (and `munsu <command> --help` per group)
for the complete registered set.

---

## Init / Config

| Command | Description |
|---------|-------------|
| `munsu init` | Create a munsu home directory. |
| `munsu config get|set|show` | Read, write, or show resolved configuration values. |
| `munsu config dispatch` | Manage Soldier dispatch profiles. |
| `munsu bootstrap` | Detect toolchain and run setup sweeps. |
| `munsu doctor` | Toolchain diagnostics with actionable fix suggestions. |

## Project / Worktree / Harness

| Command | Description |
|---------|-------------|
| `munsu project add|list|show|rm` | Manage registered projects. |
| `munsu project mode <name>` | Resolve delivery mode for a project. |
| `munsu worktree get|return|status` | Acquire, return, or inspect pooled worktrees. |
| `munsu worktree reclaim` | Reclaim orphaned worktrees not referenced by task metadata. |
| `munsu harness detect` | Detect the running coding-agent harness. |
| `munsu harness soldier|captain` | Resolve the configured harness for a rank. |

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
| `munsu spawn <id> [<project>] [--kind ship\|scout] [--mode no-mistakes\|direct-PR\|local-only] [--backend tmux\|herdr] [--yolo]` | Launch a soldier agent in a worktree+tmux/herdr window. |
| `munsu send <id> "<line>"` | Send a mailbox command downlink to a Soldier or Captain endpoint; uplink to General is refused. |
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
| `munsu watch status` | Show bounded watcher health without entering daemon mode. |
| `munsu watch stop` | Stop the watcher idempotently. |
| `munsu wake claim --consumer <id> [--lease-seconds 60] [--limit 10]` | Claim a batch of queued wakes under a lease. Takes no positional argument; `--consumer` is required. |
| `munsu wake ack <lease-id> <event-id...>` | Acknowledge one or more processed wakes. |
| `munsu wake resolve --claim-id <lease-id> --event-id <event-id> --summary <text>` | Resolve one claimed wake with durable evidence. |
| `munsu guard` | Report fleet guard conditions, or act as a harness Stop-hook guard. |

## Captain

| Command | Description |
|---------|-------------|
| `munsu captain list` | List registered captains. |
| `munsu captain seed|launch|retire|list` | Manage the core Captain lifecycle. |
| `munsu captain converge` | Reconcile mailbox pending records, terminal receipts, nudges, and inherited config. |
| `munsu captain handoff|config-push` | Hand off queued tasks or push inherited config. |
| `munsu captain migrate|recover|update|validate` | Migrate, recover, fast-forward, or validate Captain homes. |

## Task / Brief / Backlog

| Command | Description |
|---------|-------------|
| `munsu task add <id> "<desc>" [--kind ship\|scout] [--repo <name>]` | Register a queued task. |
| `munsu task list` | List tasks from the canonical Task Authority. |
| `munsu task show <id>` | Show a task. |
| `munsu task start <id>` | Start a task (mark in-flight). |
| `munsu task block <id>` | Block a task on a dependency. |
| `munsu task unblock <id>` | Mark a blocked task queued again. |
| `munsu task done <id>` | Mark a task as done. |
| `munsu task reopen <id>` | Reopen a terminal task as a new generation. |
| `munsu task observe <task-id> [--fields description,branch,pane_alive,no_mistakes_step]` | Observe one task using the orchestration contract. |
| `munsu brief <id> <repo> [--scout]` | Scaffold a task brief. |
| `munsu soldier-state <id>` | Read soldier current state. |
| `munsu promote <id>` | Promote a scout task to ship. |
| `munsu teardown <id> [--force]` | Tear down a soldier by its task ID. |

## Delivery

| Command | Description |
|---------|-------------|
| `munsu delivery review-diff <id>` | Review the diff between the task branch and its base. |
| `munsu delivery merge-status <id>` | Query merge status of the recorded delivery identity. |
| `munsu delivery pr-merge <id> <pr-url> [--teardown]` | Merge a PR through the journaled delivery execution. |

## Event / Stow / Decision Hold

| Command | Description |
|---------|-------------|
| `munsu event append <event-id> --type <type> [--producer <id>] [--key <slug>] [--json <payload>]` | Append a typed event to the event log. |
| `munsu stow [text...]` | Capture durable learnings. |
| `munsu stow --general [text...]` | Capture General preferences in `data/general.md`. |
| `munsu stow --kind general [text...]` | Same as --general. |
| `munsu decision-hold hold <key> --reason <summary> --from <task-id>` | Record a new decision hold. |
| `munsu decision-hold list <origin-id>` | List unresolved decisions for an origin task. |
| `munsu decision-hold resolve <key> --answer <text> --from <origin-id> [--unblock <dep-id>...]` | Resolve a decision hold and unblock dependents. |

## Integrate / Update / Skill

| Command | Description |
|---------|-------------|
| `munsu integrate install [--harness <name>]` | Install munsu integrate extension for a harness. |
| `munsu integrate repair [--harness <name>]` | Check or repair a munsu integrate installation. |
| `munsu integrate status [harness]` | Show integration state for a harness. |
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
| `munsu capabilities` | Show agent-facing orchestration capabilities (contract). |
| `munsu task observe <task-id>` | Observe one task using the orchestration contract. |
| `munsu fleet snapshot --version 2` | Compact fleet state snapshot with aggregate counts. |
| `munsu guard` | Report fleet guard conditions. |
| `munsu event append` | Append a typed event to the event log. |
| `munsu backend capabilities` | Show supported operations for one session backend. |
