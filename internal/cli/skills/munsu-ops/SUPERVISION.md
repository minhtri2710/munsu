# Supervision loop — watch / wake / guard / afk

This document details the soldier supervision loop. For the high-level step, see `SKILL.md` step 5.

## Watch loop (`munsu watch`)

The watcher is a singleton persistent event-driven daemon. It queues actionable wake reasons and continues polling until stopped.

- **Ensure:** `munsu watch ensure [--restart]`
- **Run once:** `munsu watch run`
- **Status:** `munsu watch status`
- **Stop:** `munsu watch stop`
- **Singleton:** Home-scoped lock prevents multiple concurrent watchers.

## Wake handling (`munsu wake`)

When the watcher queues actionable events, prefer lease-based handling:

```sh
munsu wake claim --consumer <id>     # claim a bounded batch under a lease
munsu soldier-state <id>             # read ground truth, not a raw status tail
munsu wake ack <lease-id> <event-id> # acknowledge each processed event
```

`munsu wake claim` is the lease-based wake queue surface; claim, resolve, and ack are the canonical operations.

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
   `--force` skips the safety checks only; a `report.md` the stuck soldier
   already wrote survives the teardown, so read it before re-spawning.
5. Append `failed:` only after all above exhausted.
