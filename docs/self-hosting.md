# Self-Hosting: running munsu as its own orchestrator

> How to use munsu to manage munsu itself — init, arm, supervise, and
> deliver PRs without firstmate.

## 1. Init a dedicated home

A self-hosted munsu should use an isolated home, separate from the
live secondmate home that firstmate supervises.

```sh
export MUNSU_HOME=~/.munsu-selfhost
munsu init                        # creates tree + seeds config + skills
munsu doctor                      # verify toolchain
```

At this point the home directory exists with `state/`, `data/`, `config/`,
and `projects/`.

Now register the munsu repo itself as a project so spawn can find it:

```sh
munsu project add munsu <absolute-path-to-munsu-repo>
```

**Detect backend:** `munsu config get backend` prints the detected session
backend (tmux or herdr). If it shows `herdr` the HERDR_ENV is active; this
is the normal mode when running inside a Herdr-aware agent.

## 2. Arm the watcher

Before spawning any crewmates, arm the event-driven watcher. The watcher
runs in the background as a singleton child process, polled every 5s, and
exits with a wake reason when an actionable event is found.

```sh
munsu watch-arm                    # launch background watcher
```

With `--restart` if already running:

```sh
munsu watch-arm --restart
```

**Verify:** `munsu guard` should produce no `WARNING` about a stale or
missing watcher.

### Guard warning behavior

`munsu guard` runs automatically as a persistent pre-run hook on most
commands. It checks two conditions:

| Condition | Check | Warns when |
|-----------|-------|------------|
| Stale watcher | Liveness beat in `state/.last-watcher-beat` | Beat is missing or older than 5m |
| Project tangle | Default branch in primary checkout | Non-default branch detected |

The guard is **fail-open**: it prints a warning to stderr but never blocks
the command. Silence with `MUNSU_GUARD_SKIP=1`.

### Watcher beat format

The watcher liveness beat is stored in `state/.last-watcher-beat` with format:

```
<unix_epoch_seconds> <pid>
```

- **Content-timestamp drives staleness** — `ReadBeatStatus` reads the Unix
  epoch from the file content and compares it to `time.Now()`.
- `touch -t` alone does **not** produce a valid beat — the file content must
  contain a valid epoch+pid pair.
- If the content cannot be parsed, the beat is treated as missing
- A missing beat file reports `Age: 0` (signaling never existed) rather than
  the stale threshold.
When the guard warns about in-flight tasks with a stale watcher, re-arm
with `munsu watch-arm --restart` before resuming spawn/send operations.

## 3. Session start equivalent

`munsu session-start` is designed for first-mate-of-day orchestration
scenarios. For self-hosting, run it to get the session digest (bootstrap
diagnostics, context, fleet state, supervision reminder):

```sh
munsu session-start
```

In a pure self-hosted loop the digest is still useful — it confirms that
all toolchain components are ready and prints the current fleet state.

## 4. Spawn, supervise, interact

### Register work

```sh
munsu backlog add <task-id> "<description>" --kind ship --repo munsu --start
munsu brief <task-id> munsu                     # scaffold brief
# fill in {TASK} placeholder in data/<task-id>/brief.md
```

### Spawn crewmate

```sh
munsu spawn <task-id> munsu --arm               # spawn + arm watcher
```

The spawn flow:
1. Tangle check (skippable with `--yolo`)
2. Worktree acquisition via treehouse pool
3. Harness detection (pi, claude-code, etc.)
4. Launch template from `config/crew-dispatch.json`
5. Session window creation
6. Meta write to `state/<task-id>.meta`

With `--arm`, the watcher is armed automatically after spawn.

### Monitor and steer

```sh
munsu peek <task-id> [--lines N]                # read crewmate output
munsu crew-state <task-id>                      # ground truth state
munsu send <task-id> "<instruction>"            # steer the crewmate
```

### Wake handling

When the watcher fires, drain wakes:

```sh
munsu wake-drain                                # process all queued wakes
munsu crew-state <task-id>                      # read ground truth
```

