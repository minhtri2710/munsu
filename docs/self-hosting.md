# Self-Hosting: running munsu as its own orchestrator

> How to use munsu to manage munsu itself — init, arm, supervise, and
> deliver PRs.

## 1. Init a dedicated home

A self-hosted munsu should use an isolated home, separate from

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

**Set the general pane:** `config/general-pane` holds one pane handle in
`session:pane` form (for example `munsu-general:0.1`) — the General's own pane.
It is the target of the uplink notify path (`munsu report` from a captain) and
of wake dispatch: those are the paths that write there, only when the composer
is verified empty. `munsu doctor` lists it with `backend` as a required key; when
it is unset, resolution falls back to `TMUX_PANE` / `HERDR_PANE_ID`, and with
neither there is no target and nothing is delivered to a pane. The AFK daemon
does **not** read it — it diagnoses only (see [ADR-0013](adr/0013-afk-is-diagnosis-and-manual-action.md)).

## 2. Arm the watcher

Before spawning any soldiers, arm the event-driven watcher. The watcher
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
munsu task add <task-id> "<description>" --kind ship --repo munsu
munsu task start <task-id>
munsu brief <task-id> munsu                     # scaffold brief
# fill in {TASK} placeholder in data/<task-id>/brief.md
```

### Spawn soldier

```sh
munsu spawn <task-id> munsu --arm               # spawn + arm watcher
```

The spawn flow:
1. Tangle check (skippable with `--yolo`)
2. Worktree acquisition via treehouse pool
3. Harness detection (pi, claude-code, etc.)
4. Launch template from `config/soldier-dispatch.json`
5. Session window creation
6. Meta write to `state/<task-id>.meta`

With `--arm`, the watcher is armed automatically after spawn.

### Monitor and steer

```sh
munsu peek <task-id> [--lines N]                # read soldier output
munsu soldier-state <task-id>                      # ground truth state
munsu send <task-id> "<instruction>"            # steer the soldier
```

### Wake handling

When the watcher fires, drain wakes:

```sh
munsu wake claim --consumer <id>                # claim queued wakes under a lease
munsu soldier-state <task-id>                      # read ground truth
```

If tasks are still in flight, re-arm:

```sh
munsu watch-arm
```

### Deliver

```sh
munsu delivery pr-merge <task-id> <pr-url>      # merge PR
```

### Teardown

```sh
munsu task done <task-id>                       # close the task first: a retired
                                                # task can no longer be completed
