# Supervision: Watch / Wake-Drain / Guard / AFK

## Overview

The supervision subsystem monitors soldier sessions for lifecycle events. It
replaces the need for polling from the orchestrator — the watcher runs in the
background, detects stale or completed tasks, and exits with a wake reason that
the orchestrator can act on.

## How the watcher works

`munsu watch` runs an event-driven poll loop:

```
watcher loop (every 5s):
  1. Touch liveness beat at state/.last-watcher-beat
  2. Scan all tasks in state/<id>.meta
  3. For each task, check:
     a. Pane liveness via session backend (tmux/herdr)
     b. Status log staleness (> 5 min idle)
  4. On first stale detection → emit a "stale" wake
  5. After 3 consecutive stale polls (~15s) → demand deep inspection
  6. If wake queue exists → emit a "signal" wake
```

The watcher is singleton-safe via a home-scoped `flock` lock at `state/.lock`.
Only one watcher process may run per `~/.munsu` home at a time. A SIGTERM/SIGINT
handler ensures graceful cleanup (lock release) on interruption.

### Stale absorption

Tasks running in the no-mistakes pipeline (run-step one of `running`, `fixing`,
`ci`, `fix_review`, `awaiting_approval`) absorb stale signals — even if the
session pane appears dead or idle, the watcher will not raise a stale wake for
that task. This prevents false wake-ups during long-running CI builds.

### Stale streaks

The watcher tracks consecutive stale polls per task. After 3 consecutive stale
polls (~15s), the wake reason is marked with `DemandDeepInspection = true`,
signalling that the orchestrator should inspect more thoroughly before deciding
what to do.

## Watch arm

`munsu watch-arm [--restart]` launches the watcher as a background child process
(via `os/exec`). With `--restart`, it signals any existing watcher via the liveness
beat PID before starting a new one.

## Liveness beat

The watcher writes a timestamp + PID to `state/.last-watcher-beat` at every
poll tick. Guard commands read this beat to determine if the watcher is alive.
The stale threshold is 300 captains (5 minutes) — if the beat is older than this,
the watcher is considered dead.

## Wake drain

`munsu wake-drain` reads and removes all entries from the durable wake queue at
`state/.wake-queue`. Each record contains:

| Field   | Description                    |
|---------|--------------------------------|
| Epoch   | Unix timestamp of enqueue      |
| Seq     | PID of enqueuing process       |
| Kind    | Wake kind (signal, stale, etc) |
| Key     | Task or context identifier     |
| Payload | Arbitrary message              |

The waker package (`internal/waker`) provides `EnqueueWake` for producers and
`Drain` for consumers.

## Guard

`munsu guard` checks two conditions:

1. **Stale watcher beat** — warns if the liveness beat is older than 5 minutes
   (indicating the watcher may have died without cleanup).
2. **Project tangle** — checks each registered project's primary checkout is not
   on a non-default branch. A tangle means the project is on a feature branch in
   the primary checkout instead of using an isolated worktree, which can cause
   interference between soldiers.

## AFK mode

`munsu afk` starts an away-mode sub-supervisor daemon that:

- Sets an AFK flag in the home directory (absorbed by the session-start
  bootstrap to skip heavy setup)
- Polls the fleet at a reduced cadence
- Prints general-relevant events (done, failed, needs-decision)
- Stops gracefully on SIGTERM/SIGINT (clears the AFK flag on exit)

## Key paths

All paths are relative to `$MUNSU_HOME` (default `~/.munsu`).

| Path | Purpose |
|------|---------|
| `state/.lock` | Flock-based singleton lock for watcher |
| `state/.last-watcher-beat` | Liveness timestamp + PID |
| `state/.wake-queue` | Durable wake event queue |
| `state/<id>.meta` | Per-task metadata |
| `state/<id>.status` | Per-task status log |
