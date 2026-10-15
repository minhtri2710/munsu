# Supervision loop — watch / wake / guard / afk

This document details the soldier supervision loop. For the high-level step, see `SKILL.md` step 5.

## Watch loop (`munsu watch`)

The watcher is a singleton persistent event-driven daemon. It polls every 5 s, queues actionable wake reasons in the durable queue and continues polling until stopped.

- **Ensure:** `munsu watch ensure [--restart]` returns within the beacon timeout (3 s), plus up to 2 s to stop a holder on `--restart` or a stalled holder, with the watcher state (`attached`, `healthy` or `failed`). Neither `watch ensure` nor `watch run` prints `signal:`, `stale:`, `check:` or `heartbeat` lines or notifies on completion.
- **Run once:** `munsu watch run`
- **Status:** `munsu watch status`
- **Stop:** `munsu watch stop`
- **Agent-run forms:** only `munsu watch status|ensure|stop|run` and `--help`/`-h`; the safety hook refuses bare `munsu watch` (the daemon `ensure` starts).
- **Singleton:** Home-scoped lock prevents multiple concurrent watchers.

## Wake handling (`munsu wake`)

Reports from soldiers and captains reach the General only as queued wakes; nothing writes a report into the General pane.

Under herdr with a claude General, `munsu session-start` records `general-pane` and `wake-delivery-mode=herdr`, and the watcher daemon submits each queued wake to the General pane as a `[mu-system:wake]` prompt when the General is idle with an empty composer. The prompt carries `key`, `claim_id`, `event_id`, the wake line and the exact command to run: handle the wake, then `munsu wake resolve --claim-id <claim_id> --event-id <event_id> --summary <text>` with a non-empty summary. Read ground truth with `munsu soldier-state <id>`, not a raw status tail.

A dispatch refused before the claim leaves the wake queued: AFK is on, the harness is not claude, the composer holds input or is not recognized, or the General is busy. A failure after the claim (the prompt is not accepted) leaves the wake claimed until its 300 s lease expires, after which the next `munsu wake claim` reclaims it. `munsu inbox` shows queued and claimed wakes.

A silent seat is one `stale` wake per incident: its status file has not changed for `seat-silent-after` (default 5m) and its pane is not provably working. A seat that appends the same `failed:` or `blocked:` line `seat-repeat-nudge` times (default 5) produces one `stale` wake telling you to steer it, and one at `seat-repeat-end` (default 8) telling you it is stuck. A silent incident ends on any status write, and a repeat incident ends when the seat writes a different line. Set the keys with `munsu config set`; an invalid hand-edited value stops the watcher with the key and its valid range.

Settling a report wake, by `munsu wake resolve` or `munsu wake ack`, consumes the wake and settles the report it carries; `munsu afk return` settles every report in the digest it prints. Teardown refuses while a task has a report its parent has not settled; `munsu teardown --force` retires it anyway. Settling a scout's `done` report completes the scout task; if that fails the report is still settled and the command prints the scout's task ID with the hint to run `munsu task done <id>` once the cause is fixed. If settling the report itself fails, the report is not settled and still holds its task's teardown, and nothing settles it again: the hint says to retire the task with `munsu teardown <id> --force` once the cause is fixed. A Captain's soldiers report into the Captain's home, and its watcher delivers their wakes into the Captain's own pane, read from the captain task meta in the General home, never into the General pane. A Captain running claude on herdr gets each wake as a prompt (its home's `wake-delivery-mode` defaults to `herdr`); any other Captain learns of them at its next command, turn end or wake claim.

On any other backend or harness nothing is delivered. Claim under a lease, then settle each event:

```sh
munsu wake claim --consumer <id>     # claim a bounded batch under a lease
munsu soldier-state <id>             # read ground truth, not a raw status tail
munsu wake resolve --claim-id <lease-id> --event-id <event-id> --summary <text>
munsu wake ack <lease-id> <event-id> # or consume an event without a summary
```

After handling actionable wakes, confirm the persistent watcher remains healthy if tasks are still in flight:

```sh
munsu watch ensure
```

## Post-action guard (`munsu guard`)

Run `munsu guard` after every fleet action. It checks for:
- **Tangle**: primary checkout has a non-default branch checked out.
- **Stale watcher**: watcher lock is held but no process is alive.

## Away mode (`munsu afk`)

When the Human steps away, run `munsu afk` in its own pane. The foreground daemon takes the identity lock `state/.afk.lock`, sets the consent flag `state/.afk`, clears stale `state/.seen-*` and `state/.subsuper-*` markers, then every 30 s triages wakes into the batched digest `state/.afk-digest` (flushed every 60 s; `afk-digest-window` overrides). A stale or missing watcher beat adds one `[wedge]` entry per incident, so keep the watcher running. The daemon never repairs, merges, approves or writes to the General's pane; the watcher's wake dispatch defers every wake while `state/.afk` exists. SIGTERM/SIGINT stops it and clears the flag and lock. Every operation is scoped to one `MUNSU_HOME`.

On return:

```sh
munsu afk return        # stop the daemon, print escalations and blocked items, settle drained report wakes
munsu afk return check  # exit 0 when clean, non-zero while actionable state remains
```

`munsu afk return` is idempotent. A scout report whose task it could not complete is listed with `munsu task done <id>`, to run once the cause is fixed; a report it could not settle is listed with `munsu teardown <id> --force`.

## Stuck soldier recovery

If a soldier is unresponsive, do not re-implement recovery logic here. Run `munsu skill show stuck-soldier-recovery` for the full escalation ladder:

1. `munsu peek <id>` — read last N lines.
2. `munsu send <id> "<instruction>"` — steer.
3. Interrupt (harness-specific: Ctrl+C via tmux/herdr send-keys).
4. `munsu teardown <id> --force` then `munsu spawn <id> <project>` — relaunch.
   Teardown retains the task data directory and any report under its generation-bound name.
5. Append `failed:` only after all above exhausted.