munsu teardown <task-id>                        # safety-gated teardown
munsu teardown <task-id> --force                # skip safety checks
```

Without --force, scout teardown requires report.md and no unresolved decision
holds. Ship teardown requires clean git state with a remote tracking branch.
With --force, all safety checks are bypassed and nothing else changes: --force
never deletes more than a plain teardown does.

Either way the teardown first establishes authoritative endpoint absence, then archives the retired generation's report under a generation-bound name such as
`report-g<generation>.md`. If a retry finds that archive after archival was
already attempted for the active cleanup claim, a reappeared `report.md` is
preserved under the smallest unused suffix, such as `report-g<generation>-2.md`;
all such archive names count as report evidence. The attempt marker is recorded
in the fenced cleanup claim before the rename, so ownership is not inferred from
hard links, symlinks, or other filesystem witnesses. Before that marker exists,
an existing generation archive is an unproved collision and teardown refuses
rather than guessing. The archived entry remains available for inspection, and
because the safety check reads only the unarchived name, a task reopened to a
later generation cannot pass its own teardown on evidence an earlier generation
wrote. A report written after the teardown fence has closed remains outside that
recheck and must eventually be generation-named by the report writer.

The data directory is never removed by teardown, including with `--force`.
Session-start reclamation runs after the 24h grace period under the task
Authority's fence: an Authority-confirmed released task (retired with a
terminal or absent cleanup claim, or superseded by transfer) may have its
directory reclaimed, including a leftover brief, while any report evidence
always keeps the directory. If no Authority record exists, the sweep reclaims the directory
only when it has no brief; a brief is retained as the only evidence that an
unknown directory may still be intended for a task.

## 5. Decision-hold scout gate

Scout tasks (`--kind scout`) investigate a question rather than shipping
code. When a scout finds a decision the general must make, it records a
structured hold before completing.

### Recording a hold (Phase A — protocol only)

```sh
munsu task status <scout-id> "needs-decision" "<key>: <summary>"
munsu task block <dependent-id> --by <scout-id>
```

### Recording the general's answer

```sh
munsu task status <scout-id> "resolved" "<key>: <answer>"
munsu task unblock <dependent-id>
```

### Scout teardown with open holds

Teardown refuses when the scout's meta contains a `needs-decision` state
in its status log. Use `--force` only when the operator has explicitly
decided to skip the safety checks:

```sh
munsu teardown <scout-id>          # refuses with unresolved holds
munsu teardown <scout-id> --force  # skips the safety checks
```

**Design principle:** The scout gathers evidence. The general decides.
Holds ensure the decision is tracked, not lost, when the scout session
ends.

## 6. Fleet management

```sh
munsu fleet view                    # snapshot + render all tasks
munsu fleet sync                    # fast-forward project clones
munsu guard                         # post-action safety check
munsu afk                           # away-mode supervision daemon
```

## 7. Away-mode (AFK supervision)

When the general is away, the AFK daemon supervises the fleet autonomously. It
triages wakes, accumulates a digest, and detects wedge conditions. It never
writes to the general pane — you read the digest on return (ADR-0013).

```sh
munsu afk                        # start the away-mode daemon (foreground)
^Z bg                             # background it, or start in a dedicated pane
```

The daemon runs a 30s poll loop:
1. Triage the wake queue
2. Feed results into the digester (60s window)
3. Check wedge conditions (stale beat, missing beat, repeated wake)
4. Flush the batched digest to `state/.afk-digest` when the window expires

On return:

```sh
munsu afk return                  # stop daemon, drain digest, print summary
munsu afk return check             # exit 0 = clean, 1 = actionable items remain
```

**Details:** `docs/skills/afk.md` covers the full AFK contract — consent flag,
identity lock, sentinel marker, batched digest, wedge detection, target safety
gates, and the return catch-up gate.

## 8. Guard supervision loop summary

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
          │  munsu wake claim│  claim queued wakes under a lease
          │  munsu soldier-state│  ground truth per task
          └────────┬─────────┘
                   │ re-arm if tasks in flight
          ┌────────▼─────────┐
          │  munsu watch-arm │  (if not already running)
          │  munsu guard     │  post-action safety check
          └──────────────────┘
```

## 9. Safety rules for self-hosting

1. **Isolated home:** Always use a dedicated home (e.g. `~/.munsu-selfhost`)
   for self-hosting. Never share the live captain home.
2. **Never flip live captain:** Do not run `munsu watch-arm` inside the
   live captain home (`~/.munsu`).
3. **Watcher singleton:** Only one watcher process per home. The flock lock
   at `state/.lock` enforces this.
4. **Guard after every action:** Run `munsu guard` after spawn, teardown,
   and fleet actions.
5. **Isolated session/workspace:** Use a dedicated herdr workspace or tmux
   session scoped to each home (munsu auto-derives from home path). Avoid
   sharing a session/workspace across test homes to prevent interference.
6. **Herdr session vs workspace label:** The Herdr session name (server name,
	   e.g. `default` or a lab session) is **not** the same as the workspace label
	   (home-derived hometag like `e7b346`). The `session.Resolve` function for
	   the "herdr" backend uses `""` → `HERDR_SESSION` → `"default"`, *never*
	   the hometag. The hometag is the workspace label passed separately by spawn
	   to `NewWindow`. After spawn, lifecycle commands (peek, send, teardown)
	   reconstruct the backend via `BackendForTask`, which reads the session from
	   `meta["herdr_session"]` or the window handle prefix. Do not confuse
	   these two values in meta or config.
	   dogfood tests. Example: `mkdir -p /tmp/munsu-dogfood && MUNSU_HOME=/tmp/munsu-dogfood munsu init`.

## State file rename (item-5 transition)

In a future release, the legacy state suffixes `.check.sh` and `.turn-ended`
will no longer be cleaned up during teardown. The canonical names are now
`.check` and `.turnend`.

Munsu never writes a `.check`. Per-task checks are authored externally — by
the operator or an agent — and placed in the state directory. Munsu discovers
and validates these files for watcher polling and wake generation. See the
canonical [Supervision check-plugin contract](../SUPERVISION.md#check-plugins)
for refusal reporting, wake suppression, and artifact-preservation behavior.
Task teardown removes leftover `<id>.check` files. During teardown, munsu recognizes **both**
old and new suffixes. If you have scripts that write `.check.sh` files into the
state directory, change them to write `.check`. Existing `.check.sh` and
`.turn-ended` files will still be cleaned up. No immediate action required.
