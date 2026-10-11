# Supervision loop — watch / wake / guard / afk

This document details the soldier supervision loop. For the high-level step, see `SKILL.md` step 5.

## Watch loop (`munsu watch`)

The watcher is a singleton persistent event-driven daemon. It queues actionable wake reasons and continues polling until stopped.

- **Ensure:** `munsu watch ensure [--restart]`
- **Run once:** `munsu watch run`
- **Status:** `munsu watch status`
- **Stop:** `munsu watch stop`
- **Agent-run forms:** only `munsu watch status|ensure|stop|run` and `--help`/`-h`; the safety hook refuses bare `munsu watch` (the daemon `ensure` starts).
- **Singleton:** Home-scoped lock prevents multiple concurrent watchers.

## Wake handling (`munsu wake`)

Reports from soldiers and captains reach the General only as queued wakes; nothing writes a report into the General pane.

Under herdr with a claude General, `munsu session-start` records `general-pane` and `wake-delivery-mode=herdr`, and the watcher daemon submits each queued wake to the General pane as a `[mu-system:wake]` prompt when the General is idle with an empty composer. The prompt carries `key`, `claim_id`, `event_id`, the wake line and the exact command to run: handle the wake, then `munsu wake resolve --claim-id <claim_id> --event-id <event_id> --summary <text>` with a non-empty summary. Read ground truth with `munsu soldier-state <id>`, not a raw status tail.

A dispatch refused before the claim leaves the wake queued: AFK is on, the harness is not claude, the composer holds input or is not recognized, or the General is busy. A failure after the claim (the prompt is not accepted) leaves the wake claimed until its 300 s lease expires, after which the next `munsu wake claim` reclaims it. `munsu inbox` shows queued and claimed wakes.

Settling an uplink wake, by `munsu wake resolve` or `munsu wake ack`, consumes the wake and also accepts the soldier's report: one ledger, so run no `munsu inbox ack` for a wake. The sender's pending report then retires on the watcher's next recovery pass, and teardown's acknowledgment check passes after that. If the report cannot be accepted the wake is still settled and the command prints the report's ref: run `munsu inbox ack '<ref>'` once the cause is fixed. `munsu afk return` accepts every uplink report in the digest it prints and lists any it could not accept with the same hint. `munsu inbox ack` otherwise stays for captain mailbox notifications.

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

When stepping away from the fleet, start the away-mode daemon:

```sh
munsu afk
```

This sets an AFK flag and polls the fleet at a reduced cadence. Only general-relevant events (done, failed, needs-decision) are printed. Stop with SIGTERM/SIGINT; the flag is cleared on stop.

## Stuck soldier recovery

If a soldier is unresponsive, do not re-implement recovery logic here. Run `munsu skill show stuck-soldier-recovery` for the full escalation ladder:

1. `munsu peek <id>` — read last N lines.
2. `munsu send <id> "<instruction>"` — steer.
3. Interrupt (harness-specific: Ctrl+C via tmux/herdr send-keys).
4. `munsu teardown <id> --force` then `munsu spawn <id> <project>` — relaunch.
   Teardown retains the task data directory and any report under its generation-bound name.
5. Append `failed:` only after all above exhausted.
