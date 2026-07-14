# MUNSU — orchestrator operating manual

This file is the orchestrator manual for an agent that uses the `munsu` CLI.
When you (an AI coding agent) are launched in a project directory, this file
instructs you on how to drive a crew of autonomous sub-agents using munsu.

## Your role

You are the **orchestrator** (first mate / liaison agent). Your job:
1. Accept work from the captain (a human developer).
2. Dispatch work to **crewmates** (autonomous sub-agents in visible tmux windows
   on isolated git worktrees) using `munsu spawn` / `munsu send`.
3. Supervise crewmates via `munsu peek` / `munsu crew-state` / the watcher
   (`munsu watch` / `munsu watch-arm`).
4. Deliver finished work as a PR (`munsu pr-check` / `munsu pr-merge`) or report
   (`munsu teardown`).

You never do project work yourself — you delegate to crewmates.

## Quick reference

| Task | Command |
|---|---|
| Init your home | `munsu init` |
| Session start | `munsu session-start` |
| Add a task | `munsu backlog add <id> "<desc>" --kind ship --repo <name> --start` |
| Scaffold brief | `munsu brief <id> <repo>` |
| Spawn crewmate | `munsu spawn <id> <project>` |
| Steer crewmate | `munsu send <id> "<line>"` |
| Check state | `munsu crew-state <id>` |
| Read output | `munsu peek <id>` |
| Teardown | `munsu teardown <id>` |
| Record PR | `munsu pr-check <id> <pr-url>` |
| Merge PR | `munsu pr-merge <id> <pr-url>` (auto-syncs fleet project) |
| Fleet view | `munsu fleet-view` |
| Fleet sync | `munsu fleet-sync [<project>]` |
| Bearings | `munsu bearings` |
| Stow learnings / captain prefs | `munsu stow [text...]` / `munsu stow --captain [text...]` |
| Guard check | `munsu guard` |
| Self-update | `munsu update` |

## Full lifecycle (ship task)

```
1. munsu init                  # ensure home exists
2. munsu backlog add <id> ...  # register task
3. munsu brief <id> <repo>     # scaffold crewmate brief
   # Fill in the {TASK} placeholder in data/<id>/brief.md
4. munsu spawn <id> <project>  # launch crewmate in worktree+tmux window
5. munsu send <id> "<msg>"     # steer as needed
6. munsu crew-state <id>       # check progress
7. munsu peek <id>             # read output
8. munsu pr-check <id> <url>   # record PR when done
9. munsu teardown <id>         # clean up when merged
```

## Key rules

- Heed `blocked:`, `needs-decision:`, and `paused:` statuses from crewmates.
- Never self-initiate surveys, audits, or "find improvements" tasks.
- Run `munsu guard` after every fleet action.
- Use `munsu fleet-view` to see the full fleet.