If tasks are still in flight, re-arm:

```sh
munsu watch-arm
```

### Deliver

```sh
munsu delivery pr-check <task-id> <pr-url>      # record PR
munsu delivery pr-merge <task-id> <pr-url>      # merge PR
# or for local-only:
munsu delivery merge-local <task-id>            # fast-forward merge
```

### Teardown

```sh
munsu teardown <task-id>                        # safety-gated teardown
munsu teardown <task-id> --force                # skip safety checks, removes data/<id>/
munsu backlog done <task-id>                    # close the backlog item
```

Without --force, scout teardown requires report.md and no unresolved decision
holds. Ship teardown requires clean git state with a remote tracking branch.
With --force, all safety checks are bypassed and the data/<id>/ directory
(including report.md and brief.md) is removed.

## 5. Decision-hold scout gate

Scout tasks (`--kind scout`) investigate a question rather than shipping
code. When a scout finds a decision the captain must make, it records a
structured hold before completing.

### Recording a hold (Phase A — protocol only)

```sh
munsu task status <scout-id> "needs-decision" "<key>: <summary>"
munsu backlog block <dependent-id> --by <scout-id>
```

### Recording the captain's answer

```sh
munsu task status <scout-id> "resolved" "<key>: <answer>"
munsu backlog ready <dependent-id>
```

### Scout teardown with open holds

Teardown **warns but does not block** when the scout's meta contains
a `needs-decision` state in its status log. The operator must explicitly
decide to proceed:

```sh
munsu teardown <scout-id>          # warns about unresolved holds
munsu teardown <scout-id> --force  # bypass hold warning
```

**Design principle:** The scout gathers evidence. The captain decides.
Holds ensure the decision is tracked, not lost, when the scout session
ends.

## 6. Fleet management

```sh
munsu fleet view                    # snapshot + render all tasks
munsu fleet sync                    # fast-forward project clones
munsu guard                         # post-action safety check
munsu afk                           # away-mode supervision daemon
```

## 7. Guard supervision loop summary

```
          ┌──────────────────┐
          │  munsu watch-arm │  (background daemon)
          └────────┬─────────┘
                   │ every 5s
          ┌────────▼─────────┐
          │  Touch beat      │  state/.last-watcher-beat
          │  Scan tasks      │  state/<id>.meta
          │  Check liveness  │  session backend (tmux/herdr)
          │  Check staleness │  >5 min idle → "stale" wake
          └────────┬─────────┘
                   │ wake detected
          ┌────────▼─────────┐
          │  munsu wake-drain│  read + clear wake queue
          │  munsu crew-state│  ground truth per task
          └────────┬─────────┘
                   │ re-arm if tasks in flight
          ┌────────▼─────────┐
          │  munsu watch-arm │  (if not already running)
          │  munsu guard     │  post-action safety check
          └──────────────────┘
```

## 8. Safety rules for self-hosting

1. **Isolated home:** Always use a dedicated home (e.g. `~/.munsu-selfhost`)
   for self-hosting. Never share the live secondmate home that firstmate
   supervises.
2. **Never flip live secondmate:** Do not run `munsu watch-arm` inside the
   live secondmate home (`~/.munsu` if it is supervised by firstmate).
3. **Watcher singleton:** Only one watcher process per home. The flock lock
   at `state/.lock` enforces this.
4. **Guard after every action:** Run `munsu guard` after spawn, teardown,
   and fleet actions.
5. **Isolated session/workspace:** Use a dedicated herdr workspace or tmux
   session scoped to each home (munsu auto-derives from home path). Avoid
   sharing a session/workspace across test homes to prevent interference.
6. **Temp homes for dogfood:** Use temporary directories for exploratory
   dogfood tests. Example: `mkdir -p /tmp/munsu-dogfood && MUNSU_HOME=/tmp/munsu-dogfood munsu init`.
   Note: leftover `/tmp/munsu-* watch` processes from prior tests can trigger stale-watcher
   warnings in a new test home; stop them with `munsu watch stop` in the original home or kill the PID.
