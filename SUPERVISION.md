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
  2. Scan state/*.status for general-relevant last lines (done/failed/blocked/...)
     including Captain return-channel files state/captain:<id>.status
     → enqueue Kind=signal (even while the captain pane is alive)
  3. Scan all tasks in state/<id>.meta not already signaled
  4. For each remaining task, check:
     a. Pane liveness via session backend (tmux/herdr)
     b. Status log staleness (> 5 min idle)
  5. On first stale detection → emit a "stale" wake
  6. After 3 consecutive stale polls (~15s) → demand deep inspection
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

### Check plugins

The watcher discovers per-task `.check` files in `state/` and shared checks in
`state/checks/`. A check must be a regular, non-symlink file whose content starts
with a shebang (`#!`). On Unix, the file must also have the owner-execute mode
bit. Windows does not provide executable mode bits through Go and does not
require a Windows executable extension for these scripts, so a valid shebang is
the executability gate there. Empty, unreadable, non-regular,
symlinked, or missing-shebang checks are refused on every platform. Retirement
polling and the supervision watcher use this same validator, so both paths apply
these platform-specific rules consistently.

## Watch arm

`munsu watch-arm [--restart]` launches the watcher as a background child process
(via `os/exec`). With `--restart`, it signals any existing watcher via the liveness
beat PID before starting a new one.

## Liveness beat

The watcher writes a timestamp + PID to `state/.last-watcher-beat` at every
poll tick. Guard commands read this beat to determine if the watcher is alive.
The stale threshold is 300 seconds (5 minutes) — if the beat is older than this,
the watcher is considered dead.

## Return channel closed loop

General does not read Captain chat. The operable path is:

1. **Marked send** — `munsu send <captain-id> "..."` prefixes `[mu-from-general]` + U+2063 for `kind=captain`.
2. **Captain status** — Captain appends one line to General home `state/captain:<id>.status` (charter return channel).
3. **Signal wake** — watcher `RunCycle` / `munsu watch run` enqueues `kind=signal` for general-relevant last lines.
4. **Claim** — General leases wakes with `munsu wake claim --consumer <id>`.

Hermetic proof: `TestReturnChannelClosedLoop` in `internal/orchestrator` (no live pane required).

Operator checklist (live home):

```sh
# 1) Send (marks automatically when meta kind=captain)
munsu send captain:munsu "report status on <task>"

# 2) Captain (or operator) appends parent status
echo "done [key=<task>]: one short line" >> "$MUNSU_HOME/state/captain:munsu.status"

# 3) One watcher cycle (daemon does this every 5s)
munsu watch run

# 4) Lease the signal
munsu wake claim --consumer general --output json
```

## Wake drain / claim

`munsu wake claim --consumer <id>` leases entries from the durable wake queue at
`state/.wake-queue` without destroying unacked ownership semantics.

`munsu wake claim` reads entries from that queue under a lease.
Each record contains:

| Field   | Description                    |
|---------|--------------------------------|
| Epoch   | Unix timestamp of enqueue      |
| Seq     | PID of enqueuing process       |
| Kind    | Wake kind (signal, stale, etc) |
| Key     | Task or context identifier     |
| Payload | Arbitrary message              |

The lifecycle package provides `EnqueueWake` for producers and
`ClaimWakes` / `DrainWakes` for consumers. The orchestrator `waker` wraps drain helpers.

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
| `state/<id>.status` | Per-task append-only event log (not sole current state; use `soldier-state`) |
