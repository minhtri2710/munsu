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
| `munsu worktree reclaim` | Reclaim orphaned worktrees not claimed by task metadata or authoritative task bindings; refuses when either source cannot be read. |
| `munsu harness detect` | Detect the running coding-agent harness. |
| `munsu harness soldier|captain` | Resolve the configured harness for a rank. |

## Session / Fleet

| Command | Description |
|---------|-------------|
| `munsu session-start` | Lock, bootstrap, ensure watcher for in-flight work, and print the session-start digest. |
| `munsu fleet snapshot` | Compact fleet state snapshot with aggregate counts. |
| `munsu fleet sync [<project>]` | Clone or pull project repos. |
| `munsu fleet view` | See the full fleet. |
| `munsu dashboard` | Full-screen fleet dashboard: header chips (watcher beat, captains, working, unacked wakes), failed homes named, a seat table grouped by home (general, then each captain; Human-needed rows first in a group), event feed, and confirmed actions that run the existing munsu commands. Needs a terminal; use `munsu fleet view` otherwise. |
| `munsu home [--mkdir]` | Print or create the munsu home directory. |
| `munsu inbox` | Show queued and claimed wakes and last captain status lines (General convenience view). |

## Spawn / Send / Peek

| Command | Description |
|---------|-------------|
| `munsu spawn <id> [--backend herdr\|tmux] [--yolo]` | Launch a soldier agent in a worktree+herdr/tmux window in the task's project and ensure the watcher; delivery mode is derived from the project's configured forge step. |
| `munsu send <id> "<line>"` | Send a mailbox command downlink to a Soldier or Captain endpoint; uplink to General is refused. |
| `munsu report <state> "<msg>" [--key <slug>]` | Report status up the hierarchy: one marked line in the parent's home, which becomes a `report` wake. |
| `munsu peek <id> [--lines N]` | Read last N lines of soldier pane output (default 40). |

## Watch / Wake / Guard

| Command | Description |
|---------|-------------|
| `munsu watch` | Persistent watcher daemon; started by `munsu watch ensure`, never run directly by agents. |
| `munsu watch ensure [--restart]` | Start or restart the persistent watcher. |
| `munsu watch run` | Run one diagnostic cycle. |
| `munsu watch status` | Show bounded watcher health without entering daemon mode. |
| `munsu watch stop` | Stop the watcher idempotently. |
| `munsu wake claim --consumer <id> [--lease-seconds 60] [--limit 10]` | Claim a batch of queued wakes under a lease. Takes no positional argument; `--consumer` is required. |
| `munsu wake ack <lease-id> <event-id...>` | Acknowledge one or more processed wakes. |
| `munsu wake resolve --claim-id <lease-id> --event-id <event-id> --summary <text>` | Resolve one claimed wake with durable evidence; the command a delivered `[mu-system:wake]` prompt tells the General to run. |
| `munsu guard` | Report fleet guard conditions, or act as a harness Stop-hook guard. |

## Captain

| Command | Description |
|---------|-------------|
| `munsu captain list` | List registered captains. |
| `munsu captain seed|launch|retire|list` | Manage the core Captain lifecycle. |
| `munsu captain converge` | Validate each Captain's registry entry, retry pending nudges, fast-forward Captain homes when safe, push inherited config, refresh charters, check liveness (relaunching only when absence is proven), reconcile pending config-reread, continuity and mailbox records, and report watcher status. |
| `munsu captain handoff|config-push` | Hand off queued tasks or push inherited config. |
| `munsu captain recover|update|validate` | Recover, fast-forward, or validate Captain homes. |

## Task / Backlog

| Command | Description |
|---------|-------------|
| `munsu task add <id> "<desc>" [--kind ship\|scout\|review] [--reviews <ship-id>] [--repo <name>]` | Register a queued task and write its brief. |
| `munsu task list` | List tasks from the canonical Task Authority. |
| `munsu task show <id>` | Show a task: canonical state, meta, `pane_alive`, and the PR the status log reports (`reported: PR <url> (unverified)`), when one is known. |
| `munsu task start <id>` | Mark a queued task working without spawning it; `munsu spawn` does this itself. |
| `munsu task block <id>` | Block a task on a dependency. |
| `munsu task unblock <id>` | Mark a blocked task queued again. |
| `munsu task done <id>` | Mark a task as done. |
| `munsu task reopen <id>` | Reopen a terminal task as a new generation and rewrite its brief. |
| `munsu task observe <task-id> [--fields description,branch,pane_alive]` | Observe one task using the orchestration contract. |
| `munsu promote <id>` | Promote a scout task to ship and rewrite its brief as a ship brief. |
| `munsu teardown <id> [--force]` | Tear down a soldier by its task ID. |

## Delivery

| Command | Description |
|---------|-------------|
| `munsu delivery review-diff <id>` | Review the diff between the task branch and its base. |
| `munsu delivery merge-status <id>` | Query merge status of the recorded delivery identity. |
| `munsu delivery pr-merge <id> <pr-url>` | Merge a PR through the journaled delivery execution; a landed merge marks the task done and tears the soldier down. |

## Event / Stow / Decision Hold

| Command | Description |
|---------|-------------|
| `munsu event append --type <type> [--producer <id>] [--key <slug>] [--json <payload>]` | Append a typed event to the event log. |
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
| `munsu skill show <name>` | Print every file of a skill, SKILL.md and its companions. |

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
| `munsu backend capabilities [--backend herdr\|tmux]` | Show supported operations for one session backend. |

## Orchestration contract

| Command | Description |
|---------|-------------|
| `munsu capabilities` | Show agent-facing orchestration capabilities (contract). |
| `munsu task observe <task-id>` | Observe one task using the orchestration contract. |
| `munsu fleet snapshot` | Compact fleet state snapshot with aggregate counts. |
| `munsu guard` | Report fleet guard conditions. |
| `munsu event append` | Append a typed event to the event log. |
| `munsu backend capabilities` | Show supported operations for one session backend. |
